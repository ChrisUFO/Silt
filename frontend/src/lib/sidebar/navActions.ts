import type { NavigationTree, NavNotebook } from './types'

/**
 * Helpers for Sidebar create/rename/delete actions.
 *
 * The IPC orchestration itself (await `RenameNotebook(...)`) lives in the
 * component — it's already a thin wrapper. What IS worth extracting is the
 * pure decision logic that those wrappers consume:
 *  - linked-vs-vault notebook detection (used by delete)
 *  - the human-readable delete-target label
 *  - the "what should active triple become after a delete" reconcile
 */

export type DeleteLevel = 'notebook' | 'section' | 'page'

export interface DeleteTarget {
  level: DeleteLevel
  notebook: string
  section?: string
  page?: string
}

/**
 * Returns the linked-id (`linked:<id>` → `<id>`) if the notebook is a linked
 * external mount (#100), or `null` for a vault notebook. Used by delete to
 * decide `UnlinkNotebook` (files untouched) vs `DeleteNotebook` (trash).
 *
 * Previously duplicated in Sidebar.svelte's `deleteTargetLinked` derived and
 * `confirmDelete`'s notebook branch.
 */
export function linkedNotebookId(nb: NavNotebook | undefined): string | null {
  if (!nb?.source) return null
  const prefix = 'linked:'
  if (!nb.source.startsWith(prefix)) return null
  const id = nb.source.slice(prefix.length)
  return id || null
}

/** Convenience predicate form of {@link linkedNotebookId}. */
export function isLinkedNotebook(nb: NavNotebook | undefined): boolean {
  return linkedNotebookId(nb) !== null
}

/**
 * Build the human-readable label shown in the delete-confirmation dialog.
 * Pure string assembly — pulled out so the label format is testable and
 * consistent across the context-menu and any future delete entry point.
 */
export function deleteTargetLabel(target: DeleteTarget): string {
  const { level, notebook, section, page } = target
  if (level === 'page' && page) return `page "${page}"`
  if (level === 'section' && section)
    return `section "${section}" and all its pages`
  return `notebook "${notebook}" and all its content`
}

/**
 * Decide what the active navigation triple should become after a delete.
 *
 * Rules (mirrors the inline logic that used to live in Sidebar's confirmDelete):
 *  - If the deleted notebook WAS the active one → clear all three (Sidebar's
 *    next loadNavigation will pick a sensible fallback).
 *  - If the deleted section WAS the active one → clear section + page.
 *  - If the deleted page WAS the active one → clear page only.
 *  - Otherwise → unchanged.
 *
 * Pure: returns a new object, never mutates `current`.
 */
export interface ActiveTriple {
  notebook: string
  section: string
  page: string
}

export function reconcileActiveAfterDelete(
  target: DeleteTarget,
  current: ActiveTriple
): ActiveTriple {
  if (target.level === 'notebook' && current.notebook === target.notebook) {
    return { notebook: '', section: '', page: '' }
  }
  if (target.level === 'section' && current.section === target.section) {
    return { ...current, section: '', page: '' }
  }
  if (target.level === 'page' && current.page === target.page) {
    return { ...current, page: '' }
  }
  return { ...current }
}

/**
 * Find a notebook by name. Pure wrapper for `tree.notebooks.find(...)` used
 * by delete to resolve the source for the linked-vs-vault check. Returns
 * `undefined` if not found.
 */
export function findNotebook(
  tree: NavigationTree,
  name: string
): NavNotebook | undefined {
  return tree.notebooks.find((n) => n.name === name)
}
