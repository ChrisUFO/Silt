import { Extension } from '@tiptap/core'
import type { Editor } from '@tiptap/core'
import {
  SearchQuery,
  search as pmSearchPlugin,
  setSearchState,
  findNext as pmFindNext,
  findPrev as pmFindPrev,
  getMatchHighlights,
  getSearchState
} from 'prosemirror-search'

/**
 * In-page find (Ctrl+F) — issue #186. Wraps the official `prosemirror-search`
 * plugin (MIT, by the ProseMirror author) so Silt gets correct match
 * decoration, navigation, and replace commands without re-implementing text
 * scanning. The UI lives in `FindBar.svelte`; this extension owns the
 * ProseMirror integration + the query state + the scope filter.
 *
 * Scope filter: matches inside fenced `codeBlock`s, the inline `code` mark, or
 * the `link` mark (URLs) are ignored so a search for a prose term doesn't light
 * up inside code. Block-identity comments (`<!-- id: uuid -->`) are not in the
 * editor doc (the id is a node attr), so they need no filtering here.
 */

export interface SearchParams {
  search: string
  caseSensitive?: boolean
  wholeWord?: boolean
  regexp?: boolean
  replace?: string
}

/** Reject matches inside code blocks, inline code, or links. */
function scopeFilter(
  state: { doc: any },
  result: { from: number; to: number }
): boolean {
  const $from = state.doc.resolve(result.from)
  for (let depth = $from.depth; depth > 0; depth--) {
    if ($from.node(depth).type.name === 'codeBlock') return false
  }
  // Check marks at a position INSIDE the match range, not the boundary.
  // resolve(from).marks() at a text-node boundary associates with the
  // preceding node and can miss the mark that starts at `from`; resolving at
  // the last char of the match (to-1) is reliably inside the matched text.
  const inside = state.doc.resolve(result.to - 1).marks()
  const fromMarks = $from.marks()
  const blocked = (m: { type: { name: string } }) =>
    m.type.name === 'code' || m.type.name === 'link'
  if (inside.some(blocked) || fromMarks.some(blocked)) return false
  return true
}

export const Search = Extension.create({
  name: 'siltSearch',

  addStorage() {
    return {
      params: {
        search: '',
        caseSensitive: false,
        wholeWord: false,
        regexp: false,
        replace: ''
      } as SearchParams
    }
  },

  addProseMirrorPlugins() {
    // The plugin stores the current query + range and renders match decorations.
    // It starts with an empty query; setSearchQuery dispatches the real one.
    return [pmSearchPlugin()]
  },

  addCommands() {
    return {
      setSearchQuery:
        (params: SearchParams) =>
        ({ state, dispatch }: { state: any; dispatch?: (tr: any) => void }) => {
          this.storage.params = params
          const query = new SearchQuery({
            search: params.search,
            caseSensitive: params.caseSensitive ?? false,
            wholeWord: params.wholeWord ?? false,
            regexp: params.regexp ?? false,
            replace: params.replace ?? '',
            // The filter runs for every candidate match; keep it cheap.
            filter: scopeFilter as (
              s: any,
              r: { from: number; to: number }
            ) => boolean
          })
          const tr = setSearchState(state.tr, query)
          if (dispatch) dispatch(tr)
          return true
        },
      findNextInPage:
        () =>
        ({ state, dispatch }: { state: any; dispatch?: (tr: any) => void }) =>
          pmFindNext(state, dispatch),
      findPrevInPage:
        () =>
        ({ state, dispatch }: { state: any; dispatch?: (tr: any) => void }) =>
          pmFindPrev(state, dispatch)
    }
  }
})

/** Total highlighted matches in the current doc (0 when the bar is closed/empty). */
export function getMatchCount(editor: Editor): number {
  const state = editor.state
  if (!getSearchState(state)) return 0
  const set = getMatchHighlights(state)
  if (!set || set === null) return 0
  return set.find(0, state.doc.content.size).length
}

/**
 * The 0-based index of the match the selection is currently on, or -1 if the
 * selection isn't on a match. ProseMirror's `findNext`/`findPrev` select the
 * match (selection.from/to == match from/to), so we look for an exact range
 * match first, then a containing match.
 */
export function getActiveMatchIndex(editor: Editor): number {
  const state = editor.state
  if (!getSearchState(state)) return -1
  const set = getMatchHighlights(state)
  if (!set) return -1
  const decos = set.find(0, state.doc.content.size)
  const { from, to } = state.selection
  const exact = decos.findIndex(
    (d: { from: number; to: number }) => d.from === from && d.to === to
  )
  if (exact >= 0) return exact
  const containing = decos.findIndex(
    (d: { from: number; to: number }) => d.from <= from && d.to >= to
  )
  return containing
}

/** Clear the query (called when the find bar closes so decorations disappear). */
export function clearSearch(editor: Editor): void {
  editor.commands.setSearchQuery({
    search: '',
    caseSensitive: false,
    wholeWord: false,
    regexp: false,
    replace: ''
  })
}

declare module '@tiptap/core' {
  interface Commands<ReturnType> {
    siltSearch: {
      setSearchQuery: (params: SearchParams) => ReturnType
      findNextInPage: () => ReturnType
      findPrevInPage: () => ReturnType
    }
  }
}
