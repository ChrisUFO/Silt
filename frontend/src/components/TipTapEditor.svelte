<script lang="ts">
  import { onDestroy, untrack } from 'svelte'
  import { createEditor, EditorContent } from 'svelte-tiptap'
  import type { Editor } from 'svelte-tiptap'
  import StarterKit from '@tiptap/starter-kit'
  import Placeholder from '@tiptap/extension-placeholder'
  import { CharacterCount, Focus } from '@tiptap/extensions'
  import Typography from '@tiptap/extension-typography'
  import {
    SaveFileBlocks,
    AcquireFocusLock,
    RefreshFocusLock,
    ReleaseFocusLock
  } from '../../wailsjs/go/main/App.js'
  import {
    SiltBlockExtensionsWithNodeViews,
    SiltInlineMarkExtensions,
    SiltColorMarkExtensions,
    UniqueBlockIds,
    SiltBlockKeymaps,
    convertToBlock,
    TaskMetaSuggest,
    applyMetaSuggestion,
    filterMetaKeys,
    blocksToDoc,
    docToBlocks
  } from '../lib/editor'
  import type { ParsedBlock, MetaKey, SuggestContext } from '../lib/editor'
  import TemplatePicker from '../templates/TemplatePicker.svelte'
  import { settings, saveConfig } from '../settings/store.svelte'
  import { themeState } from '../theme/store.svelte'
  import { measureFrameBudget } from '../lib/perf/frame-budget'
  import { pushNotification } from '../notifications/store.svelte'
  import CommandPalette from './CommandPalette.svelte'
  import FormatToolbar from './editor/FormatToolbar.svelte'
  import FormattingFirstRunTip from './editor/FormattingFirstRunTip.svelte'
  import SelectionBubble from './editor/SelectionBubble.svelte'
  import BlockHoverMenu from './editor/BlockHoverMenu.svelte'
  import ViewModeToggle from './editor/ViewModeToggle.svelte'
  import MarkdownSourceViewer from './editor/MarkdownSourceViewer.svelte'
  import { getViewMode, toggleViewMode } from '../lib/viewMode.svelte'
  import { getSlashCommands } from '../lib/editor/slash-registry'
  import { dispatch as dispatchPluginEvent } from '../plugins/events'

  // Map of slash command ids to their mark type (#168). 'clear' is special
  // (strips all marks); 'link' opens a URL prompt.
  const FORMAT_COMMANDS: Record<string, string> = {
    bold: 'bold',
    italic: 'italic',
    underline: 'underline',
    strike: 'strike',
    code: 'code',
    highlight: 'highlight',
    subscript: 'subscript',
    superscript: 'superscript',
    link: 'link',
    'clear-formatting': 'clear'
  }

  // Validates hex color strings before applying to marks (#170). Prevents
  // injection of arbitrary CSS or characters that break the converter regex.
  const HEX_COLOR_RE = /^#[0-9a-fA-F]{3,8}$/

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

  let editorInstance = $state<Editor | null>(null)
  let saveTimeout: ReturnType<typeof setTimeout> | null = null
  let heartbeatInterval: ReturnType<typeof setInterval> | null = null
  let hasFocusLock = false
  let isFocused = $state(false)
  let suppressUpdate = false
  let showSlashMenu = $state(false)
  let showTemplatePicker = $state(false)

  // Active inline marks in the current selection (#168). Updated on every
  // selection change so the FormatToolbar buttons reflect aria-pressed state.
  const ALL_MARKS = ['bold', 'italic', 'underline', 'strike', 'code', 'highlight', 'subscript', 'superscript', 'link', 'textColor', 'backgroundColor']
  let activeMarks = $state<Set<string>>(new Set())

  // Selection bubble state (#168): tracks whether the selection is non-
  // collapsed and the screen coords for positioning the floating bubble.
  let selectionEmpty = $state(true)
  let selectionCoords = $state<{ left: number; top: number; bottom: number } | null>(null)

  // show_format_toolbar config (default true; *bool on Go side, nil = true).
  let showFormatToolbar = $derived(
    (settings.config?.ui as unknown as Record<string, unknown> | undefined)?.show_format_toolbar !== false
  )

  let isDark = $derived(
    themeState.mode === 'dark' ||
    (themeState.mode === 'system' &&
      window.matchMedia?.('(prefers-color-scheme: dark)').matches)
  )

  let colorEnabled = $derived(
    ((settings.config?.ui as unknown as Record<string, unknown> | undefined)?.formatting as Record<string, unknown> | undefined)?.color_enabled !== false
  )

  // show_word_count config (default false; opt-in, Phase 3).
  let showWordCount = $derived(
    (settings.config?.editor as unknown as Record<string, unknown> | undefined)?.show_word_count === true
  )

  // focus_mode config (default false; Phase 3). When true, CSS dims non-active
  // paragraphs for distraction-free writing.
  let focusModeEnabled = $derived(
    (settings.config?.editor as unknown as Record<string, unknown> | undefined)?.focus_mode === true
  )

  // Word count (updated on every editor transaction via CharacterCount storage).
  let wordCount = $state(0)

  // Inline link URL input (#168). Shows a small <input> near the selection
  // when the user clicks the link button or presses Ctrl+K. Enter applies,
  // Esc cancels, blur applies.
  let showLinkInput = $state(false)
  let linkInputValue = $state('')
  let linkInputCoords = $state<{ left: number; top: number } | null>(null)

  // View mode (#171): edit (TipTap WYSIWYG) vs source (raw markdown).
  // Synced via $effect so it reacts to both prop changes and store toggles.
  let viewMode = $state<'edit' | 'source'>('edit')
  $effect(() => {
    viewMode = getViewMode(notebook, section, page)
  })

  // First-run tip: dismissed when 'formatting_tip_v1' is in dismissed_tips.
  let formatTipDismissed = $derived(
    ((settings.config?.ui as unknown as Record<string, unknown> | undefined)?.dismissed_tips as string[] | undefined)?.includes('formatting_tip_v1') ?? false
  )

  function dismissFormatTip(): void {
    const cfg = settings.config
    if (!cfg) return
    const ui = cfg.ui as unknown as Record<string, unknown>
    const tips = (ui.dismissed_tips as string[] | undefined) ?? []
    if (!tips.includes('formatting_tip_v1')) {
      ui.dismissed_tips = [...tips, 'formatting_tip_v1']
      void saveConfig(cfg)
    }
  }

  // --- Inline link URL input (#168) ----------------------------------------
  function openLinkInput(): void {
    if (!editorInstance || editorInstance.isDestroyed) return
    const { selection } = editorInstance.state
    if (selection.empty) return
    // If already linked, remove instead of prompting.
    if (editorInstance.isActive('link')) {
      editorInstance.chain().focus().unsetLink().run()
      return
    }
    try {
      const coords = editorInstance.view.coordsAtPos(selection.from)
      linkInputCoords = { left: coords.left, top: coords.bottom }
    } catch {
      linkInputCoords = null
    }
    linkInputValue = ''
    showLinkInput = true
  }

  function applyLinkInput(): void {
    if (!editorInstance || editorInstance.isDestroyed) return
    const url = linkInputValue.trim()
    if (url) {
      editorInstance.chain().focus().toggleLink({ href: url }).run()
    } else {
      editorInstance.chain().focus().run()
    }
    showLinkInput = false
    linkInputValue = ''
  }

  function cancelLinkInput(): void {
    showLinkInput = false
    linkInputValue = ''
    editorInstance?.chain().focus().run()
  }

  // Auto-focus the link input when it appears.
  $effect(() => {
    if (showLinkInput) {
      requestAnimationFrame(() => {
        const input = document.querySelector<HTMLInputElement>('.link-input')
        input?.focus()
      })
    }
  })

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
  const initialKey = untrack(
    () => `${blocks.map((b) => b.id).join(',')}:${blocks.length}`
  )
  let lastSyncedBlocksKey = $state(initialKey)

  // Read config-driven extension toggles at editor creation time (#168 Phase 3).
  // These take effect on the next page load; toggling in Settings does not
  // hot-swap extensions mid-session (acceptable for v1).
  const typographyEnabled = untrack(
    () => {
      const formatting = (settings.config?.ui as unknown as Record<string, unknown> | undefined)?.formatting as Record<string, unknown> | undefined
      return formatting?.typography_enabled !== false
    }
  )

  const editorExtensions = [
    StarterKit.configure({
      paragraph: false,
      heading: false,
      bulletList: false,
      orderedList: false,
      listItem: false,
      blockquote: false,
      codeBlock: false,
      horizontalRule: false,
      trailingNode: false,
      link: { openOnClick: false, autolink: true }
    }),
    ...SiltBlockExtensionsWithNodeViews,
      ...SiltInlineMarkExtensions,
      ...SiltColorMarkExtensions,
      UniqueBlockIds,
    TaskMetaSuggest.configure({
      onChange: onMetaChange,
      onNavigate: onMetaNavigate,
      onSelectActive: onMetaSelectActive
    }),
    SiltBlockKeymaps,
    Placeholder.configure({
      placeholder: 'Type / for commands, or start writing…'
    }),
    // Editor UX enhancements (#168 Phase 3):
    CharacterCount, // word/char count (always loaded; visibility is CSS-gated)
    Focus, // focus mode (always loaded; dimming is CSS-gated by .focus-mode)
    ...(typographyEnabled ? [Typography] : []) // smart input replacements
  ]

  const editorStore = createEditor({
    extensions: editorExtensions,
    content: initialDoc,
    onUpdate: () => {
      if (suppressUpdate) return
      detectSlashCommand()
      unsavedChanges = true
      // Update word count from CharacterCount storage (#168 Phase 3).
      const storage = editorInstance?.storage as unknown as Record<string, unknown> | undefined
      const cc = storage?.characterCount as { words?: () => number } | undefined
      if (cc?.words) wordCount = cc.words()
      triggerAutoSave()
    },
    onSelectionUpdate: ({ editor }) => {
      // Track active marks for the FormatToolbar's aria-pressed state (#168).
      const marks = new Set<string>()
      for (const m of ALL_MARKS) {
        if (editor.isActive(m)) marks.add(m)
      }
      activeMarks = marks
      // Track selection state for the SelectionBubble (#168).
      const { selection } = editor.state
      selectionEmpty = selection.empty
      if (!selection.empty && !editor.isDestroyed) {
        try {
          const start = editor.view.coordsAtPos(selection.from)
          const end = editor.view.coordsAtPos(selection.to)
          selectionCoords = {
            left: (start.left + end.left) / 2,
            top: Math.min(start.top, end.top),
            bottom: Math.max(start.bottom, end.bottom)
          }
        } catch {
          selectionCoords = null
        }
      } else {
        selectionCoords = null
      }
      // Emit selection:changed on the plugin event bus (#106/#110).
      const selFrom = selection.$from
      // Attempt to read the block id at the selection anchor.
      let blockId: string | undefined
      try {
        const parentAttrs = selFrom.parent.attrs
        if (parentAttrs && parentAttrs.id) blockId = parentAttrs.id
      } catch {
        /* not in a block node */
      }
      // Emit selection:changed on the plugin event bus (#106/#110).
      dispatchPluginEvent('selection:changed', {
        notebook,
        section,
        page,
        blockId
      })
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

  function handleToggleViewMode(): void {
    toggleViewMode(notebook, section, page)
    viewMode = getViewMode(notebook, section, page)
  }

  // Global event listeners for cross-component hotkeys.
  function onOpenLinkInput(): void {
    openLinkInput()
  }
  function onToggleViewModeEvent(): void {
    handleToggleViewMode()
  }
  function onChangeBlockType(e: Event): void {
    const detail = (e as CustomEvent).detail
    if (!editorInstance) return
    if (detail?.type === 'headerBlock') {
      convertToBlock(editorInstance as any, 'headerBlock', detail.depth || 1)
    } else if (detail?.type === 'noteBlock') {
      convertToBlock(editorInstance as any, 'noteBlock')
    } else if (detail?.type === 'taskBlock') {
      convertToBlock(editorInstance as any, 'taskBlock')
    }
  }
  function onSetBlockAlign(e: Event): void {
    const align = (e as CustomEvent).detail as string
    if (align) setBlockAlignAttr(align)
  }
  function onEditorScroll(): void {
    selectionCoords = null
  }

  window.addEventListener('silt:open-link-input', onOpenLinkInput)
  window.addEventListener('toggle-view-mode', onToggleViewModeEvent)
  window.addEventListener('silt:change-block-type', onChangeBlockType)
  window.addEventListener('silt:set-block-align', onSetBlockAlign)
  window.addEventListener('scroll', onEditorScroll, true)

  onDestroy(() => {
    stopHeartbeat()
    void flushPendingSave().then(() => releaseFocus())
    window.removeEventListener('silt:open-link-input', onOpenLinkInput)
    window.removeEventListener('toggle-view-mode', onToggleViewModeEvent)
    window.removeEventListener('silt:change-block-type', onChangeBlockType)
    window.removeEventListener('silt:set-block-align', onSetBlockAlign)
    window.removeEventListener('scroll', onEditorScroll, true)
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

  let unsavedChanges = $state(false)
  let lastSaveError: string | null = $state(null)

  async function doSave(): Promise<void> {
    if (!editorInstance || editorInstance.isDestroyed) return
    const updatedBlocks = measureFrameBudget('tiptap-transaction', () =>
      docToBlocks(editorInstance.getJSON())
    )
    try {
      await SaveFileBlocks(notebook, section, page, updatedBlocks)
      lastSaveError = null
      unsavedChanges = false
      // Emit editor:save on the plugin event bus (#110).
      dispatchPluginEvent('editor:save', { notebook, section, page })
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e)
      console.error('TipTapEditor: SaveFileBlocks failed:', e)
      lastSaveError = msg
      pushNotification({
        kind: 'error',
        message: `Save failed: ${msg}`,
        action: { label: 'Retry', run: () => doSave() }
      })
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

  // Set block alignment attr (#173). No-op for TASK blocks.
  function setBlockAlignAttr(align: string): void {
    if (!editorInstance || editorInstance.isDestroyed) return
    const pos = editorInstance.state.selection.$from
    for (let d = pos.depth; d >= 1; d--) {
      const node = pos.node(d)
      if (node.type.name === 'taskBlock') return
      if (['noteBlock', 'headerBlock'].includes(node.type.name)) {
        const nodePos = pos.before(d)
        const tr = editorInstance.state.tr.setNodeAttribute(
          nodePos,
          'align',
          align
        )
        editorInstance.view.dispatch(tr)
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
      convertToBlock(editorInstance as any, 'taskBlock')
    } else if (commandId === 'h1') {
      convertToBlock(editorInstance as any, 'headerBlock', 1)
    } else if (commandId === 'h2') {
      convertToBlock(editorInstance as any, 'headerBlock', 2)
    } else if (commandId === 'h3') {
      convertToBlock(editorInstance as any, 'headerBlock', 3)
    } else if (commandId === 'note') {
      convertToBlock(editorInstance as any, 'noteBlock')
    } else if (commandId === 'task') {
      convertToBlock(editorInstance as any, 'taskBlock')
    } else if (commandId === 'align-left') {
      setBlockAlignAttr('left')
    } else if (commandId === 'align-center') {
      setBlockAlignAttr('center')
    } else if (commandId === 'align-right') {
      setBlockAlignAttr('right')
    } else if (commandId === 'align-justify') {
      setBlockAlignAttr('justify')
    } else if (commandId === 'text-color') {
      const input = window.prompt('Enter color (hex, e.g. #ff0000):')
      const color = input?.trim()
      if (color && HEX_COLOR_RE.test(color) && editorInstance) {
        editorInstance.chain().focus().setMark('textColor', { color }).run()
      }
    } else if (commandId === 'background-color') {
      const input = window.prompt('Enter background color (hex, e.g. #ffff00):')
      const color = input?.trim()
      if (color && HEX_COLOR_RE.test(color) && editorInstance) {
        editorInstance.chain().focus().setMark('backgroundColor', { color }).run()
      }
    } else if (commandId === 'remove-color') {
      if (editorInstance) editorInstance.chain().focus().unsetMark('textColor').run()
    } else if (commandId === 'remove-background') {
      if (editorInstance) editorInstance.chain().focus().unsetMark('backgroundColor').run()
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
    } else if (FORMAT_COMMANDS[commandId]) {
      // Inline formatting slash commands (#168). Each toggles its mark.
      const mark = FORMAT_COMMANDS[commandId]
      if (mark === 'link') {
        openLinkInput()
      } else if (mark === 'clear') {
        editorInstance.chain().focus().unsetAllMarks().run()
      } else {
        editorInstance.chain().focus().toggleMark(mark).run()
      }
    } else {
      // v2 SDK plugin-registered slash command (#110): look up the command in
      // the registry and invoke its onSelect handler with the live editor +
      // cursor position. Built-ins are handled by the id branches above; any
      // other id must be a plugin command with a handler.
      const cmd = getSlashCommands().find((c) => c.id === commandId)
      if (cmd?.onSelect) {
        cmd.onSelect(editorInstance, editorInstance.state.selection.to)
      }
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

<div class="tiptap-editor-host" class:focused={isFocused} class:focus-mode={focusModeEnabled}>
  <div class="view-mode-bar">
    <ViewModeToggle mode={viewMode} onToggle={handleToggleViewMode} />
  </div>
  {#if viewMode === 'source'}
    <MarkdownSourceViewer {blocks} filePath="{notebook}/{section}/{page}.md" />
  {:else}
    {#if showFormatToolbar}
      <FormatToolbar editor={editorInstance} {activeMarks} {isDark} {colorEnabled} />
    {/if}
    <BlockHoverMenu editor={editorInstance} {colorEnabled} {isDark} />
    <FormattingFirstRunTip dismissed={formatTipDismissed} onDismiss={dismissFormatTip} />
    <SelectionBubble
      editor={editorInstance}
      {activeMarks}
      {selectionEmpty}
      {selectionCoords}
    />
    {#if editorStore}
      <EditorContent editor={$editorStore} />
    {/if}
    {#if unsavedChanges || lastSaveError}
      <div
        class="unsaved-indicator {lastSaveError ? 'error' : ''}"
        role={lastSaveError ? 'alert' : 'status'}
        aria-live={lastSaveError ? 'assertive' : 'polite'}
      >
        {#if lastSaveError}
          <span class="material-symbols-outlined text-[14px]" aria-hidden="true"
            >error</span
          >
          <span>Save failed — edits not persisted</span>
        {:else}
          <span class="material-symbols-outlined text-[14px]" aria-hidden="true"
            >schedule</span
          >
          <span>Unsaved changes</span>
        {/if}
      </div>
    {/if}
    {#if showWordCount && wordCount > 0}
      <div class="word-count" role="status" aria-live="off">
        {wordCount} {wordCount === 1 ? 'word' : 'words'}
      </div>
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
    {#if showLinkInput && linkInputCoords}
      <div
        class="link-input-popover"
        style="left:{linkInputCoords.left}px; top:{linkInputCoords.top}px"
        role="dialog"
        aria-label="Insert link URL"
      >
        <input
          type="url"
          class="link-input"
          placeholder="https://"
          bind:value={linkInputValue}
          onkeydown={(e) => {
            if (e.key === 'Enter') { e.preventDefault(); applyLinkInput() }
            else if (e.key === 'Escape') { e.preventDefault(); cancelLinkInput() }
          }}
          onblur={applyLinkInput}
        />
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

  .view-mode-bar {
    display: flex;
    justify-content: flex-end;
    padding: 4px 8px;
  }

  .unsaved-indicator {
    position: sticky;
    top: 0;
    z-index: 5;
    display: inline-flex;
    align-items: center;
    gap: 0.25rem;
    margin: 0 0 0.5rem;
    padding: 0.125rem 0.5rem;
    border-radius: 9999px;
    border: 1px solid var(--color-border-muted, #3a3f4b);
    background: color-mix(
      in srgb,
      var(--color-surface, #1a1d24) 90%,
      transparent
    );
    color: var(--color-text-muted, #8b95a3);
    font-size: 11px;
    backdrop-filter: blur(4px);
  }
  .unsaved-indicator.error {
    border-color: color-mix(
      in srgb,
      var(--color-status-danger, #e5484d) 60%,
      transparent
    );
    color: var(--color-status-danger, #e5484d);
  }

  /* The ProseMirror editable surface. Global styles (typography vars, guide
     rails, indentation, node rendering) live in index.css under .ProseMirror
     and [data-type] selectors so they apply to all editor instances. */
  .tiptap-editor-host :global(.ProseMirror) {
    min-height: 22px;
    outline: none;
  }

  /* Focus mode (#168 Phase 3): dim all top-level blocks except the one with
     cursor focus. The Focus extension adds .has-focus to the active block. */
  .focus-mode :global(.ProseMirror > div:not(.has-focus)) {
    opacity: 0.3;
    transition: opacity 0.2s;
  }
  @media (prefers-reduced-motion: reduce) {
    .focus-mode :global(.ProseMirror > div:not(.has-focus)) {
      transition: none;
    }
  }

  .word-count {
    position: sticky;
    bottom: 0;
    margin: 0.25rem 0 0 auto;
    display: inline-block;
    padding: 0.125rem 0.5rem;
    border-radius: 9999px;
    background: color-mix(in srgb, var(--color-surface, #1a1d24) 90%, transparent);
    color: var(--color-text-muted, #8b95a3);
    font-size: 11px;
  }

  .link-input-popover {
    position: fixed;
    z-index: 100;
    margin-top: 4px;
    padding: 4px;
    border-radius: 8px;
    background: var(--color-surface, #1e1e22);
    border: 1px solid var(--color-border-muted, #33333a);
    box-shadow: 0 4px 16px rgba(0, 0, 0, 0.4);
  }

  .link-input {
    width: 240px;
    padding: 4px 8px;
    border: 1px solid var(--color-border-muted, #3a3f4b);
    border-radius: 6px;
    background: var(--color-surface, #1a1d24);
    color: var(--color-text-primary, #e6e6e6);
    font-size: 0.8rem;
    outline: none;
  }

  .link-input:focus {
    border-color: var(--color-accent-primary-glow, #6fa3ff);
  }

  .meta-suggest {
    position: fixed;
    z-index: 50;
    min-width: 240px;
    margin-top: 4px;
    padding: 4px;
    border-radius: 8px;
    background: var(--color-surface, #1e1e22);
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
    color: var(--color-text-primary, #e6e6e6);
    text-align: left;
    cursor: pointer;
    font-family: inherit;
  }

  .meta-suggest-item.selected {
    background: var(--color-accent-primary-start, #4f7cff);
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
