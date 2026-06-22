import type { NavigationTree } from './types'

/**
 * The active-navigation triple tracked by Sidebar.svelte. Reconciling decides
 * what these should become after a `ListNavigation` refresh — typically when
 * the user (or an external editor) has renamed, moved, or deleted the active
 * item.
 */
export interface ActiveNav {
  notebook: string
  section: string
  page: string
}

/**
 * Decide what the active navigation should become given the freshly-loaded
 * tree and the previously-active triple.
 *
 * Rules (mirrors the inline logic that used to live in Sidebar's `loadNavigation`):
 *  - Empty tree → leave current untouched (Sidebar keeps stale state until
 *    the next refresh; the IPC layer will eventually surface the empty tree).
 *  - Active notebook missing from the tree → fall back to the first notebook.
 *  - Active section missing from the (possibly new) active notebook → clear it.
 *  - Page is NOT reconciled here; page validity is checked at open time. This
 *    preserves the existing behaviour: a stale page string is harmless until
 *    the user navigates away from it.
 *
 * Pure: returns a new object, never mutates `current`.
 */
export function reconcileActive(
  tree: NavigationTree,
  current: ActiveNav
): ActiveNav {
  if (tree.notebooks.length === 0) {
    return { ...current }
  }

  // Pick a sensible active notebook if none selected or the current one is gone.
  let notebook = current.notebook
  if (!notebook || !tree.notebooks.some((nb) => nb.name === notebook)) {
    notebook = tree.notebooks[0].name
  }

  const nb = tree.notebooks.find((n) => n.name === notebook)
  if (!nb) {
    // The first-notebook fallback above guarantees `nb` is found; this branch
    // is defensive in case the tree mutates between the two lookups.
    return { notebook, section: '', page: '' }
  }

  let section = current.section
  if (section && !nb.sections.some((s) => s.name === section)) {
    section = ''
  }

  return { notebook, section, page: current.page }
}

/**
 * Produce "Untitled", "Untitled 2", "Untitled 3", … skipping any name that
 * already exists in the given section. Used by inline page-create so the user
 * can hit "+" repeatedly without typing.
 */
export function generateUniquePageName(
  tree: NavigationTree,
  activeNotebook: string,
  sectionName: string
): string {
  const base = 'Untitled'
  const nb = tree.notebooks.find((n) => n.name === activeNotebook)
  if (!nb) return base
  const sec = nb.sections.find((s) => s.name === sectionName)
  if (!sec) return base
  const existing = new Set(sec.pages.map((p) => p.name))
  if (!existing.has(base)) return base
  let i = 2
  while (existing.has(`${base} ${i}`)) i++
  return `${base} ${i}`
}
