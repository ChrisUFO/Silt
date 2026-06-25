<script lang="ts">
  import { NodeViewWrapper, NodeViewContent } from 'svelte-tiptap'
  import type { NodeViewProps } from '@tiptap/core'
  import { themeState } from '../../theme/store.svelte'
  import { isSystemDark } from '../../lib/systemTheme.svelte'
  import {
    highlightCode,
    COMMON_LANGUAGES,
    type ShikiTheme
  } from '../../lib/editor/useShiki'
  import { pushNotification } from '../../notifications/store.svelte'

  // Dual-layer code block (#189). The ProseMirror-managed contenteditable
  // (NodeViewContent) carries the raw text with a TRANSPARENT foreground, so
  // only the caret is visible there. A Shiki-highlighted `<pre>` sits behind
  // it at identical font metrics, supplying the coloured tokens. Both layers
  // share the editor's mono font / size / line-height / padding, so the
  // coloured tokens line up exactly with the (invisible) raw characters.
  let { node, updateAttributes, selected }: NodeViewProps = $props()

  let language = $derived((node.attrs.language as string) || '')
  // node.textContent reacts to transactions; it is the source the Shiki layer
  // mirrors. Falling back to '' keeps the highlighter happy on empty blocks.
  let code = $derived(node.textContent ?? '')

  let isDark = $derived(
    themeState.mode === 'dark' ||
      (themeState.mode === 'system' && isSystemDark())
  )
  let shikiTheme = $derived<ShikiTheme>(isDark ? 'github-dark' : 'github-light')

  let highlighted = $state('')
  // `highlightedFor` is the code string the Shiki layer currently renders.
  // While it lags behind the live `code` (during continuous typing, before the
  // async highlighter resolves), the editable layer shows its own text in a
  // solid colour so newly-typed characters are visible immediately instead of
  // disappearing into the transparent layer until the debounce fires.
  let highlightedFor = $state('')
  let stale = $derived(code !== highlightedFor)
  let copyState = $state<'idle' | 'done' | 'error'>('idle')

  // Re-highlight (debounced) whenever the code, language, or theme changes.
  // Shiki is async (lazy grammar load); on resolve we publish the highlighted
  // HTML and mark the layer fresh for the code it covers. The editable layer's
  // visibility tracks `stale`, so there is never a window where typed text is
  // invisible — it shows solid while stale and goes transparent (yielding to
  // Shiki's colours) once the highlighter catches up.
  let highlightTimer: ReturnType<typeof setTimeout> | null = null
  $effect(() => {
    const c = code
    const lang = language
    const theme = shikiTheme
    if (highlightTimer) clearTimeout(highlightTimer)
    const t = setTimeout(async () => {
      const html = await highlightCode(c, lang, theme)
      highlighted = html
      highlightedFor = c
    }, 120)
    highlightTimer = t
    // Cancel the pending highlight if the block unmounts during the debounce
    // window so the callback never writes $state on a destroyed scope.
    return () => clearTimeout(t)
  })

  async function copyCode(): Promise<void> {
    try {
      await navigator.clipboard.writeText(code)
      copyState = 'done'
      setTimeout(() => (copyState = 'idle'), 1200)
    } catch {
      copyState = 'error'
      pushNotification({
        kind: 'error',
        message: 'Could not copy code to the clipboard.'
      })
    }
  }

  function onLanguageChange(e: Event): void {
    const value = (e.currentTarget as HTMLSelectElement).value
    updateAttributes({ language: value })
  }
</script>

<NodeViewWrapper
  class={`silt-code group relative my-1${selected ? ' selected' : ''}`}
  data-language={language || 'plaintext'}
  role="region"
  aria-label={language ? `${language} code block` : 'code block'}
>
  <div class="silt-code-bar">
    <select
      class="silt-code-lang"
      value={language}
      aria-label="Code language"
      onchange={onLanguageChange}
    >
      <option value="">(no language)</option>
      {#each COMMON_LANGUAGES as lang (lang)}
        <option value={lang}>
          {lang}
        </option>
      {/each}
    </select>
    <button
      type="button"
      class="silt-code-copy"
      onclick={copyCode}
      aria-label="Copy code"
    >
      <span class="material-symbols-outlined" aria-hidden="true">
        {copyState === 'done' ? 'check' : 'content_copy'}
      </span>
    </button>
  </div>
  <div class="silt-code-body">
    <!-- Shiki highlight layer (visible, non-interactive). -->
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div class="silt-code-display" aria-hidden="true">
      {@html highlighted}
    </div>
    <!-- Editable layer (transparent text once Shiki is fresh, solid colour
         while it lags so typed text is always visible). ProseMirror owns it;
         the Shiki display layer provides the semantic <pre><code> markup, so
         this layer only hosts the caret (self-closing, like every other NodeView). -->
    <NodeViewContent
      as="pre"
      class={`silt-code-edit${stale ? ' code-stale' : ''}`}
    />
  </div>
</NodeViewWrapper>
