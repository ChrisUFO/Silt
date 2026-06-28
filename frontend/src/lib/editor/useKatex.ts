// KaTeX renderer (#191). KaTeX (~270KB minified + fonts) is dynamically imported
// on first math node so it never enters the main editor bundle. The module and
// its CSS load once; subsequent renders are synchronous after the import
// resolves. Mirrors the lazy-singleton pattern in useMermaid.ts.

type KatexApi = {
  renderToString: (tex: string, options: Record<string, unknown>) => string
}

let katexPromise: Promise<KatexApi> | null = null

function loadKatex(): Promise<KatexApi> {
  if (!katexPromise) {
    // Load the JS + the stylesheet together; the CSS is a side-effect import
    // (Vite injects it on first load). Resolves to the KaTeX default export.
    katexPromise = Promise.all([
      import('katex'),
      import('katex/dist/katex.min.css')
    ])
      .then(([m]) => (m.default as KatexApi) ?? (m as unknown as KatexApi))
      .catch((e) => {
        // Clear the singleton so a transient import failure (offline blip,
        // bundler race) doesn't permanently poison every subsequent render.
        katexPromise = null
        throw e
      })
  }
  return katexPromise
}

/** Test-only: drop the singleton so a fresh load can be forced. */
export function resetKatexForTests(): void {
  katexPromise = null
}

// Render LaTeX to an HTML string. `throwOnError: false` makes KaTeX render a
// parse error inline in error color rather than throwing; the try/catch is a
// last-resort for catastrophic failures (never corrupts the doc). Empty input
// renders nothing.
export async function renderKatex(
  latex: string,
  displayMode: boolean
): Promise<{ html: string; error: string | null }> {
  if (!latex) return { html: '', error: null }
  try {
    const katex = await loadKatex()
    const html = katex.renderToString(latex, {
      displayMode,
      throwOnError: false,
      errorColor: 'var(--color-error, #ef4444)',
      output: 'htmlAndMathml',
      strict: 'warn'
    })
    return { html, error: null }
  } catch (e) {
    return { html: '', error: e instanceof Error ? e.message : String(e) }
  }
}
