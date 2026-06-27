// Shiki syntax highlighting for the Source view (#194). Renders the raw
// markdown with a theme-aware TextMate grammar — `**bold**`, `*italic*`,
// links, code, headings, lists, HTML markers — using the active Silt theme's
// color tokens. Pure-frontend: no IPC, no disk write.
//
// Shiki's `codeToHtml` lazy-loads the markdown grammar on first use; until it
// resolves (and on any error) the caller renders a plain-text fallback, so the
// source view never blocks on highlighting and never crashes on a bad theme.

import { codeToHtml } from 'shiki'

export type SourceTokens = Record<string, string>

/**
 * Minimal slice of Shiki's ThemeRegistration that this module constructs.
 * Kept local (rather than importing from @shikijs/types, a transitive dep) so
 * the mapper is self-documenting and decoupled from Shiki's type surface.
 * Structurally assignable to Shiki's `theme` option (all target fields are
 * optional, so this required-fields subset is compatible).
 */
export interface SourceShikiTheme {
  name: string
  type: 'dark' | 'light'
  fg: string
  bg: string
  colors: Record<string, string>
  tokenColors: {
    scope: string | string[]
    settings: { foreground?: string; fontStyle?: string }
  }[]
}

/**
 * Build a Shiki custom theme from Silt's effective CSS-token map. The single
 * place Shiki meets the Silt theme: a token rename or a new theme only needs
 * this mapper updated. Every token falls back to a sensible default so a
 * partial map (e.g. before initTheme has resolved) still highlights.
 */
export function tokensToShikiTheme(
  tokens: SourceTokens,
  mode: 'dark' | 'light'
): SourceShikiTheme {
  const bg = tokens['--color-surface'] ?? '#1a1d24'
  const fg = tokens['--color-text-primary'] ?? '#e6e6e6'
  const muted = tokens['--color-text-muted'] ?? '#8b95a3'
  const accent = tokens['--color-accent-primary-start'] ?? '#4f7cff'
  const accent2 = tokens['--color-accent-secondary-start'] ?? '#22d3ee'
  const warn = tokens['--color-status-warn'] ?? '#f0a020'

  return {
    name: 'silt-source',
    type: mode,
    fg,
    bg,
    colors: { 'editor.background': bg },
    tokenColors: [
      // Headings — accent + bold so structure pops.
      {
        scope: ['markup.heading'],
        settings: { foreground: accent, fontStyle: 'bold' }
      },
      // The typographic emphasis marks.
      {
        scope: ['markup.bold'],
        settings: { foreground: fg, fontStyle: 'bold' }
      },
      {
        scope: ['markup.italic'],
        settings: { foreground: fg, fontStyle: 'italic' }
      },
      {
        scope: ['markup.strike'],
        settings: { foreground: muted, fontStyle: 'strikethrough' }
      },
      // Links + inline code → secondary accent.
      {
        scope: ['markup.underline.link', 'markup.link'],
        settings: { foreground: accent2 }
      },
      {
        scope: ['markup.raw.inline', 'markup.code'],
        settings: { foreground: accent2 }
      },
      { scope: ['markup.inserted'], settings: { foreground: accent } },
      { scope: ['markup.deleted'], settings: { foreground: warn } },
      // Lists / quotes / punctuation muted so the content reads through.
      { scope: ['markup.list'], settings: { foreground: accent } },
      {
        scope: ['markup.quote'],
        settings: { foreground: muted, fontStyle: 'italic' }
      },
      { scope: ['punctuation.definition'], settings: { foreground: muted } },
      // HTML/SGML tags — the <span style>, <u>, <!-- id --> markers that the
      // on-disk format carries (color spans, alignment, block-identity).
      { scope: ['entity.name.tag'], settings: { foreground: accent2 } },
      { scope: ['punctuation.definition.tag'], settings: { foreground: muted } }
    ]
  }
}

/**
 * Highlight raw markdown. Returns the highlighted inner HTML (Shiki's token
 * spans, without the wrapping `<pre><code>`) so the caller renders it inside
 * its own `<pre>` and keeps its gutter + styling. Returns null when Shiki
 * fails or is not ready — the caller falls back to plain text.
 */
export async function highlightMarkdown(
  code: string,
  theme: SourceShikiTheme
): Promise<string | null> {
  try {
    const html = await codeToHtml(code, { lang: 'markdown', theme })
    return extractInner(html)
  } catch {
    return null
  }
}

// Shiki wraps output as `<pre class="shiki" ...><code>INNER</code></pre>`. Pull
// out INNER so we render inside the source-viewer's own <pre> (keeps the line
// gutter + the existing CSS). Any unexpected shape yields '' → plain fallback.
function extractInner(html: string): string {
  const m = html.match(/^<pre\b[^>]*><code>([\s\S]*)<\/code><\/pre>\s*$/)
  return m ? m[1] : ''
}
