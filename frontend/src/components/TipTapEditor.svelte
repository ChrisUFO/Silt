<script lang="ts">
  import { onDestroy, untrack } from 'svelte'
  import { createEditor, EditorContent } from 'svelte-tiptap'
  import type { Editor } from 'svelte-tiptap'
  import StarterKit from '@tiptap/starter-kit'
  import Placeholder from '@tiptap/extension-placeholder'
  import { CharacterCount, Focus } from '@tiptap/extensions'
  import Typography from '@tiptap/extension-typography'
  import { AutosaveManager } from '../lib/editor/useAutosave'
  import { FocusLockManager } from '../lib/editor/useFocusLock'
  import {
    SiltBlockExtensionsWithNodeViews,
    SiltInlineMarkExtensions,
    SiltColorMarkExtensions,
    SiltDetailsExtensions,
    SiltTableExtensions,
    UniqueBlockIds,
    SiltBlockKeymaps,
    convertToBlock,
    setBlockAlign,
    toggleBlockQuote,
    insertCallout,
    insertCodeBlock,
    insertDetails,
    insertTable,
    findActiveBlock,
    TaskMetaSuggest,
    applyMetaSuggestion,
    filterMetaKeys,
    blocksToDoc
  } from '../lib/editor'
  import type { ParsedBlock, MetaKey, SuggestContext } from '../lib/editor'
  import TemplatePicker from '../templates/TemplatePicker.svelte'
  import { settings, appendDismissedTip } from '../settings/store.svelte'
  import { pushNotification } from '../notifications/store.svelte'
  import CommandPalette from './CommandPalette.svelte'
  import FormattingFirstRunTip from './editor/FormattingFirstRunTip.svelte'
  import SelectionBubble from './editor/SelectionBubble.svelte'
  import MarkdownSourceViewer from './editor/MarkdownSourceViewer.svelte'
  import TableContextToolbar from './editor/TableContextToolbar.svelte'
  import { DEFAULT_COLOR_PALETTE, resolveColor } from '../lib/editor/colors'
  import { getSlashCommands } from '../lib/editor/slash-registry'
  import { clampToViewport } from '../lib/editor/popoverPositioning'
  import {
    cutSelection,
    copySelection,
    pasteFromClipboard,
    copyAsMarkdown,
    copyAsPlainText,
    copyBlockReference,
    copyBlockEmbed,
    duplicateBlock,
    deleteBlock
  } from '../lib/editor/clipboard'
  import { dispatch as dispatchPluginEvent } from '../plugins/events'
  import type { Node as ProseMirrorNode } from '@tiptap/pm/model'
  import { isSystemDark } from '../lib/systemTheme.svelte'

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
    editorInstance?: Editor | null
    activeMarks?: Set<string>
    viewMode?: 'edit' | 'source'
    /** Emitted when the editor's save state changes (dirty/error → clean).
     *  Used by the tab strip to show per-tab dirty/save-failed indicators
     *  (#167). */
    onSaveStateChange?: (state: {
      dirty: boolean
      error: string | null
    }) => void
  }

  let {
    notebook,
    section,
    page,
    blocks,
    activeFocusedBlockAncestors = [],
    onBlockFocus,
    onBlockBlur,
    onUpdate,
    editorInstance = $bindable(null),
    activeMarks = $bindable(new Set()),
    viewMode = 'edit',
    onSaveStateChange
  }: Props = $props()
  let editorReady = $state(false)
  let isFocused = $state(false)
  let suppressUpdate = false
  let showSlashMenu = $state(false)
  let slashQuery = $state('')
  let slashMenuDismissed = $state(false)
  let showTemplatePicker = $state(false)

  // Active inline marks in the current selection (#168). Updated on every
  // selection change so the FormatToolbar buttons reflect aria-pressed state.
  const ALL_MARKS = [
    'bold',
    'italic',
    'underline',
    'strike',
    'code',
    'highlight',
    'subscript',
    'superscript',
    'link',
    'textColor',
    'backgroundColor'
  ]

  // Selection bubble state (#168): tracks whether the selection is non-
  // collapsed and the screen coords for positioning the floating bubble.
  let selectionEmpty = $state(true)
  let isLastBlock = $state(false)
  let cursorInTable = $state(false)
  let selectionCoords = $state<{
    left: number
    top: number
    bottom: number
  } | null>(null)

  // Track OS dark/light preference reactively so isDark updates when the
  // OS theme changes under mode === 'system' (#168 color palette).
  let isDark = $derived(isSystemDark())

  let colorEnabled = $derived(
    settings.config?.ui?.formatting?.color_enabled !== false
  )

  // show_word_count config (default false; opt-in, Phase 3).
  let showWordCount = $derived(
    settings.config?.editor?.show_word_count === true
  )

  // focus_mode config (default false; Phase 3). When true, CSS dims non-active
  // paragraphs for distraction-free writing.
  let focusModeEnabled = $derived(settings.config?.editor?.focus_mode === true)

  // Word count (updated on every editor transaction via CharacterCount storage).
  let wordCount = $state(0)

  // Inline link URL input (#168). Shows a small <input> near the selection
  // when the user clicks the link button or presses Ctrl+K. Enter applies,
  // Esc cancels, blur applies.
  let showLinkInput = $state(false)
  let linkInputValue = $state('')
  let linkInputCoords = $state<{ left: number; top: number } | null>(null)

  // Color picker popover (#170). Shows ColorPickerMenu as a floating element
  // near the selection, replacing the window.prompt slash-command path.
  let showColorPicker = $state(false)
  let colorPickerMarkType = $state<'textColor' | 'backgroundColor'>('textColor')
  let colorPickerCoords = $state<{ left: number; top: number } | null>(null)

  // View mode (#171) is managed by the parent container.

  // First-run tip: dismissed when 'formatting_tip_v1' is in dismissed_tips.
  let formatTipDismissed = $derived(
    settings.config?.ui?.dismissed_tips?.includes('formatting_tip_v1') ?? false
  )

  async function dismissFormatTip(): Promise<void> {
    if (formatTipDismissed) return
    // Snapshot the previous dismissed_tips so we can roll back the optimistic
    // mirror if the IPC call fails — otherwise the UI hides the tip but the
    // on-disk config never recorded the dismissal, so the tip reappears on
    // next launch with no indication that anything went wrong.
    const previous = settings.config?.ui?.dismissed_tips
      ? [...settings.config.ui.dismissed_tips]
      : []
    const ok = await appendDismissedTip('formatting_tip_v1')
    if (!ok) {
      const cfg = settings.config
      if (cfg?.ui) cfg.ui.dismissed_tips = previous
      pushNotification({
        kind: 'error',
        message: 'Could not save the dismiss preference — please try again.'
      })
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

  // --- Color picker popover (#170) -----------------------------------------
  function openColorPickerPopover(
    markType: 'textColor' | 'backgroundColor'
  ): void {
    if (!editorInstance || editorInstance.isDestroyed) return
    try {
      const { selection } = editorInstance.state
      const coords = editorInstance.view.coordsAtPos(selection.from)
      colorPickerCoords = { left: coords.left, top: coords.bottom }
    } catch {
      colorPickerCoords = null
    }
    colorPickerMarkType = markType
    showColorPicker = true
  }

  function onOpenColorPicker(e: Event): void {
    const markType = (e as CustomEvent).detail as
      | 'textColor'
      | 'backgroundColor'
    if (markType) openColorPickerPopover(markType)
  }

  function applyColorFromPopover(color: string | null): void {
    if (!editorInstance || editorInstance.isDestroyed) return
    if (color && HEX_COLOR_RE.test(color)) {
      editorInstance
        .chain()
        .focus()
        .setMark(colorPickerMarkType, { color })
        .run()
    } else if (!color) {
      editorInstance.chain().focus().unsetMark(colorPickerMarkType).run()
    }
    showColorPicker = false
    editorInstance.chain().focus().run()
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
    () => settings.config?.ui?.formatting?.typography_enabled !== false
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
    ...SiltDetailsExtensions,
    ...SiltTableExtensions,
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
      isLastBlock = editorInstance
        ? editorInstance.state.doc.childCount <= 1
        : false
      // Update word count from CharacterCount storage (#168 Phase 3).
      const storage = editorInstance?.storage as unknown as
        | Record<string, unknown>
        | undefined
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
      // Contextual table toolbar (#172): shown when the cursor is inside a
      // table cell (the selection resolves to a tableCell/tableHeader node).
      cursorInTable =
        editor.isActive('tableCell') || editor.isActive('tableHeader')
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
      editorReady = true
      isLastBlock = editor.state.doc.childCount <= 1
    }
  })

  // Global event listeners for cross-component hotkeys.
  function onOpenLinkInput(): void {
    openLinkInput()
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
    if (align) setBlockAlign(editorInstance as any, align)
  }
  function onEditorScroll(): void {
    selectionCoords = null
  }
  // Dismiss SelectionBubble when clicking outside the editor and bubble (#168).
  function onDocumentClick(e: MouseEvent): void {
    const target = e.target as HTMLElement | null
    if (!target) return
    if (
      target.closest('.ProseMirror') ||
      target.closest('.selection-bubble') ||
      target.closest('.glass-palette')
    )
      return
    selectionCoords = null
    showSlashMenu = false
    slashMenuDismissed = true
  }

  window.addEventListener('silt:open-link-input', onOpenLinkInput)
  window.addEventListener('silt:change-block-type', onChangeBlockType)
  window.addEventListener('silt:set-block-align', onSetBlockAlign)
  window.addEventListener('silt:open-color-picker', onOpenColorPicker)
  window.addEventListener('scroll', onEditorScroll, true)
  document.addEventListener('click', onDocumentClick)

  onDestroy(() => {
    stopHeartbeat()
    void flushPendingSave().then(() => releaseFocus())
    window.removeEventListener('silt:open-link-input', onOpenLinkInput)
    window.removeEventListener('silt:change-block-type', onChangeBlockType)
    window.removeEventListener('silt:set-block-align', onSetBlockAlign)
    window.removeEventListener('silt:open-color-picker', onOpenColorPicker)
    window.removeEventListener('scroll', onEditorScroll, true)
    document.removeEventListener('click', onDocumentClick)
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
    // Reset save state — new content loaded, nothing is dirty (#167).
    autosave.markClean()
  })

  // --- Auto-save (debounced, config-driven, same contract as legacy) --------

  let unsavedChanges = $state(false)
  let lastSaveError: string | null = $state(null)

  const autosave = new AutosaveManager({
    getEditor: () => editorInstance,
    getNotebook: () => notebook,
    getSection: () => section,
    getPage: () => page,
    getDelay: () =>
      Math.max(settings.config?.editor?.auto_save_delay_ms ?? 500, 50),
    onUpdate: (blocks) => onUpdate(blocks),
    onStateChange: (dirty, error) => {
      unsavedChanges = dirty
      lastSaveError = error
    },
    onSaveStateChange: (state) => onSaveStateChange?.(state)
  })

  function triggerAutoSave(): void {
    autosave.trigger()
  }
  function flushPendingSave(): Promise<void> {
    return autosave.flush()
  }

  // --- Slash menu -----------------------------------------------------------

  function detectSlashCommand(): void {
    if (!editorInstance || editorInstance.isDestroyed) return
    const sel = editorInstance.state.selection
    const textBefore = sel.$from.parent.textContent.slice(
      0,
      sel.$from.parentOffset
    )
    if (textBefore.startsWith('/')) {
      if (!slashMenuDismissed) {
        showSlashMenu = true
        slashQuery = textBefore.slice(1)
      }
    } else {
      showSlashMenu = false
      slashQuery = ''
      slashMenuDismissed = false
    }
  }

  function slashCoords(): { left: number; top: number } | null {
    if (!showSlashMenu || !editorInstance || editorInstance.isDestroyed)
      return null
    const { selection } = editorInstance.state
    const pos = selection.$from.start()
    try {
      const c = editorInstance.view.coordsAtPos(pos)
      return clampToViewport(
        { x: c.left, y: c.bottom, width: 256, height: 300 },
        { width: window.innerWidth, height: window.innerHeight }
      )
    } catch (err) {
      return null
    }
  }

  function handleSlashSelect(commandId: string): void {
    showSlashMenu = false
    slashQuery = ''
    slashMenuDismissed = false
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
      setBlockAlign(editorInstance as any, 'left')
    } else if (commandId === 'align-center') {
      setBlockAlign(editorInstance as any, 'center')
    } else if (commandId === 'align-right') {
      setBlockAlign(editorInstance as any, 'right')
    } else if (commandId === 'align-justify') {
      setBlockAlign(editorInstance as any, 'justify')
    } else if (commandId === 'quote') {
      toggleBlockQuote(editorInstance as any)
    } else if (commandId === 'callout') {
      insertCallout(editorInstance as any, 'note')
    } else if (commandId.startsWith('callout-')) {
      insertCallout(editorInstance as any, commandId.slice('callout-'.length))
    } else if (commandId === 'code-block') {
      insertCodeBlock(editorInstance as any)
    } else if (commandId === 'details') {
      insertDetails(editorInstance as any)
    } else if (commandId === 'table') {
      insertTable(editorInstance as any, 3, 3)
    } else if (commandId === 'table-5x4') {
      insertTable(editorInstance as any, 5, 4)
    } else if (commandId === 'table-custom') {
      const input = window.prompt('Table dimensions (rows × columns), e.g. 4x3')
      const m = input?.match(/^(\d+)\s*[x×]\s*(\d+)$/i)
      if (m)
        insertTable(
          editorInstance as any,
          parseInt(m[1], 10),
          parseInt(m[2], 10)
        )
    } else if (commandId === 'text-color') {
      openColorPickerPopover('textColor')
    } else if (commandId === 'background-color') {
      openColorPickerPopover('backgroundColor')
    } else if (commandId === 'remove-color') {
      if (editorInstance)
        editorInstance.chain().focus().unsetMark('textColor').run()
    } else if (commandId === 'remove-background') {
      if (editorInstance)
        editorInstance.chain().focus().unsetMark('backgroundColor').run()
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

  const focusLock = new FocusLockManager({
    getNotebook: () => notebook,
    getSection: () => section,
    getPage: () => page,
    getEditor: () => editorInstance,
    onBlockFocus: (id, ancestors) => onBlockFocus?.(id, ancestors)
  })

  function acquireFocus(): void {
    void focusLock.acquire()
  }
  function releaseFocus(): void {
    void focusLock.release()
  }
  function startHeartbeat(): void {
    focusLock.startHeartbeat()
  }
  function stopHeartbeat(): void {
    focusLock.stopHeartbeat()
  }
  function notifyFocus(): void {
    focusLock.notifyFocus()
  }

  // Context Menu state
  let contextMenu = $state<{
    x: number
    y: number
    activeBlockId?: string
    activeBlockNode?: ProseMirrorNode
  } | null>(null)

  $effect(() => {
    if (contextMenu) {
      const handleKeyDown = (e: KeyboardEvent) => {
        if (e.key === 'Escape') {
          e.preventDefault()
          e.stopPropagation()
          contextMenu = null
          editorInstance?.commands.focus()
        }
      }
      window.addEventListener('keydown', handleKeyDown, true)
      return () => {
        window.removeEventListener('keydown', handleKeyDown, true)
      }
    }
  })

  // Context menu keyboard navigation (ArrowUp/Down, Home/End)
  let contextMenuEl = $state<HTMLDivElement | null>(null)

  $effect(() => {
    if (contextMenu && contextMenuEl) {
      const id = requestAnimationFrame(() => {
        const first = contextMenuEl?.querySelector<HTMLButtonElement>(
          'button:not([disabled])'
        )
        first?.focus()
      })
      return () => cancelAnimationFrame(id)
    }
  })

  function handleMenuKeyDown(e: KeyboardEvent): void {
    if (!contextMenuEl) return
    const items = Array.from(
      contextMenuEl.querySelectorAll<HTMLButtonElement>(
        'button:not([disabled])'
      )
    )
    if (items.length === 0) return
    const currentIndex = items.findIndex(
      (item) => item === document.activeElement
    )
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault()
        items[(currentIndex + 1) % items.length]?.focus()
        break
      case 'ArrowUp':
        e.preventDefault()
        items[(currentIndex - 1 + items.length) % items.length]?.focus()
        break
      case 'Home':
        e.preventDefault()
        items[0]?.focus()
        break
      case 'End':
        e.preventDefault()
        items[items.length - 1]?.focus()
        break
    }
  }

  function handleContextMenu(e: MouseEvent): void {
    if (!editorInstance || editorInstance.isDestroyed) return
    e.preventDefault()

    // Move editor cursor to the click location if the click is outside the current selection.
    const pos = editorInstance.view.posAtCoords({
      left: e.clientX,
      top: e.clientY
    })
    if (pos) {
      const { selection } = editorInstance.state
      if (pos.pos < selection.from || pos.pos > selection.to) {
        editorInstance.commands.setTextSelection(pos.pos)
      }
    }

    // Resolve the active block and its unique ID
    let activeBlockId: string | undefined
    let activeBlockNode: ProseMirrorNode | null = null
    const active = findActiveBlock(editorInstance)
    if (active) {
      activeBlockId = active.node.attrs.id
      activeBlockNode = active.node
    }

    // Viewport collision boundary adjustment to prevent offscreen rendering
    const { left: x, top: y } = clampToViewport(
      { x: e.clientX, y: e.clientY, width: 220, height: 320 },
      { width: window.innerWidth, height: window.innerHeight }
    )

    contextMenu = {
      x,
      y,
      activeBlockId,
      activeBlockNode: activeBlockNode ?? undefined
    }
  }

  // Menu action handlers — thin wrappers around the extracted clipboard
  // module. Each handler operates on `editorInstance` + the live `contextMenu`
  // state (passed via a getter so the module sees the current snapshot).
  function closeContextMenu(): void {
    contextMenu = null
    editorInstance?.commands.focus()
  }

  function clipboardDeps() {
    return {
      editor: editorInstance!,
      notify: pushNotification,
      menu: () => contextMenu
    }
  }

  function handleCut(): void {
    if (!editorInstance) return
    cutSelection(clipboardDeps())
    closeContextMenu()
  }

  function handleCopy(): void {
    if (!editorInstance) return
    copySelection(clipboardDeps())
    closeContextMenu()
  }

  async function handlePaste(): Promise<void> {
    if (!editorInstance) return
    await pasteFromClipboard(clipboardDeps())
    closeContextMenu()
  }

  async function handleCopyAsMarkdown(): Promise<void> {
    if (!editorInstance) return
    await copyAsMarkdown(clipboardDeps())
    closeContextMenu()
  }

  async function handleCopyAsPlainText(): Promise<void> {
    if (!editorInstance) return
    await copyAsPlainText(clipboardDeps())
    closeContextMenu()
  }

  async function handleCopyBlockReference(): Promise<void> {
    if (!editorInstance) return
    await copyBlockReference(clipboardDeps())
    closeContextMenu()
  }

  async function handleCopyBlockEmbed(): Promise<void> {
    if (!editorInstance) return
    await copyBlockEmbed(clipboardDeps())
    closeContextMenu()
  }

  function handleDuplicateBlock(): void {
    if (!editorInstance) return
    duplicateBlock(clipboardDeps())
    closeContextMenu()
  }

  function handleDeleteBlock(): void {
    if (!editorInstance) return
    deleteBlock(clipboardDeps())
    closeContextMenu()
  }

  function handleClearFormatting(): void {
    editorInstance?.chain().focus().unsetAllMarks().run()
    closeContextMenu()
  }
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<!-- Contextmenu listener is on the outer host wrapper to handle editor-wide custom right-click menus -->
<div
  class="tiptap-editor-host"
  class:focused={isFocused}
  class:focus-mode={focusModeEnabled}
  oncontextmenu={handleContextMenu}
>
  {#if viewMode === 'source'}
    <MarkdownSourceViewer {blocks} filePath="{notebook}/{section}/{page}.md" />
  {:else}
    {#if editorReady}
      <FormattingFirstRunTip
        dismissed={formatTipDismissed}
        onDismiss={dismissFormatTip}
      />
      <SelectionBubble
        editor={editorInstance}
        {activeMarks}
        {selectionEmpty}
        {selectionCoords}
      />
      {#if cursorInTable && editorInstance}
        <TableContextToolbar editor={editorInstance} />
      {/if}
      {#if editorStore}
        <EditorContent editor={$editorStore} />
      {/if}
    {/if}

    {#if contextMenu}
      <div class="fixed inset-0 z-[180]">
        <!-- svelte-ignore a11y_click_events_have_key_events -->
        <!-- svelte-ignore a11y_no_static_element_interactions -->
        <div
          class="absolute inset-0 cursor-default"
          onclick={() => (contextMenu = null)}
          oncontextmenu={(e) => {
            e.preventDefault()
            e.stopPropagation()
            contextMenu = null
          }}
        ></div>
        <div
          bind:this={contextMenuEl}
          class="fixed context-menu-card"
          style="left: {contextMenu.x}px; top: {contextMenu.y}px"
          role="menu"
          tabindex="-1"
          aria-label="Editor actions"
          oncontextmenu={(e) => e.preventDefault()}
          onkeydown={handleMenuKeyDown}
        >
          <button
            type="button"
            class="context-menu-item"
            role="menuitem"
            onclick={handleCut}
            disabled={selectionEmpty}
          >
            <span class="material-symbols-outlined text-[16px]"
              >content_cut</span
            >
            Cut
          </button>
          <button
            type="button"
            class="context-menu-item"
            role="menuitem"
            onclick={handleCopy}
            disabled={selectionEmpty}
          >
            <span class="material-symbols-outlined text-[16px]"
              >content_copy</span
            >
            Copy
          </button>
          <button
            type="button"
            class="context-menu-item"
            role="menuitem"
            onclick={handlePaste}
          >
            <span class="material-symbols-outlined text-[16px]"
              >content_paste</span
            >
            Paste
          </button>

          <div class="context-menu-separator"></div>

          <button
            type="button"
            class="context-menu-item"
            role="menuitem"
            onclick={handleCopyAsMarkdown}
          >
            <span class="material-symbols-outlined text-[16px]">markdown</span>
            Copy as Markdown
          </button>
          <button
            type="button"
            class="context-menu-item"
            role="menuitem"
            onclick={handleCopyAsPlainText}
          >
            <span class="material-symbols-outlined text-[16px]">notes</span>
            Copy as Plain Text
          </button>

          {#if contextMenu.activeBlockId}
            <div class="context-menu-separator"></div>
            <button
              type="button"
              class="context-menu-item"
              role="menuitem"
              onclick={handleCopyBlockReference}
            >
              <span class="material-symbols-outlined text-[16px]">link</span>
              Copy Block Reference
            </button>
            <button
              type="button"
              class="context-menu-item"
              role="menuitem"
              onclick={handleCopyBlockEmbed}
            >
              <span class="material-symbols-outlined text-[16px]"
                >integration_instructions</span
              >
              Copy Block Embed
            </button>

            <div class="context-menu-separator"></div>
            <button
              type="button"
              class="context-menu-item"
              role="menuitem"
              onclick={handleDuplicateBlock}
            >
              <span class="material-symbols-outlined text-[16px]"
                >difference</span
              >
              Duplicate Block
            </button>
            <button
              type="button"
              class="context-menu-item text-status-danger"
              role="menuitem"
              onclick={handleDeleteBlock}
              disabled={isLastBlock}
            >
              <span class="material-symbols-outlined text-[16px]">delete</span>
              Delete Block
            </button>
          {/if}

          <div class="context-menu-separator"></div>
          <button
            type="button"
            class="context-menu-item"
            role="menuitem"
            onclick={handleClearFormatting}
          >
            <span class="material-symbols-outlined text-[16px]"
              >format_clear</span
            >
            Clear Formatting
          </button>
        </div>
      </div>
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
        {wordCount}
        {wordCount === 1 ? 'word' : 'words'}
      </div>
    {/if}
    {#if showSlashMenu}
      {@const coords = slashCoords()}
      {#if coords}
        <CommandPalette
          style="position: fixed; left: {coords.left}px; top: {coords.top}px;"
          query={slashQuery}
          onSelect={handleSlashSelect}
          onClose={() => {
            showSlashMenu = false
            slashMenuDismissed = true
          }}
        />
      {/if}
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
            if (e.key === 'Enter') {
              e.preventDefault()
              applyLinkInput()
            } else if (e.key === 'Escape') {
              e.preventDefault()
              cancelLinkInput()
            }
          }}
          onblur={applyLinkInput}
        />
      </div>
    {/if}
    {#if showColorPicker && colorPickerCoords}
      <!-- svelte-ignore a11y_click_events_have_key_events -->
      <div
        class="color-picker-popover"
        style="left:{colorPickerCoords.left}px; top:{colorPickerCoords.top}px"
        role="menu"
        tabindex="-1"
        aria-label={colorPickerMarkType === 'textColor'
          ? 'Text color'
          : 'Background color'}
        onclick={(e) => e.stopPropagation()}
      >
        <button
          type="button"
          class="cp-swatch cp-reset"
          onclick={() => applyColorFromPopover(null)}
          aria-label="No color"
        >
          <span
            class="material-symbols-outlined"
            style="font-size:16px"
            aria-hidden="true">format_color_reset</span
          >
        </button>
        {#each DEFAULT_COLOR_PALETTE as entry (entry.id)}
          <button
            type="button"
            class="cp-swatch"
            style="background-color: {resolveColor(entry, isDark)}"
            aria-label={entry.label}
            role="menuitem"
            onclick={() => applyColorFromPopover(resolveColor(entry, isDark))}
          >
          </button>
        {/each}
        <label class="cp-custom-row">
          <input
            type="color"
            class="cp-custom-input"
            onchange={(e) =>
              applyColorFromPopover(
                (e.currentTarget as HTMLInputElement).value
              )}
            aria-label="Custom color"
          />
        </label>
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
    background: color-mix(
      in srgb,
      var(--color-surface, #1a1d24) 90%,
      transparent
    );
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

  .color-picker-popover {
    position: fixed;
    z-index: 100;
    margin-top: 4px;
    padding: 6px;
    border-radius: 8px;
    background: var(--color-surface, #1e1e22);
    border: 1px solid var(--color-border-muted, #33333a);
    box-shadow: 0 4px 16px rgba(0, 0, 0, 0.4);
    display: grid;
    grid-template-columns: repeat(6, 1fr);
    gap: 3px;
    max-width: 200px;
  }

  .cp-swatch {
    width: 24px;
    height: 24px;
    border: 2px solid transparent;
    border-radius: 5px;
    cursor: pointer;
    padding: 0;
    transition: border-color 0.1s;
  }

  .cp-swatch:hover {
    border-color: var(--color-text-primary, #e6e6e6);
  }

  .cp-reset {
    display: flex;
    align-items: center;
    justify-content: center;
    background: transparent;
    color: var(--color-text-muted, #8b95a3);
  }

  .cp-custom-row {
    grid-column: 1 / -1;
    display: flex;
    justify-content: center;
    margin-top: 2px;
  }

  .cp-custom-input {
    width: 28px;
    height: 22px;
    border: 1px solid var(--color-border-muted, #3a3f4b);
    border-radius: 4px;
    background: transparent;
    cursor: pointer;
    padding: 0;
  }

  @media (prefers-reduced-motion: reduce) {
    .cp-swatch {
      transition: none;
    }
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

  .context-menu-card {
    background-color: color-mix(in srgb, var(--color-panel) 90%, transparent);
    backdrop-filter: blur(12px) saturate(140%);
    border: 1px solid var(--color-border-muted, #33333a);
    border-radius: 8px;
    box-shadow: 0 10px 25px -5px rgba(0, 0, 0, 0.5);
    padding: 4px;
    min-width: 180px;
    z-index: 181;
  }

  .context-menu-item {
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    padding: 6px 12px;
    border: none;
    background: transparent;
    color: var(--color-text-primary, #e6e6e6);
    font-size: 12px;
    font-family: var(--font-body, inherit);
    text-align: left;
    cursor: pointer;
    border-radius: 6px;
    transition: background-color 120ms ease-out;
  }

  .context-menu-item:hover {
    background-color: var(--color-hover, #1e2128);
  }

  .context-menu-item:disabled {
    opacity: 0.4;
    cursor: not-allowed;
  }

  .context-menu-item.text-status-danger {
    color: var(--color-status-danger, #e5484d);
  }

  .context-menu-item.text-status-danger .material-symbols-outlined {
    color: var(--color-status-danger, #e5484d);
  }

  .context-menu-item:hover.text-status-danger {
    background-color: color-mix(
      in srgb,
      var(--color-status-danger, #e5484d) 15%,
      transparent
    );
  }

  .context-menu-separator {
    height: 1px;
    background: var(--color-border-muted, #33333a);
    margin: 4px;
  }
</style>
