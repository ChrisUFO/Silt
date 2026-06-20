// Per-page view mode store (#171). Tracks whether each page is in Edit mode
// (the TipTap WYSIWYG editor) or Source mode (raw markdown view). The mode is
// per-page and sticky within a session (not persisted across restarts —
// editor.default_view_mode controls the initial mode on app launch).

export type ViewMode = 'edit' | 'source'

function pageKey(notebook: string, section: string, page: string): string {
  return `${notebook}/${section}/${page}`
}

// Module-scoped reactive map: page locator → view mode.
const viewModes = $state<Map<string, ViewMode>>(new Map())

export function getViewMode(
  notebook: string,
  section: string,
  page: string
): ViewMode {
  return viewModes.get(pageKey(notebook, section, page)) || 'edit'
}

export function setViewMode(
  notebook: string,
  section: string,
  page: string,
  mode: ViewMode
): void {
  viewModes.set(pageKey(notebook, section, page), mode)
}

export function toggleViewMode(
  notebook: string,
  section: string,
  page: string
): void {
  const current = getViewMode(notebook, section, page)
  setViewMode(notebook, section, page, current === 'edit' ? 'source' : 'edit')
}
