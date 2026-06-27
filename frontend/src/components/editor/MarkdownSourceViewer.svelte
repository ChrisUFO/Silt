<script lang="ts">
  import { onDestroy } from 'svelte'
  import type { ParsedBlock } from '../../lib/editor/types'
  import { themeState } from '../../theme/store.svelte'
  import {
    highlightMarkdown,
    tokensToShikiTheme
  } from '../../lib/editor/useMarkdownHighlighter'

  // MarkdownSourceViewer — renders the raw markdown representation of the
  // page's blocks as a read-only <pre> with line numbers (#171). Shows the
  // exact on-disk representation including formatting marks (bold, italic,
  // color spans, alignment markers, etc.). "Copy as Markdown" copies the
  // full content to the clipboard. Syntax is highlighted via Shiki, driven
  // by the active theme's tokens (#194); until the highlighter resolves
  // (and on any error) the plain text is rendered so the view never blocks.

  interface Props {
    blocks: ParsedBlock[]
    filePath: string
  }

  let { blocks, filePath }: Props = $props()

  // Reconstruct the raw markdown from blocks. Each block becomes one line
  // with its raw_text (which includes bullet markers, heading hashes, task
  // checkboxes, and formatted clean_text).
  function reconstructMarkdown(blocks: ParsedBlock[]): string {
    const lines: string[] = []
    for (const block of blocks) {
      const indent = '  '.repeat(block.depth || 0)
      let line = block.raw_text || block.clean_text || ''
      lines.push(indent + line)
    }
    return lines.join('\n')
  }

  let markdown = $derived(reconstructMarkdown(blocks))
  let lineCount = $derived(markdown.split('\n').length)

  // Resolve the effective (mode-resolved) token map + concrete dark/light
  // mode from the theme store. "system" follows prefers-color-scheme.
  //
  // The theme store re-injects editor CSS on an OS dark↔light flip but does
  // NOT change themeState.mode or its token maps, so a $derived that read
  // only themeState + a one-shot matchMedia() call would miss the flip and
  // leave the Source view's Shiki colors stale. Track the OS scheme in
  // $state and keep it in sync with an MQL listener so the highlight
  // re-derives on a live OS scheme change (#194 AC: re-highlights on theme
  // mode change — covers the system-mode path).
  let systemLight = $state(
    typeof window !== 'undefined' &&
      !!window.matchMedia?.('(prefers-color-scheme: light)').matches
  )
  $effect(() => {
    if (typeof window === 'undefined' || !window.matchMedia) return
    const mql = window.matchMedia('(prefers-color-scheme: light)')
    const onChange = (): void => {
      systemLight = mql.matches
    }
    mql.addEventListener('change', onChange)
    return () => mql.removeEventListener('change', onChange)
  })
  let effectiveMode = $derived<'dark' | 'light'>(
    themeState.mode === 'light'
      ? 'light'
      : themeState.mode === 'dark'
        ? 'dark'
        : systemLight
          ? 'light'
          : 'dark'
  )
  let tokens = $derived(
    effectiveMode === 'light' ? themeState.lightTokens : themeState.darkTokens
  )

  // Re-highlight whenever the source, theme tokens, or mode change (#194 AC:
  // re-highlights on theme mode change). Shiki loads the markdown grammar
  // lazily on the first call, so the result is async; a monotonic sequence
  // guards against out-of-order resolves (a slow earlier highlight landing
  // after a newer one). Before the first resolve, highlightedHtml is null and
  // the template falls back to plain text.
  let highlightedHtml = $state<string | null>(null)
  let highlightSeq = 0
  $effect(() => {
    const md = markdown
    const t = tokens
    const mode = effectiveMode
    const seq = ++highlightSeq
    void (async () => {
      // highlightMarkdown is expected to return null (never throw), but a
      // stray rejection must degrade to the plain-text fallback rather than
      // surface an unhandled rejection — highlighting is cosmetic, the
      // source text is the load-bearing content.
      let html: string | null = null
      try {
        html = await highlightMarkdown(md, tokensToShikiTheme(t, mode))
      } catch {
        html = null
      }
      if (seq === highlightSeq) highlightedHtml = html
    })()
  })

  // Copy feedback: a transient status announced to assistive tech so the
  // button isn't a silent action. Cleared after a short delay. The timer is
  // cleared on unmount so toggling back to Edit (which unmounts this viewer)
  // mid-countdown doesn't leave a pending callback writing to a destroyed
  // component (a Svelte 5 dev-mode warning — this PR removes console warnings,
  // so don't reintroduce one here).
  let copyStatus = $state<{ kind: 'ok' | 'err'; msg: string } | null>(null)
  let copyStatusTimer: ReturnType<typeof setTimeout> | null = null
  function setCopyStatus(kind: 'ok' | 'err', msg: string): void {
    copyStatus = { kind, msg }
    if (copyStatusTimer) clearTimeout(copyStatusTimer)
    copyStatusTimer = setTimeout(() => {
      copyStatus = null
      copyStatusTimer = null
    }, 2500)
  }
  onDestroy(() => {
    if (copyStatusTimer) clearTimeout(copyStatusTimer)
  })
  async function copyAsMarkdown(): Promise<void> {
    try {
      await navigator.clipboard.writeText(markdown)
      setCopyStatus('ok', 'Copied markdown to clipboard.')
    } catch {
      setCopyStatus('err', 'Failed to copy — clipboard unavailable.')
    }
  }
</script>

<div class="source-viewer">
  <div class="source-header">
    <span class="file-path" title={filePath}>{filePath}</span>
    <div class="header-actions">
      <button
        type="button"
        class="copy-btn"
        onclick={copyAsMarkdown}
        aria-label="Copy markdown to clipboard"
      >
        <span class="material-symbols-outlined" aria-hidden="true"
          >content_copy</span
        >
        Copy as Markdown
      </button>
      {#if copyStatus}
        <span
          class="copy-status"
          class:copy-status--err={copyStatus.kind === 'err'}
          role={copyStatus.kind === 'err' ? 'alert' : 'status'}
          aria-live={copyStatus.kind === 'err' ? 'assertive' : 'polite'}
          >{copyStatus.msg}</span
        >
      {/if}
    </div>
  </div>
  <div
    class="source-body"
    role="document"
    aria-label="Source view of {filePath}"
  >
    <div class="line-numbers" aria-hidden="true">
      {#each Array(lineCount) as _, i}
        <span class="line-num">{i + 1}</span>
      {/each}
    </div>
    <pre
      class="source-code">{#if highlightedHtml}{@html highlightedHtml}{:else}{markdown}{/if}</pre>
  </div>
</div>

<style>
  .source-viewer {
    display: flex;
    flex-direction: column;
    height: 100%;
    background: var(--color-surface, #1a1d24);
  }

  .source-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 6px 12px;
    border-bottom: 1px solid var(--color-border-muted, #2a2e36);
    flex-shrink: 0;
  }

  .file-path {
    font-size: 0.75rem;
    color: var(--color-text-muted, #8b95a3);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .copy-btn {
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 3px 8px;
    border: 1px solid var(--color-border-muted, #3a3f4b);
    border-radius: 6px;
    background: transparent;
    color: var(--color-text-muted, #8b95a3);
    font-size: 0.72rem;
    cursor: pointer;
  }

  .copy-btn:hover {
    background: color-mix(
      in srgb,
      var(--color-accent-primary-start, #4f7cff) 15%,
      transparent
    );
    color: var(--color-text-primary, #e6e6e6);
  }

  .copy-btn .material-symbols-outlined {
    font-size: 14px;
  }

  .copy-status {
    font-size: 0.7rem;
    color: var(--color-text-muted, #8b95a3);
    white-space: nowrap;
  }

  .copy-status--err {
    color: var(--color-status-danger, #f87171);
  }

  .source-body {
    display: flex;
    overflow: auto;
    flex: 1;
  }

  .line-numbers {
    display: flex;
    flex-direction: column;
    padding: 8px 8px 8px 12px;
    text-align: right;
    user-select: none;
    border-right: 1px solid var(--color-border-muted, #2a2e36);
    flex-shrink: 0;
  }

  .line-num {
    font-family: var(--font-mono, monospace);
    font-size: 0.75rem;
    line-height: 1.6;
    color: var(--color-text-muted, #555);
    opacity: 0.5;
  }

  .source-code {
    margin: 0;
    padding: 8px 12px;
    font-family: var(--font-mono, monospace);
    font-size: 0.8rem;
    line-height: 1.6;
    color: var(--color-text-primary, #e6e6e6);
    white-space: pre-wrap;
    word-break: break-word;
    flex: 1;
  }
</style>
