/**
 * Find-bar open/close state for in-page find (#186). Held in a module-scope
 * reactive store (Svelte 5 runes) so App.svelte's global Ctrl+F handler and
 * the FindBar component share one source of truth without prop-drilling.
 *
 * The active editor instance is per-tab; FindBar receives it as a prop from
 * App.svelte (which tracks the active tab's editor). Match counts + active
 * index are derived FROM the editor (see searchExtension.getMatchCount), not
 * stored here — they're a projection of the editor's decoration set.
 */

let open = $state(false)
let replaceOpen = $state(false)

export const findBarState = {
  get open() {
    return open
  },
  get replaceOpen() {
    return replaceOpen
  },
  /** Ctrl+F: open the find bar (find-only). */
  openFind() {
    open = true
    replaceOpen = false
  },
  /** Ctrl+H: open with the replace row visible. */
  openReplace() {
    open = true
    replaceOpen = true
  },
  /** Esc: close the bar. The caller (FindBar) clears the search query on close
   *  so decorations disappear and the editor regains its normal selection. */
  close() {
    open = false
    replaceOpen = false
  }
}
