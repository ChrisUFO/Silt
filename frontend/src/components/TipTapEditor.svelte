<script lang="ts">
  import { onDestroy, untrack } from 'svelte'
  import { createEditor, EditorContent } from 'svelte-tiptap'
  import type { Editor } from 'svelte-tiptap'
  import StarterKit from '@tiptap/starter-kit'
  import Placeholder from '@tiptap/extension-placeholder'
  import {
    SaveFileBlocks,
    AcquireFocusLock,
    RefreshFocusLock,
    ReleaseFocusLock
  } from '../../wailsjs/go/main/App.js'
  import {
    SiltBlockExtensionsWithNodeViews,
    UniqueBlockIds,
    SiltBlockKeymaps,
    TaskMetaSuggest,
    applyMetaSuggestion,
    filterMetaKeys,
    blocksToDoc,
    docToBlocks
  } from '../lib/editor'
  import type { ParsedBlock, MetaKey, SuggestContext } from '../lib/editor'
  import TemplatePicker from '../templates/TemplatePicker.svelte'
  import { settings } from '../settings/store.svelte'
  import { measureFrameBudget } from '../lib/perf/frame-budget'
  import CommandPalette from './CommandPalette.svelte'

  interface Props {
    notebook: string
    section: string
    page: string
    blocks: ParsedBlock[]
    activeFocusedBlockAncestors?: string[]
    onBlockFocus?: (blockId: string, ancestors: string[]) => void
    onBlockBlur?: () => void
    onUpdate: (updatedBlocks: ParsedBlock[]) => void
  }

  let {
    notebook,
    section,
    page,
    blocks,
    activeFocusedBlockAncestors = [],
    onBlockFocus,
    onBlockBlur,
    onUpdate
  }: Props = $props()

  let editorInstance: Editor | null = null
  let saveTimeout: ReturnType<typeof setTimeout> | null = null
  let heartbeatInterval: ReturnType<typeof setInterval> | null = null
  let hasFocusLock = false
  let isFocused = $state(false)
  let suppressUpdate = false
  let showSlashMenu = $state(false)
  let showTemplatePicker = $state(false)

  // --- Task metadata suggest (%-autocomplete) ------------------------------
  // `metaPopup` is null when the popup is closed. While open it carries the
  // active context (range/position), the filtered key list, and the
  // highlighted index navigated by ↑/↓.
  let metaPopup = $state<{
    ctx: SuggestContext
    items: MetaKey[]
    selected: number
  } | null>(null)

  function onMetaChange(ctx: SuggestContext | null): void {
    if (!ctx) {
      metaPopup = null
      return
    }
    const items = filterMetaKeys(ctx.query)
    metaPopup = items.length === 0 ? null : { ctx, items, selected: 0 }
  }

  function onMetaNavigate(dir: 1 | -1): void {
    if (!metaPopup) return
    const n = metaPopup.items.length
    metaPopup.selected = (metaPopup.selected + dir + n) % n
  }

  function onMetaSelectActive(): void {
    if (!metaPopup || !editorInstance || editorInstance.isDestroyed) {
      metaPopup = null
      return
    }
    const item = metaPopup.items[metaPopup.selected]
    metaPopup = null
    if (item) applyMetaSuggestion(editorInstance, item.key)
  }

  function onMetaPick(key: string): void {
    if (!editorInstance || editorInstance.isDestroyed) {
      metaPopup = null
      return
    }
    metaPopup = null
    applyMetaSuggestion(editorInstance, key)
  }

  function metaPopupCoords(): { left: number; top: number } | null {
    if (!metaPopup || !editorInstance || editorInstance.isDestroyed) return null
    const c = editorInstance.view.coordsAtPos(metaPopup.ctx.from)
    return { left: c.left, top: c.bottom }
  }

  // Capture the initial blocks under untrack to signal that the one-shot
  // capture is intentional — the $effect below handles live reactivity (#64).
  const initialDoc = untrack(() => blocksToDoc(blocks))
  const initialKey = untrack(() => `${blocks.map((b) => b.id).join(',')}:${blocks.length}`)
  let lastSyncedBlocksKey = $state(initialKey)
  const editorStore = createEditor({
    extensions: [
      StarterKit.configure({
        paragraph: false,
        heading: false,
        bulletList: false,
        orderedList: false,
        listItem: false,
        blockquote: false,
        codeBlock: false,
        horizontalRule: false,
        trailingNode: false
      }),
      ...SiltBlockExtensionsWithNodeViews,
      UniqueBlockIds,
      TaskMetaSuggest.configure({
        onChange: onMetaChange,
        onNavigate: onMetaNavigate,
        onSelectActive: onMetaSelectActive
      }),
      SiltBlockKeymaps,
      Placeholder.configure({
        placeholder: 'Type / for commands, or start writing…'
      })
    ],
    content: initialDoc,
    onUpdate: () => {
      if (suppressUpdate) return
      detectSlashCommand()
      triggerAutoSave()
    },
    onFocus: () => {
      isFocused = true
      acquireFocus()
      startHeartbeat()
      notifyFocus()
    },
    onBlur: () => {
      isFocused = false
      stopHeartbeat()
      // Flush the pending save BEFORE releasing the focus lock so an embed's
      // MutateBlock retry sees the just-saved content rather than overwriting
      // it (#64). The save is awaited, then the lock is released.
      void flushPendingSave().then(() => releaseFocus())
      onBlockBlur?.()
    },
    onCreate: ({ editor }) => {
      editorInstance = editor as Editor
    }
  })

  onDestroy(() => {
    stopHeartbeat()
    void flushPendingSave().then(() => releaseFocus())
  })

  // --- External content sync ------------------------------------------------
  $effect(() => {
    const key = `${blocks.map((b) => b.id).join(',')}:${blocks.length}`
    if (!editorInstance || editorInstance.isDestroyed) return
    if (key === lastSyncedBlocksKey) return
    // Don't clobber the editor's content while the user is actively editing.
    // The editor is the source of truth until blur; external updates wait.
    if (isFocused) return
    lastSyncedBlocksKey = key

    suppressUpdate = true
    editorInstance.commands.setContent(blocksToDoc(blocks), {
      emitUpdate: false
    })
    suppressUpdate = false
  })

  // --- Auto-save (debounced, config-driven, same contract as legacy) --------

  function triggerAutoSave(): void {
    if (saveTimeout) {
      clearTimeout(saveTimeout)
      saveTimeout = null
    }
    const delay = Math.max(
      settings.config?.editor?.auto_save_delay_ms ?? 500,
      50
    )
    saveTimeout = setTimeout(() => {
      saveTimeout = null
      void doSave()
    }, delay)
  }

  async function doSave(): Promise<void> {
    if (!editorInstance || editorInstance.isDestroyed) return
    const updatedBlocks = measureFrameBudget('tiptap-transaction', () =>
      docToBlocks(editorInstance.getJSON())
    )
    try {
      await SaveFileBlocks(notebook, section, page, updatedBlocks)
    } catch (e) {
      console.error('TipTapEditor: SaveFileBlocks failed:', e)
    }
    onUpdate(updatedBlocks)
  }

  function flushPendingSave(): Promise<void> {
    if (saveTimeout) {
      clearTimeout(saveTimeout)
      saveTimeout = null
      return doSave()
    }
    return Promise.resolve()
  }

  // --- Slash menu -----------------------------------------------------------

  function detectSlashCommand(): void {
    if (!editorInstance || editorInstance.isDestroyed) return
    const sel = editorInstance.state.selection
    const textBefore = sel.$from.parent.textContent.slice(
      0,
      sel.$from.parentOffset
    )
    if (textBefore === '/') {
      showSlashMenu = true
    } else if (showSlashMenu && !textBefore.startsWith('/')) {
      showSlashMenu = false
    }
  }

  function changeBlockType(
    type: string,
    newAttrs: Record<string, unknown>
  ): void {
    if (!editorInstance || editorInstance.isDestroyed) return
    const pos = editorInstance.state.selection.$from
    for (let d = pos.depth; d >= 1; d--) {
      const node = pos.node(d)
      if (['noteBlock', 'taskBlock', 'headerBlock'].includes(node.type.name)) {
        const mergedAttrs = {
          ...node.attrs,
          ...newAttrs
        }
        if (type === 'taskBlock') {
          delete (mergedAttrs as Record<string, unknown>).bullet
        } else if (type === 'headerBlock') {
          delete (mergedAttrs as Record<string, unknown>).bullet
          delete (mergedAttrs as Record<string, unknown>).status
          delete (mergedAttrs as Record<string, unknown>).owner
          delete (mergedAttrs as Record<string, unknown>).start_date
          delete (mergedAttrs as Record<string, unknown>).due_date
          delete (mergedAttrs as Record<string, unknown>).priority
        } else if (type === 'noteBlock') {
          delete (mergedAttrs as Record<string, unknown>).status
          delete (mergedAttrs as Record<string, unknown>).owner
          delete (mergedAttrs as Record<string, unknown>).start_date
          delete (mergedAttrs as Record<string, unknown>).due_date
          delete (mergedAttrs as Record<string, unknown>).priority
          if (mergedAttrs.bullet === undefined) {
            ;(mergedAttrs as Record<string, unknown>).bullet = '- '
          }
        }
        editorInstance.commands.setNode(type, mergedAttrs)
        return
      }
    }
  }

  function handleSlashSelect(commandId: string): void {
    showSlashMenu = false
    if (!editorInstance || editorInstance.isDestroyed) return

    const sel = editorInstance.state.selection
    const from = sel.$from.start()
    const to = from + sel.$from.parentOffset
    editorInstance.commands.deleteRange({ from, to })

    if (commandId === 'todo') {
      changeBlockType('taskBlock', { status: 'TODO' })
    } else if (commandId === 'h1') {
      changeBlockType('headerBlock', { depth: 1 })
    } else if (commandId === 'today') {
      const d = new Date()
      const today = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
      editorInstance.commands.insertContent(today)
    } else if (commandId === 'embed') {
      editorInstance.commands.insertContent('{{embed:')
    } else if (commandId === 'template') {
      // The `/` text is already deleted above; open the picker. The editor
      // preserves its selection state, so when the user confirms the rendered
      // blocks are inserted at the cursor position (ARCHITECTURE §5.1 — the
      // UniqueBlockIds extension mints fresh UUIDs for the inserted nodes).
      showTemplatePicker = true
    }
  }

  // Insert rendered template blocks at the cursor. Called by the TemplatePicker
  // (insert mode) via onInsertBlocks. The blocksToDoc converter produces
  // ProseMirror node JSON; insertContent inserts at the current selection.
  // UniqueBlockIds (appendTransaction) guards against any UUID collision.
  function handleTemplateInsert(blocks: ParsedBlock[]): void {
    if (!editorInstance || editorInstance.isDestroyed) return
    const doc = blocksToDoc(blocks)
    editorInstance.commands.insertContent(doc.content)
    editorInstance.commands.focus()
  }

  // --- Focus lock (reuses the #38 TTL-lease bindings) -----------------------

  async function acquireFocus(): Promise<void> {
    try {
      await AcquireFocusLock(notebook, section, page)
      hasFocusLock = true
    } catch (e) {
      console.error('TipTapEditor: AcquireFocusLock failed:', e)
    }
  }

  async function releaseFocus(): Promise<void> {
    if (!hasFocusLock) return
    hasFocusLock = false
    try {
      await ReleaseFocusLock(notebook, section, page)
    } catch (e) {
      console.error('TipTapEditor: ReleaseFocusLock failed:', e)
    }
  }

  function startHeartbeat(): void {
    stopHeartbeat()
    heartbeatInterval = setInterval(() => {
      RefreshFocusLock(notebook, section, page).catch(() => {
        // Transient IPC error — the next tick retries.
      })
    }, 20000)
  }

  function stopHeartbeat(): void {
    if (heartbeatInterval !== null) {
      clearInterval(heartbeatInterval)
      heartbeatInterval = null
    }
  }

  // --- Focus ancestry notification (for guide-rail highlighting) -----------

  function notifyFocus(): void {
    if (!onBlockFocus) return
    if (!editorInstance || editorInstance.isDestroyed) return
    const pos = editorInstance.state.selection.$from
    // Walk up to depth 1 (direct child of doc) to find the block node.
    for (let d = pos.depth; d >= 1; d--) {
      const node = pos.node(d)
      if (node && node.attrs && (node.attrs as Record<string, unknown>).id) {
        const blockId = (node.attrs as Record<string, unknown>).id as string
        // Build ancestor chain: all parent block ids from root to this block.
        const ancestors: string[] = [blockId]
        for (let pd = d - 1; pd >= 1; pd--) {
          const pnode = pos.node(pd)
          if (
            pnode &&
            pnode.attrs &&
            (pnode.attrs as Record<string, unknown>).id
          ) {
            ancestors.unshift(
              (pnode.attrs as Record<string, unknown>).id as string
            )
          }
        }
        onBlockFocus(blockId, ancestors)
        return
      }
    }
  }
</script>

<div class="tiptap-editor-host" class:focused={isFocused}>
  {#if editorStore}
    <EditorContent editor={$editorStore} />
  {/if}
  {#if showSlashMenu}
    <CommandPalette
      onSelect={handleSlashSelect}
      onClose={() => (showSlashMenu = false)}
    />
  {/if}
  {#if metaPopup}
    {@const c = metaPopupCoords()}
    {#if c}
      <div class="meta-suggest" style="left:{c.left}px; top:{c.top}px">
        {#each metaPopup.items as item, i}
          <button
            type="button"
            class="meta-suggest-item"
            class:selected={i === metaPopup.selected}
            role="option"
            aria-selected={i === metaPopup.selected}
            onclick={() => onMetaPick(item.key)}
          >
            <span class="meta-suggest-key">{item.key}</span>
            <span class="meta-suggest-desc">{item.description}</span>
          </button>
        {/each}
      </div>
    {/if}
  {/if}
</div>

{#if showTemplatePicker}
  <TemplatePicker
    mode="insert"
    onClose={() => (showTemplatePicker = false)}
    onInsertBlocks={handleTemplateInsert}
  />
{/if}

<style>
  .tiptap-editor-host {
    width: 100%;
  }

  /* The ProseMirror editable surface. Global styles (typography vars, guide
     rails, indentation, node rendering) live in index.css under .ProseMirror
     and [data-type] selectors so they apply to all editor instances. */
  .tiptap-editor-host :global(.ProseMirror) {
    min-height: 22px;
    outline: none;
  }

  .meta-suggest {
    position: fixed;
    z-index: 50;
    min-width: 240px;
    margin-top: 4px;
    padding: 4px;
    border-radius: 8px;
    background: var(--bg-surface, #1e1e22);
    border: 1px solid var(--border-subtle, #33333a);
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.35);
    display: flex;
    flex-direction: column;
  }

  .meta-suggest-item {
    display: flex;
    align-items: baseline;
    gap: 10px;
    padding: 6px 8px;
    border: none;
    border-radius: 6px;
    background: transparent;
    color: var(--text-primary, #e6e6e6);
    text-align: left;
    cursor: pointer;
    font-family: inherit;
  }

  .meta-suggest-item.selected {
    background: var(--accent-primary-start, #4f7cff);
    color: #fff;
  }

  .meta-suggest-key {
    font-family: var(--font-mono, monospace);
    font-weight: 600;
    font-size: 0.85rem;
    min-width: 64px;
  }

  .meta-suggest-desc {
    font-size: 0.8rem;
    opacity: 0.8;
  }
</style>
