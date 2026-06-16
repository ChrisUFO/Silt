// TaskMetaSuggest — metadata-key autocomplete for task lines.
//
// When the user types `%` inside a taskBlock (lines rendered from
// `- [ ]`, `- [/]`, `- [x]`), this extension opens a popup listing the
// metadata keys Silt understands. The user filters by typing and commits a
// selection, which replaces the `%query` text with an inline metadata
// snippet (`[key:: ]`, cursor placed for value entry; `[pin:: true]` for the
// boolean pin key).
//
// `@tiptap/suggestion` is not a dependency, so this is a self-contained
// TipTap Extension built on `Extension.create` + a ProseMirror plugin. The
// plugin recomputes the active "suggest context" on every transaction and
// notifies the host component through `onChange` so it can render the popup.
// Keyboard navigation (↑/↓/Enter/Escape) is handled via `addKeyboardShortcuts`
// and forwarded to `onNavigate` / `onSelectActive`.
//
// All detection/insertion logic is exposed as pure functions so it can be
// unit-tested without driving the rendered webview (per AGENTS.md: no
// Playwright; test the contract, not the DOM).

import { Extension } from '@tiptap/core'
import type { Editor } from '@tiptap/core'
import { Plugin, PluginKey, TextSelection } from '@tiptap/pm/state'
import type { EditorState, Selection } from '@tiptap/pm/state'

// ---- Metadata catalog ----------------------------------------------------

export interface MetaKey {
  key: string
  label: string
  description: string
}

// The six metadata keys Silt recognises inline. Order is the display order
// shown when `%` is typed with no filter.
export const META_KEYS: readonly MetaKey[] = [
  { key: 'due', label: 'due', description: 'Due date' },
  { key: 'start', label: 'start', description: 'Start date' },
  { key: 'owner', label: 'owner', description: 'Owner / assignee' },
  {
    key: 'priority',
    label: 'priority',
    description: 'Priority (1=critical, 2=normal, 3=low)'
  },
  { key: 'pin', label: 'pin', description: 'Pinned task' },
  { key: 'progress', label: 'progress', description: 'Progress (0-100)' }
]

// Filter the catalog by a prefix query (case-insensitive). An empty query
// returns the full list — the user sees every option the moment `%` is typed.
export function filterMetaKeys(query: string): MetaKey[] {
  const q = query.toLowerCase()
  if (!q) return META_KEYS.slice()
  return META_KEYS.filter((m) => m.key.toLowerCase().startsWith(q))
}

// ---- Detection -----------------------------------------------------------

export interface SuggestContext {
  // Doc position of the `%` trigger character.
  triggerPos: number
  // Text typed after `%` (letters only — any other char deactivates).
  query: string
  // Doc range covering `%` + query (the text replaced on commit).
  from: number
  // Cursor position (end of the range).
  to: number
}

// True when the current selection is inside a taskBlock node.
export function isInTaskBlock(state: EditorState): boolean {
  return selectionInTaskBlock(state.selection)
}

function selectionInTaskBlock(selection: Selection): boolean {
  const $from = selection.$from
  for (let d = $from.depth; d >= 1; d--) {
    if ($from.node(d).type.name === 'taskBlock') return true
  }
  return false
}

// Inspect an EditorState and return the active suggest context, or null when
// the popup should be hidden (non-task line, no `%`, expanded selection, or a
// query containing non-letter characters).
export function getSuggestContext(state: EditorState): SuggestContext | null {
  return getSuggestContextAt(state.selection)
}

// Selection-level variant used by the plugin (which has a Transaction, not a
// full EditorState, at apply time).
export function getSuggestContextAt(
  selection: Selection
): SuggestContext | null {
  // Only a collapsed caret qualifies — never trigger over a selection.
  if (selection.from !== selection.to) return null
  if (!selectionInTaskBlock(selection)) return null

  const $from = selection.$from
  const textBefore = $from.parent.textContent.slice(0, $from.parentOffset)
  const pct = textBefore.lastIndexOf('%')
  if (pct === -1) return null

  const query = textBefore.slice(pct + 1)
  // The query must be empty or all ASCII letters. A space, digit, or symbol
  // after `%` means the user is not invoking the suggester.
  if (!/^[a-zA-Z]*$/.test(query)) return null

  const blockStart = $from.start()
  return {
    triggerPos: blockStart + pct,
    query,
    from: blockStart + pct,
    to: $from.pos
  }
}

// ---- Insertion -----------------------------------------------------------

export interface InsertPlan {
  text: string
  // Caret offset relative to `from` after inserting `text`, or null to leave
  // the caret at the end of the inserted text.
  cursorOffset: number | null
}

// Build the inline snippet for a metadata key. `pin` is a boolean and is
// auto-filled; every other key opens an empty value slot with the caret
// positioned inside the brackets for immediate typing.
export function buildInsertPlan(key: string): InsertPlan {
  if (key === 'pin') return { text: '[pin:: true]', cursorOffset: null }
  const text = `[${key}:: ]`
  // Place the caret just before the closing `]` so typing fills the value:
  //   `[due:: |]` -> type "tomorrow" -> `[due:: tomorrow]`
  return { text, cursorOffset: text.length - 1 }
}

// Replace the active `%query` with the key's snippet and position the caret.
// Returns false (and changes nothing) when no suggest context is active.
export function applyMetaSuggestion(editor: Editor, key: string): boolean {
  const ctx = getSuggestContext(editor.state)
  if (!ctx) return false

  const plan = buildInsertPlan(key)
  const tr = editor.state.tr
    .delete(ctx.from, ctx.to)
    .insertText(plan.text, ctx.from)

  if (plan.cursorOffset !== null) {
    const caret = ctx.from + plan.cursorOffset
    tr.setSelection(TextSelection.create(tr.doc, caret, caret))
  }
  editor.view.dispatch(tr)
  return true
}

// ---- Extension / plugin --------------------------------------------------

interface MetaSuggestState {
  context: SuggestContext | null
  // When true the popup is suppressed (e.g. after Escape) until the next
  // document edit clears it.
  suppressed: boolean
}

const metaSuggestKey = new PluginKey<MetaSuggestState>('siltMetaSuggest')

export interface TaskMetaSuggestOptions {
  // Fired whenever the active context changes (open, close, reposition,
  // filter). The host renders/hides its popup from this callback.
  onChange: (ctx: SuggestContext | null) => void
  // Fired on ↑/↓ while the popup is open.
  onNavigate: (direction: 1 | -1) => void
  // Fired on Enter while the popup is open — the host resolves the currently
  // highlighted item and calls applyMetaSuggestion itself.
  onSelectActive: () => void
}

export function getMetaSuggestState(editor: Editor): MetaSuggestState {
  return (
    metaSuggestKey.getState(editor.state) ?? {
      context: null,
      suppressed: false
    }
  )
}

export const TaskMetaSuggest = Extension.create<TaskMetaSuggestOptions>({
  name: 'siltMetaSuggest',

  addOptions() {
    return {
      onChange: () => {},
      onNavigate: () => {},
      onSelectActive: () => {}
    }
  },

  addProseMirrorPlugins() {
    const onChange = this.options.onChange
    // Track the last signature we notified so onChange only fires on real
    // changes (avoids redundant popup re-renders on no-op transactions).
    let lastSig = ''

    return [
      new Plugin<MetaSuggestState>({
        key: metaSuggestKey,
        state: {
          init() {
            return { context: null, suppressed: false }
          },
          apply(tr, old, _oldState, newState) {
            const escape =
              (tr.getMeta(metaSuggestKey) as { escape?: boolean } | undefined)
                ?.escape === true
            // Any real edit clears suppression so typing reopens the popup.
            let suppressed = old.suppressed
            if (tr.docChanged) suppressed = false
            if (escape) suppressed = true

            const context = suppressed
              ? null
              : getSuggestContextAt(newState.selection)
            return { context, suppressed }
          }
        },
        view() {
          return {
            update(view) {
              const ctx = metaSuggestKey.getState(view.state)?.context ?? null
              const sig = ctx ? `${ctx.from}|${ctx.query}` : ''
              if (sig !== lastSig) {
                lastSig = sig
                onChange(ctx)
              }
            }
          }
        }
      })
    ]
  },

  // These shortcuts only act while a suggest context is active; otherwise they
  // fall through (`false`) so the rest of the editor behaves normally. The
  // extension is registered before SiltBlockKeymaps so Enter is intercepted
  // here instead of creating a new block.
  addKeyboardShortcuts() {
    const editor = this.editor
    const opts = this.options

    const active = () => getSuggestContext(editor.state) !== null
    // The popup is "actionable" only when the suggest context is active AND
    // the filtered catalog has at least one item. When the query matches no
    // key (e.g. `%xyz`), the host hides the popup — Enter/Arrow keys must
    // fall through to the editor's default behavior instead of being
    // swallowed. Escape can keep gating on active() alone because
    // suppressing an empty popup is a harmless no-op.
    const popupActionable = () => {
      const ctx = getSuggestContext(editor.state)
      if (!ctx) return false
      return filterMetaKeys(ctx.query).length > 0
    }

    return {
      ArrowUp: () => {
        if (!popupActionable()) return false
        opts.onNavigate(-1)
        return true
      },
      ArrowDown: () => {
        if (!popupActionable()) return false
        opts.onNavigate(1)
        return true
      },
      Enter: () => {
        if (!popupActionable()) return false
        opts.onSelectActive()
        return true
      },
      Escape: () => {
        if (!active()) return false
        // Suppress the popup until the next edit; the context is still `%…`
        // in the doc, so we hide it via plugin metadata.
        const tr = editor.state.tr.setMeta(metaSuggestKey, { escape: true })
        editor.view.dispatch(tr)
        return true
      }
    }
  }
})
