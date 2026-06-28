// MentionSuggest — @-trigger owner typeahead (#184).
//
// When the user types `@` in a prose block, this extension opens a popup of
// known task owners (the read-only DistinctOwners index projection). The user
// filters by typing and commits a selection, which replaces the `@query` text
// with an atomic MentionNode (`@[name]`) that round-trips through markdown.
//
// Like TaskMetaSuggest, this is a self-contained TipTap Extension built on
// `Extension.create` + a ProseMirror plugin (no `@tiptap/suggestion`
// dependency — the in-repo convention). The plugin recomputes the active
// "@-context" on every transaction and notifies the host through `onChange` so
// it can render the popup; keyboard nav (↑/↓/Enter/Escape) is handled via
// `addKeyboardShortcuts` and forwarded to the host.
//
// Detection/insertion/filter logic is exposed as pure functions so it is unit-
// testable without the rendered webview (per AGENTS.md: no Playwright).

import { Extension } from '@tiptap/core'
import type { Editor } from '@tiptap/core'
import { Plugin, PluginKey, TextSelection } from '@tiptap/pm/state'
import type { EditorState, Selection } from '@tiptap/pm/state'

// Owner names may contain letters/digits, spaces, apostrophes, hyphens, dots
// (e.g. "Ada Lovelace", "O'Brien", "J. Doe"). Any other char ends the query.
const QUERY_RE = /^[\p{L}\p{N} .'_-]*$/u

export interface MentionContext {
  // Doc position of the `@` trigger character.
  triggerPos: number
  // Text typed after `@`.
  query: string
  // Doc range covering `@` + query (replaced on commit).
  from: number
  // Cursor position (end of the range).
  to: number
}

// True when the selection resolves inside a fenced code block — mentions must
// not trigger there (the source is literal).
function inCodeBlock(selection: Selection): boolean {
  const $from = selection.$from
  for (let d = $from.depth; d >= 1; d--) {
    if ($from.node(d).type.name === 'codeBlock') return true
  }
  return false
}

// True when the caret sits inside an inline `code` mark (` `…` `) — mentions
// must not trigger there either, or committing would replace text inside the
// code span with a chip and corrupt it.
function inCodeMark(selection: Selection): boolean {
  return selection.$from.marks().some((m) => m.type.name === 'code')
}

export function getMentionContextAt(
  selection: Selection
): MentionContext | null {
  // Only a collapsed caret qualifies — never trigger over a selection.
  if (selection.from !== selection.to) return null
  if (inCodeBlock(selection)) return null
  if (inCodeMark(selection)) return null

  const $from = selection.$from
  const textBefore = $from.parent.textContent.slice(0, $from.parentOffset)
  const at = textBefore.lastIndexOf('@')
  if (at === -1) return null

  // Avoid email-style false triggers: `@` must start the line or follow a
  // space/punctuation, not a letter/digit ("foo@bar" is not a mention).
  const prev = at > 0 ? textBefore[at - 1] : ''
  if (/[A-Za-z0-9]/.test(prev)) return null

  const query = textBefore.slice(at + 1)
  if (!QUERY_RE.test(query)) return null

  const blockStart = $from.start()
  return {
    triggerPos: blockStart + at,
    query,
    from: blockStart + at,
    to: $from.pos
  }
}

export function getMentionContext(state: EditorState): MentionContext | null {
  return getMentionContextAt(state.selection)
}

// Filter the distinct-owner set by the typed query (case-insensitive substring
// so partial matches work mid-name). An empty query returns the full list — the
// user sees every known owner the moment `@` is typed.
export function filterOwners(
  owners: readonly string[],
  query: string
): string[] {
  const q = query.trim().toLowerCase()
  if (!q) return owners.slice()
  return owners.filter((o) => o.toLowerCase().includes(q))
}

// Replace the active `@query` with an atomic MentionNode and place the caret
// after it. Returns false (and changes nothing) when no context is active.
export function applyMentionSuggestion(editor: Editor, name: string): boolean {
  const ctx = getMentionContext(editor.state)
  if (!ctx) return false
  const mentionType = editor.schema.nodes.mentionNode
  if (!mentionType) return false

  const node = mentionType.create({ name })
  const tr = editor.state.tr.delete(ctx.from, ctx.to).insert(ctx.from, node)
  let after = ctx.from + node.nodeSize
  // Preserve a separating space when the query ended with whitespace, so
  // chaining `@alice @bob` doesn't collapse the two chips together.
  if (/\s$/.test(ctx.query)) {
    const space = editor.state.schema.text(' ')
    tr.insert(after, space)
    after += space.nodeSize
  }
  tr.setSelection(TextSelection.create(tr.doc, after, after))
  editor.view.dispatch(tr)
  return true
}

// ---- Extension / plugin --------------------------------------------------

interface MentionSuggestState {
  context: MentionContext | null
  // When true the popup is suppressed (e.g. after Escape) until the next edit.
  suppressed: boolean
}

const mentionSuggestKey = new PluginKey<MentionSuggestState>(
  'siltMentionSuggest'
)

export interface MentionSuggestOptions {
  // Live owner source: the host feeds the DistinctOwners index projection so
  // the plugin stays free of IPC coupling and pure-testable.
  owners: () => readonly string[]
  onChange: (ctx: MentionContext | null) => void
  onNavigate: (direction: 1 | -1) => void
  onSelectActive: () => void
}

export const MentionSuggest = Extension.create<MentionSuggestOptions>({
  name: 'siltMentionSuggest',

  addOptions() {
    return {
      owners: () => [],
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
      new Plugin<MentionSuggestState>({
        key: mentionSuggestKey,
        state: {
          init() {
            return { context: null, suppressed: false }
          },
          apply(tr, old, _oldState, newState) {
            const escape =
              (
                tr.getMeta(mentionSuggestKey) as
                  | { escape?: boolean }
                  | undefined
              )?.escape === true
            let suppressed = old.suppressed
            if (tr.docChanged) suppressed = false
            if (escape) suppressed = true
            const context = suppressed
              ? null
              : getMentionContextAt(newState.selection)
            return { context, suppressed }
          }
        },
        view() {
          return {
            update(view) {
              const ctx =
                mentionSuggestKey.getState(view.state)?.context ?? null
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

  // These shortcuts only act while an @-context is active; otherwise they fall
  // through (`false`) so the rest of the editor behaves normally. Registered
  // alongside TaskMetaSuggest — the two contexts are mutually exclusive.
  addKeyboardShortcuts() {
    const editor = this.editor
    const opts = this.options

    const active = () => getMentionContext(editor.state) !== null
    const popupActionable = () => {
      const ctx = getMentionContext(editor.state)
      if (!ctx) return false
      return filterOwners(opts.owners(), ctx.query).length > 0
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
        const tr = editor.state.tr.setMeta(mentionSuggestKey, { escape: true })
        editor.view.dispatch(tr)
        return true
      },
      Tab: () => {
        // Dismiss the popup on Tab (Tab then indents as usual) so it doesn't
        // linger after the user moves on. Returns false so Tab's default runs.
        if (!active()) return false
        editor.view.dispatch(
          editor.state.tr.setMeta(mentionSuggestKey, { escape: true })
        )
        return false
      }
    }
  }
})
