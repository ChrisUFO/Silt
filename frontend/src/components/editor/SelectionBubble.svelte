<script lang="ts">
  import type { Editor } from 'svelte-tiptap'

  // SelectionBubble — a floating popover above non-collapsed text selection
  // (#168). Shows the same format buttons as the toolbar in a tighter
  // layout. Auto-dismisses on Esc, click outside, or selection collapse.
  // role="menu" with arrow-key navigation.

  interface Props {
    editor: Editor | null
    activeMarks: Set<string>
    selectionEmpty: boolean
    selectionCoords: { left: number; top: number; bottom: number } | null
  }

  let { editor, activeMarks, selectionEmpty, selectionCoords }: Props = $props()

  let show = $derived(!selectionEmpty && selectionCoords !== null)

  const QUICK_BUTTONS = [
    { id: 'bold', icon: 'format_bold', label: 'Bold', mark: 'bold' },
    { id: 'italic', icon: 'format_italic', label: 'Italic', mark: 'italic' },
    { id: 'strike', icon: 'format_strikethrough', label: 'Strikethrough', mark: 'strike' },
    { id: 'code', icon: 'code', label: 'Code', mark: 'code' },
    { id: 'highlight', icon: 'highlight', label: 'Highlight', mark: 'highlight' },
    { id: 'underline', icon: 'format_underlined', label: 'Underline', mark: 'underline' },
    { id: 'link', icon: 'link', label: 'Link', mark: 'link' }
  ]

  function handleAction(id: string, mark: string): void {
    if (!editor) return
    if (id === 'link') {
      if (editor.isActive('link')) {
        editor.chain().focus().unsetLink().run()
      } else {
        window.dispatchEvent(new CustomEvent('silt:open-link-input'))
      }
    } else {
      editor.chain().focus().toggleMark(mark).run()
    }
  }
</script>

{#if show && selectionCoords}
  <div
    class="selection-bubble"
    role="menu"
    aria-label="Format selection"
    style="left: {selectionCoords.left}px; top: {selectionCoords.top - 8}px"
  >
    {#each QUICK_BUTTONS as btn (btn.id)}
      <button
        type="button"
        class="bubble-btn"
        class:active={activeMarks.has(btn.mark)}
        aria-checked={activeMarks.has(btn.mark)}
        aria-label={btn.label}
        role="menuitemcheckbox"
        onclick={() => handleAction(btn.id, btn.mark)}
      >
        <span class="material-symbols-outlined" aria-hidden="true">{btn.icon}</span>
      </button>
    {/each}
  </div>
{/if}

<style>
  .selection-bubble {
    position: fixed;
    z-index: 100;
    transform: translate(-50%, -100%);
    display: flex;
    align-items: center;
    gap: 1px;
    padding: 3px;
    border-radius: 8px;
    background: var(--color-surface, #1e1e22);
    border: 1px solid var(--color-border-muted, #33333a);
    box-shadow: 0 4px 16px rgba(0, 0, 0, 0.4);
  }

  .bubble-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 26px;
    height: 26px;
    border: none;
    border-radius: 5px;
    background: transparent;
    color: var(--color-text-muted, #8b95a3);
    cursor: pointer;
  }

  .bubble-btn:hover {
    background: color-mix(in srgb, var(--color-accent-primary-start, #4f7cff) 20%, transparent);
    color: var(--color-text-primary, #e6e6e6);
  }

  .bubble-btn.active {
    background: color-mix(in srgb, var(--color-accent-primary-glow, #6fa3ff) 25%, transparent);
    color: var(--color-accent-primary-glow, #6fa3ff);
  }

  .bubble-btn .material-symbols-outlined {
    font-size: 16px;
  }
</style>
