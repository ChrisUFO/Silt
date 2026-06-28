// Mermaid diagram renderer (#190). mermaid.js (~200KB gzipped) is dynamically
// imported on first mermaid block so it never enters the main bundle. The
// module is initialized once per theme (re-init is idempotent and re-colors
// existing diagrams); results are memoised by (theme|source) so re-renders
// after a cursor move are cheap. securityLevel:'strict' disables click
// handlers / HTML in diagram source — the local single-user threat model still
// favours strict so a malformed diagram can't inject markup.

type MermaidApi = {
  initialize: (config: Record<string, unknown>) => void
  parse: (text: string) => Promise<unknown>
  render: (id: string, text: string) => Promise<{ svg: string }>
}

let mermaidPromise: Promise<MermaidApi> | null = null
let initTheme: string | null = null
const cache = new Map<string, string>()
const CACHE_CAP = 32
let counter = 0

function setCache(key: string, svg: string): void {
  if (cache.size >= CACHE_CAP) {
    const first = cache.keys().next().value
    if (first !== undefined) cache.delete(first)
  }
  cache.set(key, svg)
}

async function loadMermaid(): Promise<MermaidApi> {
  if (!mermaidPromise) {
    mermaidPromise = import('mermaid')
      .then((m) => (m.default as MermaidApi) ?? (m as unknown as MermaidApi))
      .catch((e) => {
        // Clear the singleton so a transient import failure doesn't permanently
        // poison every subsequent render for the session.
        mermaidPromise = null
        throw e
      })
  }
  return mermaidPromise
}

export type MermaidTheme = 'dark' | 'default'

async function ensureMermaid(theme: MermaidTheme): Promise<MermaidApi> {
  const api = await loadMermaid()
  if (initTheme !== theme) {
    api.initialize({ startOnLoad: false, theme, securityLevel: 'strict' })
    initTheme = theme
    // Cached SVGs are theme-coloured; a theme switch invalidates them.
    cache.clear()
  }
  return api
}

/** Test-only: drop the singleton + cache so a fresh module load can be forced. */
export function resetMermaidForTests(): void {
  mermaidPromise = null
  initTheme = null
  cache.clear()
}

// Stamp a unique id onto a cached SVG so two blocks sharing the same source
// don't emit duplicate id attributes (and so internal url(#…) marker/gradient
// refs resolve within their own diagram, not the first one in the DOM).
function withUniqueId(rawSvg: string): string {
  const id = `silt-mermaid-${counter++}`
  return rawSvg.replace(/silt-mermaid-\d+/g, id)
}

// Render a diagram definition to an SVG string, or return an error message for
// invalid source (the caller renders it inline, never a blank box). Empty
// source renders nothing. Errors never throw — a bad diagram must not break
// the editor. The parse + render result is memoised by (theme|source); each
// call then stamps a fresh unique id so identical-source blocks don't collide.
export async function renderMermaid(
  source: string,
  theme: MermaidTheme
): Promise<{ svg: string; error: string | null }> {
  if (!source.trim()) return { svg: '', error: null }
  const key = `${theme}|${source}`
  const hit = cache.get(key)
  if (hit !== undefined) return { svg: withUniqueId(hit), error: null }
  try {
    const api = await ensureMermaid(theme)
    await api.parse(source)
    const { svg } = await api.render(`silt-mermaid-${counter++}`, source)
    setCache(key, svg)
    return { svg: withUniqueId(svg), error: null }
  } catch (e) {
    const msg = e instanceof Error ? e.message : String(e)
    return { svg: '', error: msg }
  }
}
