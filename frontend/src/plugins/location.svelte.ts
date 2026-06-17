/**
 * Module-scoped reactive location state for the plugin context (#69).
 *
 * This is the single source of truth for the active notebook/section/page
 * that plugins read via `ctx.activeNotebook` etc. It is a `$state` object
 * so plugins reading these values inside a Svelte reactive context (template,
 * `$derived`, `$effect`) automatically re-render when the user navigates.
 *
 * Plugins that read `ctx.active*` inside `init()` and cache the value see a
 * stale snapshot — that is an inherent limitation of destructuring. Plugins
 * that read `ctx.activeNotebook` at query time always see the live value.
 *
 * #100: `source` ('vault' | 'linked:<id>') is resolved from a notebook-name →
 * source map kept in sync by the Sidebar whenever the navigation tree reloads.
 * Notebook display names are globally unique (LinkNotebook rejects collisions),
 * so the name unambiguously resolves the source. This keeps the source flowing
 * to FetchPageBlocks/SaveFileBlocks without threading it through every sidebar
 * selection handler.
 */
let activeLocation = $state({
  notebook: '',
  section: '',
  page: '',
  source: 'vault' as string
})

// notebook display name → source ('vault' | 'linked:<id>'). Refreshed by the
// Sidebar on each navigation-tree load.
let sourceMap = new Map<string, string>()

export function setActiveLocation(
  notebook: string,
  section: string,
  page: string
): void {
  activeLocation.notebook = notebook
  activeLocation.section = section
  activeLocation.page = page
  activeLocation.source = sourceMap.get(notebook) ?? 'vault'
}

export function getActiveLocation() {
  return activeLocation
}

/** Source for an arbitrary notebook name (used by page load/save paths). */
export function getSourceForNotebook(notebook: string): string {
  return sourceMap.get(notebook) ?? 'vault'
}

/**
 * Replace the notebook-name → source map. Called by the Sidebar after each
 * navigation-tree load so source resolution stays current as notebooks are
 * linked/unlinked/renamed.
 */
export function setNotebookSourceMap(entries: Map<string, string>): void {
  sourceMap = entries
  // Re-resolve the active source in case the active notebook's source changed
  // (e.g. it was just linked/unlinked).
  activeLocation.source = sourceMap.get(activeLocation.notebook) ?? 'vault'
}
