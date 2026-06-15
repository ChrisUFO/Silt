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
 */
let activeLocation = $state({
  notebook: '',
  section: '',
  page: ''
})

export function setActiveLocation(
  notebook: string,
  section: string,
  page: string
): void {
  activeLocation.notebook = notebook
  activeLocation.section = section
  activeLocation.page = page
}

export function getActiveLocation() {
  return activeLocation
}
