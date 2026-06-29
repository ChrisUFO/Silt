<script lang="ts">
  import type { Editor } from 'svelte-tiptap'
  import HeadingLevelMenu from './HeadingLevelMenu.svelte'
  import ColorPickerMenu from './ColorPickerMenu.svelte'
  import {
    toggleBlockQuote,
    insertCallout,
    insertCodeBlock,
    insertDetails,
    insertTable
  } from '../../lib/editor'
  import { settings } from '../../settings/store.svelte'
  import { resolveHotkeyDisplay } from '../../settings/hotkeys'

  interface Props {
    editor: Editor | null
    activeMarks: Set<string>
    isDark: boolean
    colorEnabled: boolean
  }

  let { editor, activeMarks, isDark, colorEnabled }: Props = $props()

  // Each toolbar button carries a hotkey ACTION NAME (keyed into
  // config.hotkeys) — never a binding literal. The display binding is
  // resolved live so tooltips + aria-keyshortcuts track the user's actual
  // (possibly remapped or disabled) keymap. Returns '' when the action is
  // absent or disabled, in which case the attribute is omitted.
  let hotkeys = $derived(settings.config?.hotkeys ?? {})
  function hk(action: string): string {
    return resolveHotkeyDisplay(action, hotkeys)
  }

  interface FormatButton {
    id: string
    label: string
    icon: string
    /** Config hotkey action name (e.g. 'format_bold'). */
    hotkey: string
    mark: string
  }

  const BUTTONS: FormatButton[] = [
    {
      id: 'bold',
      label: 'Bold',
      icon: 'format_bold',
      hotkey: 'format_bold',
      mark: 'bold'
    },
    {
      id: 'italic',
      label: 'Italic',
      icon: 'format_italic',
      hotkey: 'format_italic',
      mark: 'italic'
    },
    {
      id: 'underline',
      label: 'Underline',
      icon: 'format_underlined',
      hotkey: 'format_underline',
      mark: 'underline'
    },
    {
      id: 'strike',
      label: 'Strikethrough',
      icon: 'format_strikethrough',
      hotkey: 'format_strike',
      mark: 'strike'
    },
    {
      id: 'code',
      label: 'Inline code',
      icon: 'code',
      hotkey: 'format_code',
      mark: 'code'
    },
    {
      id: 'highlight',
      label: 'Highlight',
      icon: 'highlight',
      hotkey: 'format_highlight',
      mark: 'highlight'
    },
    {
      id: 'subscript',
      label: 'Subscript',
      icon: 'subscript',
      hotkey: 'format_subscript',
      mark: 'subscript'
    },
    {
      id: 'superscript',
      label: 'Superscript',
      icon: 'superscript',
      hotkey: 'format_superscript',
      mark: 'superscript'
    }
  ]

  const ALIGN_BUTTONS = [
    {
      id: 'left',
      label: 'Align left',
      icon: 'format_align_left',
      hotkey: 'align_left'
    },
    {
      id: 'center',
      label: 'Align center',
      icon: 'format_align_center',
      hotkey: 'align_center'
    },
    {
      id: 'right',
      label: 'Align right',
      icon: 'format_align_right',
      hotkey: 'align_right'
    },
    {
      id: 'justify',
      label: 'Align justify',
      icon: 'format_align_justify',
      hotkey: 'align_justify'
    }
  ]

  // Block-insert entry points (#188/#180/#189/#183/#172). Each calls the same
  // helper the slash command dispatches, so the toolbar and slash menu stay in
  // sync. `run` is a thunk so the editor ref is read live at click time.
  interface InsertButton {
    id: string
    label: string
    icon: string
    run: () => void
  }
  const INSERT_BUTTONS: InsertButton[] = [
    {
      id: 'quote',
      label: 'Quote',
      icon: 'format_quote',
      run: () => editor && toggleBlockQuote(editor)
    },
    {
      id: 'code-block',
      label: 'Code block',
      icon: 'code_blocks',
      run: () => editor && insertCodeBlock(editor)
    },
    {
      id: 'callout',
      label: 'Callout',
      icon: 'info',
      run: () => editor && insertCallout(editor, 'note')
    },
    {
      id: 'details',
      label: 'Foldable section',
      icon: 'unfold_more',
      run: () => editor && insertDetails(editor)
    },
    {
      id: 'table',
      label: 'Table',
      icon: 'table_view',
      run: () => editor && insertTable(editor, 3, 3)
    }
  ]

  function handleClick(btn: FormatButton): void {
    if (!editor) return
    editor.chain().focus().toggleMark(btn.mark).run()
  }

  function handleLink(): void {
    if (!editor) return
    if (editor.isActive('link')) {
      editor.chain().focus().unsetLink().run()
    } else if (!editor.state.selection.empty) {
      window.dispatchEvent(new CustomEvent('silt:open-link-input'))
    }
  }

  function handleClear(): void {
    if (!editor) return
    editor.chain().focus().unsetAllMarks().run()
  }

  function handleAlign(align: string): void {
    if (!editor) return
    window.dispatchEvent(
      new CustomEvent('silt:set-block-align', { detail: align })
    )
  }

  function isActive(mark: string): boolean {
    return activeMarks.has(mark)
  }

  function currentAlign(): string {
    if (!editor) return 'left'
    const pos = editor.state.selection.$from
    for (let d = pos.depth; d >= 1; d--) {
      const node = pos.node(d)
      const attrs = node.attrs as Record<string, unknown>
      if (attrs.align) return attrs.align as string
    }
    return 'left'
  }

  let rovingIdx = $state(0)
  let toolbarEl: HTMLElement | null = $state(null)

  const LINK_IDX = BUTTONS.length
  const ALIGN_START = LINK_IDX + 1
  const INSERT_START = ALIGN_START + ALIGN_BUTTONS.length
  const COLOR_START = INSERT_START + INSERT_BUTTONS.length
  let clearIdx = $derived(COLOR_START + (colorEnabled ? 2 : 0))

  function handleKeydown(e: KeyboardEvent): void {
    const btns = toolbarEl?.querySelectorAll<HTMLButtonElement>('[data-tb]')
    if (!btns || btns.length === 0) return
    const count = btns.length
    let next = rovingIdx
    if (e.key === 'ArrowRight' || e.key === 'ArrowDown') {
      e.preventDefault()
      next = (rovingIdx + 1) % count
    } else if (e.key === 'ArrowLeft' || e.key === 'ArrowUp') {
      e.preventDefault()
      next = (rovingIdx - 1 + count) % count
    } else if (e.key === 'Home') {
      e.preventDefault()
      next = 0
    } else if (e.key === 'End') {
      e.preventDefault()
      next = count - 1
    } else if (e.key === 'Escape') {
      e.preventDefault()
      editor?.chain().focus().run()
      return
    } else {
      return
    }
    rovingIdx = next
    btns[next]?.focus()
  }
</script>

<div
  class="format-toolbar"
  role="toolbar"
  aria-label="Text formatting"
  tabindex="-1"
  bind:this={toolbarEl}
  onkeydown={handleKeydown}
>
  <HeadingLevelMenu {editor} />

  <span class="toolbar-divider" aria-hidden="true"></span>

  <div class="toolbar-group">
    {#each BUTTONS as btn, i (btn.id)}
      <button
        type="button"
        class="toolbar-btn"
        class:active={isActive(btn.mark)}
        aria-pressed={isActive(btn.mark)}
        aria-label={btn.label}
        aria-keyshortcuts={hk(btn.hotkey) || undefined}
        data-tb
        tabindex={rovingIdx === i ? 0 : -1}
        onclick={() => handleClick(btn)}
        onfocus={() => (rovingIdx = i)}
        title={btn.label}
      >
        <span class="material-symbols-outlined" aria-hidden="true"
          >{btn.icon}</span
        >
      </button>
    {/each}

    <button
      type="button"
      class="toolbar-btn"
      class:active={isActive('link')}
      aria-pressed={isActive('link')}
      aria-label="Insert link"
      aria-keyshortcuts={hk('format_link') || undefined}
      data-tb
      tabindex={rovingIdx === LINK_IDX ? 0 : -1}
      onclick={handleLink}
      onfocus={() => (rovingIdx = LINK_IDX)}
      title="Insert link"
    >
      <span class="material-symbols-outlined" aria-hidden="true">link</span>
    </button>
    <button
      type="button"
      class="toolbar-btn"
      aria-label="Check spelling"
      title="Check spelling (open suggestions for the misspelled word at the cursor)"
      data-tb
      onclick={() => {
        const rect = editor?.view.dom.getBoundingClientRect()
        const sel = editor?.view.coordsAtPos(editor.state.selection.head)
        window.dispatchEvent(
          new CustomEvent('silt:open-spellcheck', {
            detail: sel
              ? { x: sel.left, y: sel.bottom + 4 }
              : rect
                ? { x: rect.left + 40, y: rect.top + 40 }
                : { x: 100, y: 100 }
          })
        )
      }}
    >
      <span class="material-symbols-outlined" aria-hidden="true"
        >spellcheck</span
      >
    </button>
  </div>

  <span class="toolbar-divider" aria-hidden="true"></span>

  <div class="toolbar-group">
    {#each ALIGN_BUTTONS as btn, i (btn.id)}
      <button
        type="button"
        class="toolbar-btn"
        class:active={currentAlign() === btn.id}
        aria-pressed={currentAlign() === btn.id}
        aria-label={btn.label}
        aria-keyshortcuts={hk(btn.hotkey) || undefined}
        data-tb
        tabindex={rovingIdx === ALIGN_START + i ? 0 : -1}
        onclick={() => handleAlign(btn.id)}
        onfocus={() => (rovingIdx = ALIGN_START + i)}
        title={btn.label}
      >
        <span class="material-symbols-outlined" aria-hidden="true"
          >{btn.icon}</span
        >
      </button>
    {/each}
  </div>

  <span class="toolbar-divider" aria-hidden="true"></span>

  <div class="toolbar-group">
    {#each INSERT_BUTTONS as btn, i (btn.id)}
      <button
        type="button"
        class="toolbar-btn"
        aria-label={btn.label}
        data-tb
        tabindex={rovingIdx === INSERT_START + i ? 0 : -1}
        onclick={() => btn.run()}
        onfocus={() => (rovingIdx = INSERT_START + i)}
        title={btn.label}
      >
        <span class="material-symbols-outlined" aria-hidden="true"
          >{btn.icon}</span
        >
      </button>
    {/each}
  </div>

  {#if colorEnabled}
    <span class="toolbar-divider" aria-hidden="true"></span>
    <ColorPickerMenu {editor} markType="textColor" {isDark} />
    <ColorPickerMenu {editor} markType="backgroundColor" {isDark} />
  {/if}

  <span class="toolbar-divider" aria-hidden="true"></span>

  <div class="toolbar-group">
    <button
      type="button"
      class="toolbar-btn"
      aria-label="Clear formatting"
      data-tb
      tabindex={rovingIdx === clearIdx ? 0 : -1}
      onclick={handleClear}
      onfocus={() => (rovingIdx = clearIdx)}
      title="Clear formatting"
    >
      <span class="material-symbols-outlined" aria-hidden="true"
        >format_clear</span
      >
    </button>
  </div>
</div>

<style>
  .format-toolbar {
    display: flex;
    align-items: center;
    gap: 4px;
    height: 100%;
  }

  .toolbar-group {
    display: flex;
    align-items: center;
    gap: 2px;
  }

  .toolbar-divider {
    width: 1px;
    height: 20px;
    background: var(--color-border-muted, #2a2e36);
    margin: 0 4px;
    flex-shrink: 0;
  }

  .toolbar-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 28px;
    height: 28px;
    border: none;
    border-radius: 6px;
    background: transparent;
    color: var(--color-text-muted, #8b95a3);
    cursor: pointer;
    transition:
      background 0.1s,
      color 0.1s;
    flex-shrink: 0;
  }

  .toolbar-btn:hover {
    background: color-mix(
      in srgb,
      var(--color-accent-primary-start, #4f7cff) 15%,
      transparent
    );
    color: var(--color-text-primary, #e6e6e6);
  }

  .toolbar-btn:focus-visible {
    outline: 2px solid var(--color-accent-primary-start, #4f7cff);
    outline-offset: -2px;
  }

  .toolbar-btn.active {
    background: color-mix(
      in srgb,
      var(--color-accent-primary-glow, #6fa3ff) 20%,
      transparent
    );
    color: var(--color-accent-primary-glow, #6fa3ff);
  }

  .toolbar-btn .material-symbols-outlined {
    font-size: 18px;
    font-variation-settings: 'wght' 400;
  }

  @media (prefers-reduced-motion: reduce) {
    .toolbar-btn {
      transition: none;
    }
  }
</style>
