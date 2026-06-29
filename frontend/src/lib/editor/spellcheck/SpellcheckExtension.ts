import { Extension } from '@tiptap/core'
import type { Editor } from '@tiptap/core'
import { Plugin, PluginKey } from '@tiptap/pm/state'
import { Decoration, DecorationSet } from '@tiptap/pm/view'
import { checkWord } from './dictionary'

/**
 * Inline spellcheck (#196) — a ProseMirror decoration plugin that underlines
 * misspelled words. Runs typo-js (via dictionary.ts) over text nodes only,
 * skipping fenced code blocks, inline code, links, atomic nodes (embeds/
 * refs/mentions/math), and Dataview `[key:: value]` tokens. camelCase
 * identifiers and ALLCAPS acronyms are skipped to cut false positives.
 *
 * The decoration set is rebuilt on a 300 ms debounce after a doc change (so
 * fast typing doesn't re-check the whole doc per keystroke) and on demand via
 * requestSpellcheckRecheck (called after the dictionary finishes loading and
 * after the custom-word set changes). Word results are cached in dictionary.ts
 * so unchanged tokens skip Hunspell entirely.
 */

const key = new PluginKey('siltSpellcheck')
const RECHECK_META = 'siltSpellcheckRecheck'
const DEBOUNCE_MS = 300

/** Word tokens: letters (incl. accented) + apostrophe contractions. */
const WORD_RE = /[A-Za-z\u00C0-\u024F]+(?:[''\u2019][A-Za-z]+)*/g

/** Dataview inline-metadata token `[key:: value]` — its range is skipped. */
const DATAVIEW_RE = /\[[\w]+::\s*[^\]]*\]/g

/**
 * Whether a candidate token should be spell-checked. Filters out:
 * - ALLCAPS acronyms (≥2 letters, all upper with ≥1 A-Z): JSON, API, URL.
 * - camelCase identifiers (a lowercase letter followed by an upper inside):
 *   getTypeName, iPad. These are code/identifiers, rarely dictionary words.
 */
function shouldCheck(word: string): boolean {
  if (word.length < 2) return false
  if (word === word.toUpperCase() && /[A-Z]/.test(word)) return false
  if (/[a-z][A-Z]/.test(word)) return false
  return true
}

/** True if any mark on the node is inline code or a link. Uses the text node's
 *  own `.marks` (authoritative) rather than resolvedPos.marks(), which at a
 *  text-node boundary associates with the preceding node and misses the mark. */
function hasCodeOrLinkMark(node: any): boolean {
  return node.marks.some(
    (m: { type: { name: string } }) =>
      m.type.name === 'code' || m.type.name === 'link'
  )
}

/** True if `pos` sits inside a fenced code block (an ancestor node check —
 *  code-block content carries no inline code mark). */
function isInsideCodeBlock(doc: any, pos: number): boolean {
  const $pos = doc.resolve(pos)
  for (let depth = $pos.depth; depth > 0; depth--) {
    if ($pos.node(depth).type.name === 'codeBlock') return true
  }
  return false
}

/** Build the full set of misspelling decorations for a doc. */
function buildDecorations(doc: any): Decoration[] {
  const decos: Decoration[] = []
  doc.descendants((node: any, pos: number) => {
    // Text nodes must be handled first — ProseMirror marks text nodes
    // isAtom=true too, so the atomic-node skip below would otherwise drop
    // them. Process text; skip atomic non-text nodes (embeds/refs/mentions/
    // math) without descending; descend into containers.
    if (node.isText) {
      const text: string = node.text ?? ''
      if (text && !hasCodeOrLinkMark(node) && !isInsideCodeBlock(doc, pos)) {
        // Dataview `[key:: value]` tokens inside the prose are skipped at the
        // token-range level (a node may mix prose + metadata), not whole-node.
        const skipRanges: Array<[number, number]> = []
        DATAVIEW_RE.lastIndex = 0
        let dm: RegExpExecArray | null
        while ((dm = DATAVIEW_RE.exec(text)) !== null) {
          skipRanges.push([dm.index, dm.index + dm[0].length])
        }
        const inSkipRange = (idx: number, len: number) =>
          skipRanges.some(([a, b]) => idx < b && idx + len > a)
        WORD_RE.lastIndex = 0
        let m: RegExpExecArray | null
        while ((m = WORD_RE.exec(text)) !== null) {
          const word = m[0]
          if (inSkipRange(m.index, word.length)) continue
          if (shouldCheck(word) && !checkWord(word)) {
            decos.push(
              Decoration.inline(pos + m.index, pos + m.index + word.length, {
                class: 'silt-spell-error'
              })
            )
          }
        }
      }
      return false
    }
    if (node.isAtom) return false
    return true
  })
  return decos
}

export const Spellcheck = Extension.create({
  name: 'siltSpellcheck',

  addProseMirrorPlugins() {
    let timer: ReturnType<typeof setTimeout> | null = null
    return [
      new Plugin({
        key,
        state: {
          init(_: any, state: any) {
            return DecorationSet.create(state.doc, buildDecorations(state.doc))
          },
          apply(tr: any, prev: DecorationSet, _oldState: any, newState: any) {
            if (tr.getMeta(RECHECK_META)) {
              return DecorationSet.create(
                newState.doc,
                buildDecorations(newState.doc)
              )
            }
            if (tr.docChanged) {
              // Map existing decos through the change so they track edits until
              // the debounced rebuild catches newly-typed/corrected words.
              return prev.map(tr.mapping, tr.doc)
            }
            return prev
          }
        },
        props: {
          decorations(state: any) {
            return key.getState(state) as DecorationSet
          }
        },
        view(view: any) {
          return {
            update(v: any, prevState: any) {
              // Only schedule a recheck when the DOC changed (not selection-only).
              if (v.state.doc.eq(prevState.doc)) return
              if (timer) clearTimeout(timer)
              timer = setTimeout(() => {
                v.dispatch(v.state.tr.setMeta(RECHECK_META, true))
              }, DEBOUNCE_MS)
            },
            destroy() {
              if (timer) clearTimeout(timer)
            }
          }
        }
      })
    ]
  }
})

/**
 * Force a full decoration rebuild on the next tick — call after the dictionary
 * finishes loading (so the empty-dict window stops) and after the custom-word
 * set changes (so a just-added word un-flags immediately).
 */
export function requestSpellcheckRecheck(editor: Editor): void {
  editor.view.dispatch(editor.state.tr.setMeta(RECHECK_META, true))
}

export interface Misspelling {
  word: string
  from: number
  to: number
}

/** The misspelling decoration whose range contains `pos`, or null. */
export function findMisspellingAt(
  editor: Editor,
  pos: number
): Misspelling | null {
  const set = key.getState(editor.state) as DecorationSet | undefined
  if (!set) return null
  const decos = set.find(0, editor.state.doc.content.size)
  const d = decos.find(
    (deco: { from: number; to: number }) => deco.from <= pos && deco.to >= pos
  )
  if (!d) return null
  return {
    word: editor.state.doc.textBetween(d.from, d.to, ''),
    from: d.from,
    to: d.to
  }
}

/** The first misspelling at/after `pos`, wrapping to the first in the doc. */
export function findMisspellingAtOrAfter(
  editor: Editor,
  pos: number
): Misspelling | null {
  const set = key.getState(editor.state) as DecorationSet | undefined
  if (!set) return null
  const decos = set.find(0, editor.state.doc.content.size)
  const d = decos.find((deco: { from: number }) => deco.from >= pos) ?? decos[0]
  if (!d) return null
  return {
    word: editor.state.doc.textBetween(d.from, d.to, ''),
    from: d.from,
    to: d.to
  }
}
