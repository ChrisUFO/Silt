<script lang="ts">
  import { NodeViewWrapper, NodeViewContent } from 'svelte-tiptap'
  import type { NodeViewProps } from '@tiptap/core'

  let { node, editor }: NodeViewProps = $props()
  let lang = $derived(node.attrs.lang || '')
  let isFocused = $derived(editor.isFocused)
  let justCopied = $state(false)

  function copyContent(): void {
    const text = node.textContent || ''
    navigator.clipboard
      .writeText(text)
      .then(() => {
        justCopied = true
        setTimeout(() => {
          justCopied = false
        }, 2000)
      })
      .catch(() => {
        /* Clipboard may fail in restricted contexts — silently ignore */
      })
  }
</script>

<NodeViewWrapper
  class="code-block-wrapper my-3 rounded-lg overflow-hidden border border-border"
  data-type="code-block"
  role="region"
  aria-label={lang ? `Code block (${lang})` : 'Code block'}
>
  <div
    class="flex items-center justify-between px-4 py-1.5 bg-bg-interface text-text-muted text-xs border-b border-border"
  >
    <span class="font-mono uppercase tracking-wide" aria-hidden="true">
      {lang || 'code'}
    </span>
    <div class="flex items-center gap-2">
      {#if justCopied}
        <span
          class="text-status-success text-[11px]"
          role="status"
          aria-live="polite"
        >
          Copied
        </span>
      {/if}
      <button
        class="material-symbols-outlined text-[16px] hover:text-text transition-colors cursor-pointer select-none"
        class:opacity-0={!isFocused && !justCopied}
        onclick={copyContent}
        aria-label="Copy code"
      >
        {justCopied ? 'check' : 'content_copy'}
      </button>
    </div>
  </div>
  <pre class="px-4 py-3 overflow-x-auto bg-bg text-sm leading-relaxed">
    <NodeViewContent class="font-mono whitespace-pre" />
  </pre>
</NodeViewWrapper>
