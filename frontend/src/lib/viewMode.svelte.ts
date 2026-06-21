import { settings } from '../settings/store.svelte'

export type ViewMode = 'edit' | 'source'

function pageKey(notebook: string, section: string, page: string): string {
  return `${notebook}/${section}/${page}`
}

// Module-scoped reactive record: page key → view mode. Uses a plain object
// (not Map) so Svelte 5's $state deeply tracks property reads/writes.
const viewModes = $state<Record<string, ViewMode>>({})

export function getViewMode(
  notebook: string,
  section: string,
  page: string
): ViewMode {
  const key = pageKey(notebook, section, page)
  if (viewModes[key]) return viewModes[key]
  // Fall back to the per-vault default_view_mode config (#171).
  const configured = settings.config?.editor?.default_view_mode
  return configured === 'source' ? 'source' : 'edit'
}

export function setViewMode(
  notebook: string,
  section: string,
  page: string,
  mode: ViewMode
): void {
  viewModes[pageKey(notebook, section, page)] = mode
}

export function toggleViewMode(
  notebook: string,
  section: string,
  page: string
): void {
  const current = getViewMode(notebook, section, page)
  setViewMode(notebook, section, page, current === 'edit' ? 'source' : 'edit')
}
