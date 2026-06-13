<script lang="ts">
  import { onDestroy, tick } from 'svelte'
  import {
    SaveFileBlocks,
    AcquireFocusLock,
    ReleaseFocusLock
  } from '../../wailsjs/go/main/App.js'
  import CommandPalette from './CommandPalette.svelte'

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
  let hasFocusLock = false

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
    } else if (commandId === 'kanban') {
      block.clean_text = ''
      window.dispatchEvent(new CustomEvent('switch-view', { detail: 'kanban' }))
    } else if (commandId === 'embed') {
      block.clean_text = '{{embed:placeholder}}'
    }

    if (editableEl) {
      editableEl.innerText = block.clean_text
      editableEl.focus()
    }
    const updated = [...siblings]
    triggerAutoSave(updated)
    onUpdate(updated)
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
      await AcquireFocusLock(notebook, section, fileDate)
      hasFocusLock = true
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

  // Trigger auto-save with 500ms debounce
  function triggerAutoSave(blocksToSave = siblings) {
    if (saveTimeout) {
      clearTimeout(saveTimeout)
    }
    saveTimeout = setTimeout(() => {
      saveBlocksDirectly(blocksToSave)
      saveTimeout = null
    }, 500)
  }

  async function saveBlocksDirectly(blocksToSave = siblings) {
    try {
      await SaveFileBlocks(notebook, section, fileDate, blocksToSave)
    } catch (e) {
      console.error('Failed to save blocks:', e)
    }
  }

  async function releaseFocusLock() {
    if (!hasFocusLock) return
    hasFocusLock = false
    try {
      await ReleaseFocusLock(notebook, section, fileDate)
    } catch (e) {
      console.error('Focus unlock failed:', e)
    }
  }

  onDestroy(() => {
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
    if (e.key === 'Tab') {
      e.preventDefault()
      if (e.shiftKey) {
        // Shift+Tab: Unindent
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
        // Tab: Indent
        // Max indent is previous sibling's depth + 1
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
          class="w-2 h-2 bg-accent-indigo-end doing-indicator rounded-full"
        ></div>
      </button>
    {:else if block.status === 'DONE'}
      <button
        onclick={handleCheckboxClick}
        aria-label="Mark task as todo"
        class="w-5 h-5 mt-0.5 rounded done-check flex-shrink-0 flex items-center justify-center cursor-pointer focus:outline-none"
      >
        <span
          class="material-symbols-outlined text-accent-teal-start text-[14px] font-bold select-none"
        >
          check
        </span>
      </button>
    {/if}
  {/if}

  <!-- Content editable text segment -->
  <div class="flex-1 flex flex-wrap items-center gap-2 min-w-0 relative">
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
      class="flex-1 focus:outline-none text-on-surface leading-relaxed whitespace-pre-wrap break-words min-h-[22px] min-w-[150px]"
      class:text-text-muted={block.status === 'DONE'}
      class:line-through={block.status === 'DONE'}
    >
      {block.clean_text}
    </div>

    {#if showSlashMenu}
      <CommandPalette
        onSelect={handleCommandSelect}
        onClose={() => (showSlashMenu = false)}
      />
    {/if}

    <!-- Inline Meta Badges (Owner, Date, Priority) -->
    {#if block.type === 'TASK' && block.status !== 'DONE'}
      {#if block.owner}
        <span
          class="bg-accent-indigo-glow border border-accent-indigo-start/30 text-accent-indigo-start px-2 py-0.5 rounded text-[11px] font-label-sm select-none"
        >
          [{block.owner}]
        </span>
      {/if}

      {#if block.due_date}
        <span
          class="bg-accent-teal-glow border border-accent-teal-start/30 text-accent-teal-start px-2 py-0.5 rounded text-[11px] font-label-sm select-none"
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
