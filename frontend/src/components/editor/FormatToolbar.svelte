<script lang="ts">
  import type { Editor } from 'svelte-tiptap'
  import HeadingLevelMenu from './HeadingLevelMenu.svelte'
  import ColorPickerMenu from './ColorPickerMenu.svelte'

  interface Props {
    editor: Editor | null
    activeMarks: Set<string>
    isDark: boolean
    colorEnabled: boolean
  }

  let { editor, activeMarks, isDark, colorEnabled }: Props = $props()

  interface FormatButton {
    id: string
    label: string
    icon: string
    shortcut: string
    mark: string
  }

  const BUTTONS: FormatButton[] = [
    { id: 'bold', label: 'Bold', icon: 'format_bold', shortcut: 'Ctrl+B', mark: 'bold' },
    { id: 'italic', label: 'Italic', icon: 'format_italic', shortcut: 'Ctrl+I', mark: 'italic' },
    { id: 'underline', label: 'Underline', icon: 'format_underlined', shortcut: 'Ctrl+U', mark: 'underline' },
    { id: 'strike', label: 'Strikethrough', icon: 'format_strikethrough', shortcut: 'Ctrl+Shift+X', mark: 'strike' },
    { id: 'code', label: 'Inline code', icon: 'code', shortcut: 'Ctrl+E', mark: 'code' },
    { id: 'highlight', label: 'Highlight', icon: 'highlight', shortcut: 'Ctrl+Shift+H', mark: 'highlight' },
    { id: 'subscript', label: 'Subscript', icon: 'subscript', shortcut: 'Ctrl,', mark: 'subscript' },
    { id: 'superscript', label: 'Superscript', icon: 'superscript', shortcut: 'Ctrl.', mark: 'superscript' }
  ]

  const ALIGN_BUTTONS = [
    { id: 'left', label: 'Align left', icon: 'format_align_left', shortcut: 'Ctrl+Shift+L' },
    { id: 'center', label: 'Align center', icon: 'format_align_center', shortcut: 'Ctrl+Shift+E' },
    { id: 'right', label: 'Align right', icon: 'format_align_right', shortcut: 'Ctrl+Shift+R' },
    { id: 'justify', label: 'Align justify', icon: 'format_align_justify', shortcut: 'Ctrl+Shift+J' }
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
    window.dispatchEvent(new CustomEvent('silt:set-block-align', { detail: align }))
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
  let clearIdx = $derived(ALIGN_START + ALIGN_BUTTONS.length + (colorEnabled ? 2 : 0))

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

<div class="format-toolbar" role="toolbar" aria-label="Text formatting" tabindex="-1" bind:this={toolbarEl} onkeydown={handleKeydown}>
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
        aria-keyshortcuts={btn.shortcut}
        data-tb
        tabindex={rovingIdx === i ? 0 : -1}
        onclick={() => handleClick(btn)}
        onfocus={() => (rovingIdx = i)}
        title={btn.label}
      >
        <span class="material-symbols-outlined" aria-hidden="true">{btn.icon}</span>
      </button>
    {/each}

    <button
      type="button"
      class="toolbar-btn"
      class:active={isActive('link')}
      aria-pressed={isActive('link')}
      aria-label="Insert link"
      aria-keyshortcuts="Ctrl+K"
      data-tb
      tabindex={rovingIdx === LINK_IDX ? 0 : -1}
      onclick={handleLink}
      onfocus={() => (rovingIdx = LINK_IDX)}
      title="Insert link"
    >
      <span class="material-symbols-outlined" aria-hidden="true">link</span>
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
        aria-keyshortcuts={btn.shortcut}
        data-tb
        tabindex={rovingIdx === ALIGN_START + i ? 0 : -1}
        onclick={() => handleAlign(btn.id)}
        onfocus={() => (rovingIdx = ALIGN_START + i)}
        title={btn.label}
      >
        <span class="material-symbols-outlined" aria-hidden="true">{btn.icon}</span>
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
      aria-keyshortcuts="Ctrl+\\"
      data-tb
      tabindex={rovingIdx === clearIdx ? 0 : -1}
      onclick={handleClear}
      onfocus={() => (rovingIdx = clearIdx)}
      title="Clear formatting"
    >
      <span class="material-symbols-outlined" aria-hidden="true">format_clear</span>
    </button>
  </div>
</div>

<style>
  .format-toolbar {
    display: flex;
    align-items: center;
    gap: 4px;
    height: 36px;
    padding: 0 8px;
    position: sticky;
    top: 0;
    z-index: 10;
    background: color-mix(in srgb, var(--color-surface, #1a1d24) 95%, transparent);
    backdrop-filter: blur(8px);
    border-bottom: 1px solid var(--color-border-muted, #2a2e36);
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
    transition: background 0.1s, color 0.1s;
    flex-shrink: 0;
  }

  .toolbar-btn:hover {
    background: color-mix(in srgb, var(--color-accent-primary-start, #4f7cff) 15%, transparent);
    color: var(--color-text-primary, #e6e6e6);
  }

  .toolbar-btn:focus-visible {
    outline: 2px solid var(--color-accent-primary-start, #4f7cff);
    outline-offset: -2px;
  }

  .toolbar-btn.active {
    background: color-mix(in srgb, var(--color-accent-primary-glow, #6fa3ff) 20%, transparent);
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
