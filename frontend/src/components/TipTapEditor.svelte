<script lang="ts">
  import { onDestroy } from 'svelte'
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
    blocksToDoc,
    docToBlocks
  } from '../lib/editor'
  import type { ParsedBlock } from '../lib/editor'
  import { settings } from '../settings/store.svelte'
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

  const initialDoc = blocksToDoc(blocks)
  const initialKey = `${blocks.map((b) => b.id).join(',')}:${blocks.length}`
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
      void releaseFocus()
      flushPendingSave()
      onBlockBlur?.()
    },
    onCreate: ({ editor }) => {
      editorInstance = editor as Editor
    }
  })

  onDestroy(() => {
    stopHeartbeat()
    flushPendingSave()
    void releaseFocus()
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
    const updatedBlocks = docToBlocks(editorInstance.getJSON())
    try {
      await SaveFileBlocks(notebook, section, page, updatedBlocks)
    } catch (e) {
      console.error('TipTapEditor: SaveFileBlocks failed:', e)
    }
    onUpdate(updatedBlocks)
  }

  function flushPendingSave(): void {
    if (saveTimeout) {
      clearTimeout(saveTimeout)
      saveTimeout = null
      void doSave()
    }
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
    }
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
</div>

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
</style>
