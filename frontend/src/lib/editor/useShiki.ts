// Shiki syntax-highlighting helper (#189). Shiki's `codeToHtml` lazy-loads the
// grammar for the requested language, so we don't bundle every grammar up
// front. Results are memoised in-module keyed by (lang|theme|code): a code
// block re-highlights on every keystroke, and the common case is the same
// text re-rendered after a cursor move, so the cache is load-bearing.
//
// The editor is single-user/local, so an unbounded cache would leak memory
// across a long session; the cache is capped (oldest entries evicted).

import { codeToHtml } from 'shiki'

const cache = new Map<string, string>()
const CACHE_CAP = 128

export type ShikiTheme = 'github-dark' | 'github-light'

function setCache(key: string, html: string): void {
  if (cache.size >= CACHE_CAP) {
    // Map preserves insertion order; drop the oldest.
    const first = cache.keys().next().value
    if (first !== undefined) cache.delete(first)
  }
  cache.set(key, html)
}

// Highlight `code` for `lang`, returning Shiki's `<pre><code>…</code></pre>`
// HTML string. Unknown/empty languages fall back to plain (no spans) so the
// block still renders. Errors degrade to an escaped plain rendering rather
// than throwing — a bad grammar must never break the editor.
//
// Security: the returned HTML is injected via {@html} in CodeBlockView. This
// is safe because Shiki escapes all user content in its token output (it
// produces <span class="..."> wrappers, never raw user HTML); the local
// single-user threat model doesn't warrant a DOMPurify pass on top.
export async function highlightCode(
  code: string,
  lang: string,
  theme: ShikiTheme
): Promise<string> {
  const language = lang || 'plaintext'
  const key = `${language}|${theme}|${code}`
  const hit = cache.get(key)
  if (hit !== undefined) return hit
  try {
    const html = await codeToHtml(code, { lang: language, theme })
    setCache(key, html)
    return html
  } catch {
    // Unknown language or transient load failure — render plain text.
    const plain = escapeHtml(code)
    const fallback = `<pre class="shiki"><code>${plain}</code></pre>`
    setCache(key, fallback)
    return fallback
  }
}

function escapeHtml(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

// The short list offered in the language picker. 'plaintext' is the default
// (an empty language attr renders as plaintext); there is no separate empty
// entry, which would just duplicate it in the dropdown. Shiki lazy-loads
// others on demand when the user types a known grammar id, so this is a UX
// hint, not a hard limit.
export const COMMON_LANGUAGES = [
  'plaintext',
  'typescript',
  'javascript',
  'go',
  'python',
  'rust',
  'json',
  'yaml',
  'markdown',
  'bash',
  'sql',
  'html',
  'css',
  'mermaid'
]
