<script lang="ts">
  import type { Editor } from 'svelte-tiptap'
  import { serializeInlineContent } from '../../lib/editor/converters'

  interface Props {
    editor: Editor | null
    colorEnabled: boolean
    isDark: boolean
  }

  let { editor, colorEnabled, isDark }: Props = $props()

  let menuOpen = $state(false)

  const ALIGN_OPTS = [
    { id: 'left', label: 'Align left', icon: 'format_align_left' },
    { id: 'center', label: 'Align center', icon: 'format_align_center' },
    { id: 'right', label: 'Align right', icon: 'format_align_right' },
    { id: 'justify', label: 'Align justify', icon: 'format_align_justify' }
  ]

  function clearFormatting(): void {
    editor?.chain().focus().unsetAllMarks().run()
    menuOpen = false
  }

  async function copyAsMarkdown(): Promise<void> {
    if (!editor) return
    const { selection } = editor.state
    let md = ''
    if (selection.empty) {
      // Copy the entire block's content as markdown
      const pos = selection.$from
      for (let d = pos.depth; d >= 1; d--) {
        const node = pos.node(d)
        if (['noteBlock', 'taskBlock', 'headerBlock'].includes(node.type.name)) {
          const json = node.toJSON()
          md = json.content ? serializeInlineContent(json.content) : ''
          break
        }
      }
    } else {
      // Serialize the selection range as markdown
      const slice = editor.state.doc.slice(selection.from, selection.to)
      const parts: string[] = []
      slice.content.forEach((node) => {
        const json = node.toJSON()
        parts.push(json.content ? serializeInlineContent(json.content) : (json.text || ''))
      })
      md = parts.join('\n')
    }
    try {
      await navigator.clipboard.writeText(md)
    } catch {
      // Clipboard may be unavailable
    }
    menuOpen = false
  }

  async function copyAsPlainText(): Promise<void> {
    if (!editor) return
    const { selection } = editor.state
    const text = selection.empty
      ? ''
      : editor.state.doc.textBetween(selection.from, selection.to, '\n')
    try {
      await navigator.clipboard.writeText(text)
    } catch {
      // Clipboard may be unavailable
    }
    menuOpen = false
  }

  function handleAlign(align: string): void {
    window.dispatchEvent(new CustomEvent('silt:set-block-align', { detail: align }))
    menuOpen = false
  }

  function openColorPicker(markType: 'textColor' | 'backgroundColor'): void {
    window.dispatchEvent(new CustomEvent('silt:open-color-picker', { detail: markType }))
    menuOpen = false
  }
</script>

<div class="block-hover-menu-wrapper">
  <button
    type="button"
    class="hover-trigger"
    aria-label="Block actions"
    aria-expanded={menuOpen}
    aria-haspopup="menu"
    onclick={() => (menuOpen = !menuOpen)}
  >
    <span class="material-symbols-outlined" aria-hidden="true">more_horiz</span>
  </button>

  {#if menuOpen}
    <div class="block-menu" role="menu" aria-label="Block actions">
      {#each ALIGN_OPTS as opt (opt.id)}
        <button type="button" class="menu-item" role="menuitem" onclick={() => handleAlign(opt.id)}>
          <span class="material-symbols-outlined" aria-hidden="true">{opt.icon}</span>
          <span>{opt.label}</span>
        </button>
      {/each}

      {#if colorEnabled}
        <div class="menu-separator" aria-hidden="true"></div>
        <button type="button" class="menu-item" role="menuitem" onclick={() => openColorPicker('textColor')}>
          <span class="material-symbols-outlined" aria-hidden="true">format_color_text</span>
          <span>Text color</span>
        </button>
        <button type="button" class="menu-item" role="menuitem" onclick={() => openColorPicker('backgroundColor')}>
          <span class="material-symbols-outlined" aria-hidden="true">format_color_fill</span>
          <span>Background color</span>
        </button>
      {/if}

      <div class="menu-separator" aria-hidden="true"></div>
      <button type="button" class="menu-item" role="menuitem" onclick={clearFormatting}>
        <span class="material-symbols-outlined" aria-hidden="true">format_clear</span>
        <span>Clear formatting</span>
      </button>
      <button type="button" class="menu-item" role="menuitem" onclick={copyAsMarkdown}>
        <span class="material-symbols-outlined" aria-hidden="true">content_copy</span>
        <span>Copy as Markdown</span>
      </button>
      <button type="button" class="menu-item" role="menuitem" onclick={copyAsPlainText}>
        <span class="material-symbols-outlined" aria-hidden="true">content_paste</span>
        <span>Copy as plain text</span>
      </button>
    </div>
  {/if}
</div>

<style>
  .block-hover-menu-wrapper {
    position: relative;
    display: inline-flex;
  }

  .hover-trigger {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 22px;
    height: 22px;
    border: none;
    border-radius: 5px;
    background: transparent;
    color: var(--color-text-muted, #8b95a3);
    cursor: pointer;
    transition: background 0.1s, color 0.1s;
  }

  .hover-trigger:hover {
    background: color-mix(in srgb, var(--color-accent-primary-start, #4f7cff) 15%, transparent);
    color: var(--color-text-primary, #e6e6e6);
  }

  .hover-trigger .material-symbols-outlined {
    font-size: 16px;
  }

  .block-menu {
    position: absolute;
    top: 100%;
    left: 0;
    z-index: 50;
    min-width: 180px;
    padding: 4px;
    border-radius: 8px;
    background: var(--color-surface, #1e1e22);
    border: 1px solid var(--color-border-muted, #33333a);
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.35);
    display: flex;
    flex-direction: column;
    gap: 1px;
  }

  .menu-separator {
    height: 1px;
    background: var(--color-border-muted, #33333a);
    margin: 2px 4px;
  }

  .menu-item {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 5px 10px;
    border: none;
    border-radius: 6px;
    background: transparent;
    color: var(--color-text-primary, #e6e6e6);
    font-size: 0.78rem;
    text-align: left;
    cursor: pointer;
  }

  .menu-item:hover {
    background: color-mix(in srgb, var(--color-accent-primary-start, #4f7cff) 15%, transparent);
  }

  .menu-item .material-symbols-outlined {
    font-size: 16px;
    color: var(--color-text-muted, #8b95a3);
  }

  @media (prefers-reduced-motion: reduce) {
    .hover-trigger {
      transition: none;
    }
  }
</style>
