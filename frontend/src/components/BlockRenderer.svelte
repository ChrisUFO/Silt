<script lang="ts">
  import { onDestroy, tick } from 'svelte'
  import {
    SaveFileBlocks,
    AcquireFocusLock,
    RefreshFocusLock,
    ReleaseFocusLock
  } from '../../wailsjs/go/main/App.js'
  import CommandPalette from './CommandPalette.svelte'
  import RichText from './RichText.svelte'
  import BlockPickerModal from './BlockPickerModal.svelte'
  import { settings } from '../settings/store.svelte'
  import { matchHotkey } from '../settings/hotkeys'

  interface Block {
    id: string
    parent_id: string
    type: 'TASK' | 'NOTE' | 'HEADER'
    depth: number
    raw_text: string
    clean_text: string
    status: string
    owner: string
    start_date: string
    due_date: string
    priority: number
    line_number: number
  }

  interface Props {
    block: Block
    notebook: string
    section: string
    page: string
    fileDate: string
    siblings: Block[]
    blockIndex: number
    activeFocusedBlockAncestors: string[]
    onBlockFocus?: (blockId: string, ancestors: string[]) => void
    onBlockBlur?: () => void
    onUpdate: (updatedSiblings: Block[]) => void
  }

  let {
    block = $bindable(),
    notebook,
    section,
    page,
    fileDate,
    siblings,
    blockIndex,
    activeFocusedBlockAncestors,
    onBlockFocus,
    onBlockBlur,
    onUpdate
  }: Props = $props()

  let editableEl = $state<HTMLDivElement | null>(null)
  let isFocused = $state(false)
  let saveTimeout = $state<any>(null)
  let showSlashMenu = $state(false)
  let showBlockPicker = $state(false)
  let hasFocusLock = false
  // Heartbeat that extends the backend focus lease while the editor stays
  // focused. The Go TTL is 60s; a 20s refresh keeps it comfortably alive
  // and lets a crashed/unmounted editor self-heal (#38). Cleared on blur and
  // component destroy.
  let heartbeatInterval = $state<ReturnType<typeof setInterval> | null>(null)

  function startHeartbeat() {
    stopHeartbeat()
    heartbeatInterval = setInterval(() => {
      // RefreshFocusLock is a no-op if the lease already expired; a stale
      // editor that lost focus without firing blur just stops refreshing.
      RefreshFocusLock(notebook, section, page, fileDate).catch(() => {
        // Ignore transient IPC errors — the next tick retries.
      })
    }, 20000)
  }

  function stopHeartbeat() {
    if (heartbeatInterval !== null) {
      clearInterval(heartbeatInterval)
      heartbeatInterval = null
    }
  }

  // Enter write mode: swap the read view (RichText) for the contenteditable
  // and move focus into it.
  async function beginEdit() {
    if (isFocused) return
    isFocused = true
    await tick()
    // Initialize the contenteditable imperatively ONCE on entry. We
    // deliberately do NOT bind {block.clean_text} inside the contenteditable:
    // a reactive text binding there + handleInput writing clean_text back is
    // a feedback loop — Svelte re-renders the text node on every keystroke,
    // the browser's contenteditable DOM has diverged from Svelte's tracked
    // node, and the re-render duplicates content (compounding per keystroke
    // into "abcdefgabcdefabcdeabcdabcaba"). The contenteditable owns its
    // text during focus; handleInput syncs OUT to clean_text (for save),
    // never back in.
    if (editableEl) {
      editableEl.innerText = block.clean_text
      editableEl.focus()
    }
  }

  async function handleCommandSelect(commandId: string) {
    showSlashMenu = false
    if (commandId === 'todo') {
      block.type = 'TASK'
      block.status = 'TODO'
      block.clean_text = ''
      block.priority = 3
    } else if (commandId === 'h1') {
      block.type = 'HEADER'
      block.depth = 1
      block.clean_text = ''
    } else if (commandId === 'today') {
      const d = new Date()
      const todayStr = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
      block.clean_text = todayStr
    } else if (commandId === 'embed') {
      // Open the block picker; insert {{embed:<id>}} once a block is chosen.
      showBlockPicker = true
    }

    if (editableEl && commandId !== 'embed') {
      editableEl.innerText = block.clean_text
      editableEl.focus()
    }
    const updated = [...siblings]
    triggerAutoSave(updated)
    onUpdate(updated)
  }

  function handleEmbedPick(blockId: string) {
    block.clean_text = `{{embed:${blockId}}}`
    const updated = [...siblings]
    triggerAutoSave(updated)
    onUpdate(updated)
    // Enter edit mode so the user sees/adjusts the embed immediately.
    beginEdit()
  }

  // Find ancestor chain for a block
  function getAncestors(blockId: string): string[] {
    const chain: string[] = []
    let curr = siblings.find((b) => b.id === blockId)
    while (curr && curr.parent_id) {
      chain.push(curr.parent_id)
      curr = siblings.find((b) => b.id === curr.parent_id)
    }
    return chain
  }

  // Determine if a guide rail at depth `d` (0-indexed) is active
  let activeGuides = $derived.by(() => {
    const guides = Array(block.depth).fill(false)
    // Respect the editor.focus_highlight_ancestors config: when explicitly
    // disabled, guide rails still render (they show indentation depth) but
    // never light up with the active highlight gradient.
    if (settings.config?.editor?.focus_highlight_ancestors === false) return guides
    if (activeFocusedBlockAncestors.length === 0) return guides

    // Trace parent chain for this block
    let chain: string[] = []
    let curr = block
    while (curr && curr.parent_id) {
      chain.unshift(curr.parent_id)
      curr = siblings.find((b) => b.id === curr.parent_id) as Block
    }

    // A guide rail at depth `i` is active if the ancestor at depth `i` is in activeFocusedBlockAncestors
    for (let i = 0; i < block.depth; i++) {
      if (i < chain.length && activeFocusedBlockAncestors.includes(chain[i])) {
        guides[i] = true
      }
    }
    return guides
  })

  // Handle focus
  async function handleFocus() {
    isFocused = true
    try {
      await AcquireFocusLock(notebook, section, page, fileDate)
      hasFocusLock = true
      startHeartbeat()
    } catch (e) {
      console.error('Focus lock failed:', e)
    }

    // Notify parent about focus and ancestors
    const ancestors = getAncestors(block.id)
    // Include the block's own ID as part of the focus highlight chain
    if (onBlockFocus) {
      onBlockFocus(block.id, [block.id, ...ancestors])
    }
  }

  // Handle blur
  async function handleBlur() {
    isFocused = false
    stopHeartbeat()
    await releaseFocusLock()

    if (onBlockBlur) {
      onBlockBlur()
    }

    // Perform immediate save on blur if changes are pending
    if (saveTimeout) {
      clearTimeout(saveTimeout)
      saveTimeout = null
      saveBlocksDirectly()
    }
  }

  // Trigger auto-save with a config-driven debounce (editor.auto_save_delay_ms).
  // A floor of 50ms is enforced even when the user configures 0: each save
  // performs an atomic file write (temp + fsync + rename) and triggers a
  // re-index, so per-keystroke saves would thrash the disk. 50ms is
  // imperceptible to the user but coalesces rapid typing into one write.
  // The value is read fresh on every call so config changes apply live.
  function triggerAutoSave(blocksToSave = siblings) {
    if (saveTimeout) {
      clearTimeout(saveTimeout)
      saveTimeout = null
    }
    const delay = Math.max(settings.config?.editor?.auto_save_delay_ms ?? 500, 50)
    saveTimeout = setTimeout(() => {
      saveBlocksDirectly(blocksToSave)
      saveTimeout = null
    }, delay)
  }

  async function saveBlocksDirectly(blocksToSave = siblings) {
    try {
      await SaveFileBlocks(notebook, section, page, fileDate, blocksToSave)
    } catch (e) {
      console.error('Failed to save blocks:', e)
    }
  }

  async function releaseFocusLock() {
    if (!hasFocusLock) return
    hasFocusLock = false
    try {
      await ReleaseFocusLock(notebook, section, page, fileDate)
    } catch (e) {
      console.error('Focus unlock failed:', e)
    }
  }

  onDestroy(() => {
    stopHeartbeat()
    void releaseFocusLock()
  })

  // Checkbox state cycle: TODO -> DOING -> DONE -> TODO
  async function handleCheckboxClick() {
    let nextStatus = 'TODO'
    if (block.status === 'TODO') {
      nextStatus = 'DOING'
    } else if (block.status === 'DOING') {
      nextStatus = 'DONE'
    }

    block.status = nextStatus

    // Also update raw text to keep it synchronized
    const updated = [...siblings]
    triggerAutoSave(updated)
    onUpdate(updated)
  }

  // Handle keydown for outliner actions
  async function handleKeyDown(e: KeyboardEvent) {
    if (e.key === 'Escape' && showSlashMenu) {
      e.preventDefault()
      showSlashMenu = false
      return
    }
    // Config-driven indent / unindent hotkeys. Default bindings are Tab /
    // Shift+Tab, but the user can remap or disable them from Settings → General.
    const hk = settings.config?.hotkeys ?? {}
    const isIndent = matchHotkey(e, hk.indent_block)
    const isUnindent = matchHotkey(e, hk.unindent_block)
    if (isIndent || isUnindent) {
      e.preventDefault()
      if (isUnindent) {
        // Unindent
        if (block.depth > 0) {
          const updated = getUpdatedParentIDs(
            siblings.map((b, idx) =>
              idx === blockIndex ? { ...b, depth: b.depth - 1 } : b
            )
          )
          triggerAutoSave(updated)
          onUpdate(updated)
        }
      } else {
        // Indent — max indent is previous sibling's depth + 1
        let maxDepth = 0
        if (blockIndex > 0) {
          maxDepth = siblings[blockIndex - 1].depth + 1
        }
        if (block.depth < maxDepth) {
          const updated = getUpdatedParentIDs(
            siblings.map((b, idx) =>
              idx === blockIndex ? { ...b, depth: b.depth + 1 } : b
            )
          )
          triggerAutoSave(updated)
          onUpdate(updated)
        }
      }
    } else if (e.key === 'Enter') {
      e.preventDefault()
      // Create new block at same depth below
      const newBlock: Block = {
        id: crypto.randomUUID(),
        parent_id: block.parent_id,
        type: 'NOTE',
        depth: block.depth,
        raw_text: '',
        clean_text: '',
        status: '',
        owner: '',
        start_date: '',
        due_date: '',
        priority: 3,
        line_number: block.line_number + 1
      }

      const updated = [...siblings]
      updated.splice(blockIndex + 1, 0, newBlock)
      onUpdate(updated)

      // Focus the newly created block on next tick
      await tick()
      const nextNode = document.getElementById(
        `editable-${newBlock.id}`
      ) as HTMLDivElement
      if (nextNode) {
        nextNode.focus()
      }
      triggerAutoSave(updated)
    } else if (e.key === 'Backspace' && block.clean_text === '') {
      e.preventDefault()
      // If indent exists, unindent first
      if (block.depth > 0) {
        const updated = getUpdatedParentIDs(
          siblings.map((b, idx) =>
            idx === blockIndex ? { ...b, depth: b.depth - 1 } : b
          )
        )
        triggerAutoSave(updated)
        onUpdate(updated)
      } else if (siblings.length > 1) {
        // Delete block and focus previous
        const updated = siblings.filter((b) => b.id !== block.id)
        onUpdate(updated)

        await tick()
        if (blockIndex > 0) {
          const prevNode = document.getElementById(
            `editable-${siblings[blockIndex - 1].id}`
          ) as HTMLDivElement
          if (prevNode) {
            prevNode.focus()
            // Place cursor at end of text
            const range = document.createRange()
            const sel = window.getSelection()
            range.selectNodeContents(prevNode)
            range.collapse(false)
            sel?.removeAllRanges()
            sel?.addRange(range)
          }
        }
        triggerAutoSave(updated)
      }
    } else if (e.key === 'ArrowUp' && blockIndex > 0) {
      e.preventDefault()
      const prevNode = document.getElementById(
        `editable-${siblings[blockIndex - 1].id}`
      ) as HTMLDivElement
      if (prevNode) prevNode.focus()
    } else if (e.key === 'ArrowDown' && blockIndex < siblings.length - 1) {
      e.preventDefault()
      const nextNode = document.getElementById(
        `editable-${siblings[blockIndex + 1].id}`
      ) as HTMLDivElement
      if (nextNode) nextNode.focus()
    }
  }

  // Update parent_id fields recursively based on depth matching
  function getUpdatedParentIDs(blocks: Block[]): Block[] {
    const activeIDs: string[] = []
    return blocks.map((b) => {
      const copy = { ...b }
      if (copy.depth > 0 && copy.depth - 1 < activeIDs.length) {
        copy.parent_id = activeIDs[copy.depth - 1]
      } else {
        copy.parent_id = ''
      }

      if (copy.depth >= 0) {
        while (activeIDs.length <= copy.depth) {
          activeIDs.push('')
        }
        activeIDs[copy.depth] = copy.id
        activeIDs.splice(copy.depth + 1)
      }
      return copy
    })
  }

  // Update clean content
  function handleInput(e: any) {
    const text = e.target.innerText.replace(/[\r\n]+/g, ' ')
    block.clean_text = text
    if (text === '/') {
      showSlashMenu = true
    } else {
      showSlashMenu = false
    }
    triggerAutoSave()
  }

  // Render priority label
  function getPriorityLabel(p: number) {
    if (p === 1) return '! CRITICAL'
    if (p === 2) return '! HIGH'
    return ''
  }
</script>

<div
  class="relative group flex items-start gap-3 py-1 min-h-[32px] transition-colors duration-150"
  class:bg-surface-hover={isFocused}
>
  <!-- Guide rails rendering -->
  {#each activeGuides as isActive, dIndex}
    <div
      class="guide-rail"
      class:active={isActive}
      style="left: calc({dIndex} * var(--indent-unit) + 12px)"
    ></div>
  {/each}

  <!-- Left block indent spacer -->
  <div
    style="width: calc({block.depth} * var(--indent-unit))"
    class="flex-shrink-0"
  ></div>

  <!-- Drag handle indicator -->
  <span
    class="material-symbols-outlined text-text-muted/30 group-hover:text-primary transition-colors cursor-move mt-0.5 select-none text-[18px]"
  >
    drag_indicator
  </span>

  <!-- Checkbox logic if block is a TASK -->
  {#if block.type === 'TASK'}
    {#if block.status === 'TODO'}
      <button
        onclick={handleCheckboxClick}
        aria-label="Mark task as doing"
        class="w-5 h-5 mt-0.5 rounded todo-check flex-shrink-0 cursor-pointer focus:outline-none"
      ></button>
    {:else if block.status === 'DOING'}
      <button
        onclick={handleCheckboxClick}
        aria-label="Mark task as done"
        class="w-5 h-5 mt-0.5 rounded doing-check flex-shrink-0 flex items-center justify-center cursor-pointer focus:outline-none"
      >
        <div
          class="w-2 h-2 bg-accent-secondary-end doing-indicator rounded-full"
        ></div>
      </button>
    {:else if block.status === 'DONE'}
      <button
        onclick={handleCheckboxClick}
        aria-label="Mark task as todo"
        class="w-5 h-5 mt-0.5 rounded done-check flex-shrink-0 flex items-center justify-center cursor-pointer focus:outline-none"
      >
        <span
          class="material-symbols-outlined text-accent-primary-start text-[14px] font-bold select-none"
        >
          check
        </span>
      </button>
    {/if}
  {/if}

  <!-- Content text segment: read mode (RichText) vs write mode (contenteditable) -->
  <div class="flex-1 flex flex-wrap items-center gap-2 min-w-0 relative">
    {#if isFocused}
      <!-- svelte-ignore a11y_no_static_element_interactions -->
      <div
        id="editable-{block.id}"
        bind:this={editableEl}
        contenteditable="true"
        role="textbox"
        tabindex="0"
        onfocus={handleFocus}
        onblur={handleBlur}
        onkeydown={handleKeyDown}
        oninput={handleInput}
        class="flex-1 focus:outline-none text-on-surface whitespace-pre-wrap break-words min-h-[22px] min-w-[150px]"
        style="font-family: var(--editor-font-family); font-size: var(--editor-font-size); line-height: var(--editor-line-height);"
        class:text-text-muted={block.status === 'DONE'}
        class:line-through={block.status === 'DONE'}
      ></div>
    {:else}
      <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
      <div
        id="editable-{block.id}"
        role="textbox"
        tabindex="0"
        onfocus={beginEdit}
        onclick={beginEdit}
        class="flex-1 text-on-surface whitespace-pre-wrap break-words min-h-[22px] min-w-[150px] cursor-text rounded"
        style="font-family: var(--editor-font-family); font-size: var(--editor-font-size); line-height: var(--editor-line-height);"
        class:text-text-muted={block.status === 'DONE'}
        class:line-through={block.status === 'DONE'}
      >
        {#if block.clean_text && block.clean_text.trim() !== ''}
          <RichText
            text={block.clean_text}
            {notebook}
            {section}
            {page}
            {fileDate}
          />
        {:else}
          <span class="text-text-muted/50 italic"
            >Type '/' for commands, or start writing…</span
          >
        {/if}
      </div>
    {/if}

    {#if showSlashMenu}
      <CommandPalette
        onSelect={handleCommandSelect}
        onClose={() => (showSlashMenu = false)}
      />
    {/if}

    {#if showBlockPicker}
      <BlockPickerModal
        onPick={handleEmbedPick}
        onClose={() => (showBlockPicker = false)}
      />
    {/if}

    <!-- Inline Meta Badges (Owner, Date, Priority) -->
    {#if block.type === 'TASK' && block.status !== 'DONE'}
      {#if block.owner}
        <span
          class="bg-accent-secondary-glow border border-accent-secondary-start/30 text-accent-secondary-start px-2 py-0.5 rounded text-[11px] font-label-sm select-none"
        >
          [{block.owner}]
        </span>
      {/if}

      {#if block.due_date}
        <span
          class="bg-accent-primary-glow border border-accent-primary-start/30 text-accent-primary-start px-2 py-0.5 rounded text-[11px] font-label-sm select-none"
        >
          {block.due_date}
        </span>
      {/if}

      {#if getPriorityLabel(block.priority)}
        <span
          class="bg-error-bg border border-error-border text-error px-2 py-0.5 rounded text-[11px] font-label-sm select-none"
        >
          {getPriorityLabel(block.priority)}
        </span>
      {/if}
    {/if}
  </div>
</div>
