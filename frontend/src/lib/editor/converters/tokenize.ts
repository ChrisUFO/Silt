// Tokenize stage — recursive-descent grammar producing the typed Token[]
// representation. The MARK_PATTERNS dispatch order (code-first,
// longer-delims-before-shorter) is the canonical grammar contract pinned
// byte-for-byte by the round-trip test suite.

import { validateTokens, flattenFlat, isSafeLinkHref } from './validate'

// ---- Typed Token model (#198) ----------------------------------------------
// Discriminated union covering every inline token the editor's grammar
// recognizes. `MarkToken` recursively carries children so nested marks
// (e.g. ***bold+italic***) produce a faithful tree rather than an opener
// chain. This is the canonical in-memory representation for the inline
// grammar. NodeJSON[] is a downstream adapter for ProseMirror compatibility.

export type MarkRef = { type: string; attrs?: Record<string, unknown> }

export type TextToken = {
  kind: 'text'
  text: string
  marks: MarkRef[]
}
export type MarkToken = {
  kind: 'mark'
  markType: string
  attrs?: Record<string, unknown>
  children: Token[]
}
export type EmbedToken = {
  kind: 'embed'
  uuid: string
}
export type BlockReferenceToken = {
  kind: 'blockReference'
  uuid: string
}
export type MentionToken = {
  kind: 'mention'
  name: string
}
export type Token =
  | TextToken
  | MarkToken
  | EmbedToken
  | BlockReferenceToken
  | MentionToken

// ---- Tokenize stage: recursive-descent parser ----------------------------

type MarkPattern = {
  type: string
  regex: RegExp
  shield?: boolean // if true, inner content is NOT recursively parsed (code)
  wordBoundary?: boolean // if true, only match at word boundaries (_, __)
  extractAttrs?: (m: RegExpExecArray) => Record<string, unknown> | null
  innerGroup?: number // capture group for inner content (default 1; color marks use 2)
}

// Ordered by priority: code first (shields), then longer delimiters before
// shorter (** before *, __ before _) to avoid false matches.
const MARK_PATTERNS: MarkPattern[] = [
  { type: 'code', regex: /`([^`]+)`/y, shield: true },
  {
    type: 'link',
    regex: /\[([^\]]*)\]\(([^)\s]*)\)/y,
    extractAttrs: (m) => (isSafeLinkHref(m[2]) ? { href: m[2] } : null)
  },
  { type: 'bold', regex: /\*\*(.+?)\*\*/y },
  { type: 'bold', regex: /__(.+?)__/y, wordBoundary: true },
  { type: 'italic', regex: /\*(.+?)\*/y },
  { type: 'italic', regex: /_(.+?)_/y, wordBoundary: true },
  { type: 'strike', regex: /~~(.+?)~~/y },
  { type: 'highlight', regex: /==(.+?)==/y },
  { type: 'underline', regex: /<u>(.+?)<\/u>/y },
  { type: 'subscript', regex: /<sub>(.+?)<\/sub>/y },
  { type: 'superscript', regex: /<sup>(.+?)<\/sup>/y },
  // Color spans (#170): [^>]* after the style attribute absorbs any extra
  // HTML attributes (e.g. onmouseover), which are then dropped on round-trip
  // since the serializer only emits <span style="color:X">.
  {
    type: 'textColor',
    regex: /<span style="color:\s*([^;"]+?)\s*;?"[^>]*>(.+?)<\/span>/y,
    innerGroup: 2,
    extractAttrs: (m) => ({ color: m[1].trim() })
  },
  {
    type: 'backgroundColor',
    regex:
      /<span style="background-color:\s*([^;"]+?)\s*;?"[^>]*>(.+?)<\/span>/y,
    innerGroup: 2,
    extractAttrs: (m) => ({ color: m[1].trim() })
  }
]

interface MarkMatch {
  type: string
  inner: string
  end: number
  shield: boolean
  attrs?: Record<string, unknown>
}

// Try to match any mark pattern at position `pos`. Returns the first match
// (priority order) or null.
function tryMatchMarkAt(text: string, pos: number): MarkMatch | null {
  for (const pattern of MARK_PATTERNS) {
    pattern.regex.lastIndex = pos
    const m = pattern.regex.exec(text)
    if (!m || m.index !== pos) continue
    if (pattern.wordBoundary) {
      const before = pos > 0 ? text[pos - 1] : ''
      const afterEnd = pos + m[0].length
      const after = afterEnd < text.length ? text[afterEnd] : ''
      if (/[a-zA-Z0-9]/.test(before) || /[a-zA-Z0-9]/.test(after)) continue
    }
    const attrs = pattern.extractAttrs?.(m)
    if (attrs === null) continue
    return {
      type: pattern.type,
      inner: m[pattern.innerGroup ?? 1],
      end: pos + m[0].length,
      shield: pattern.shield === true,
      attrs: attrs ?? undefined
    }
  }
  return null
}

// Recursively parse inline marks in `text`, returning a Token[] stream.
// `inheritedMarks` carries marks from OUTER mark-token levels only — the
// parser does NOT add the current match's mark to the recursion. A MarkToken
// represents its own mark via `markType`; the Token-to-NodeJSON adapter
// threads inherited marks onto descendants, so each mark appears in the
// NodeJSON output exactly once.
function parseInlineTokens(
  text: string,
  inheritedMarks: MarkRef[] = []
): Token[] {
  const tokens: Token[] = []
  let plain = ''
  let i = 0

  while (i < text.length) {
    const match = tryMatchMarkAt(text, i)
    if (match) {
      if (plain) {
        tokens.push({
          kind: 'text',
          text: plain,
          marks: [...inheritedMarks]
        })
        plain = ''
      }
      const newMark: MarkRef = { type: match.type }
      if (match.attrs) newMark.attrs = match.attrs
      if (match.shield) {
        // Code shields content: emit as-is with the new mark baked in (no
        // recursion — the contents are literal). The MarkToken would be
        // redundant for the shielded case, so emit as a TextToken with the
        // inherited + new mark.
        tokens.push({
          kind: 'text',
          text: match.inner,
          marks: [...inheritedMarks, newMark]
        })
      } else {
        tokens.push({
          kind: 'mark',
          markType: match.type,
          attrs: match.attrs,
          children: parseInlineTokens(match.inner, inheritedMarks)
        })
      }
      i = match.end
    } else {
      plain += text[i]
      i++
    }
  }
  if (plain) {
    tokens.push({
      kind: 'text',
      text: plain,
      marks: [...inheritedMarks]
    })
  }
  return tokens
}

// ---- Smart Graph + mention tokenization ---------------------------------

// Atomic inline-token regex (embed + block reference + @-mention). UUIDs:
// 8-4-4-4-12 hex. Mention names are any non-bracket, non-newline run inside
// `@[...]` (owner labels can contain spaces, e.g. "Ada Lovelace").
const ATOMIC_INLINE_TOKEN =
  /(\{\{embed:([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})\}\})|\(\(([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})\)\)|@\[([^\[\]\n]+)\]/gi

// Split clean_text on atomic inline tokens. Text segments are later parsed for
// inline marks; token segments are emitted as-is (#85 embed/block-ref, #184
// mention).
function splitAtomicTokens(text: string): Token[] {
  const tokens: Token[] = []
  let last = 0
  let match: RegExpExecArray | null
  ATOMIC_INLINE_TOKEN.lastIndex = 0
  while ((match = ATOMIC_INLINE_TOKEN.exec(text)) !== null) {
    if (match.index > last) {
      tokens.push({
        kind: 'text',
        text: text.slice(last, match.index),
        marks: []
      })
    }
    if (match[1]) {
      tokens.push({ kind: 'embed', uuid: match[2] })
    } else if (match[3]) {
      tokens.push({ kind: 'blockReference', uuid: match[3] })
    } else if (match[4]) {
      tokens.push({ kind: 'mention', name: match[4] })
    }
    last = match.index + match[0].length
  }
  if (last < text.length) {
    tokens.push({ kind: 'text', text: text.slice(last), marks: [] })
  }
  return tokens
}

// Public: tokenize a clean_text string into the typed Token[] representation.
// Runs the tokenize + validate stages of the inline pipeline. The serialize
// stage is exposed via serializeInlineContent(NodeJSON[]) for ProseMirror
// compatibility — callers that already work with Tokens can convert to
// NodeJSON[] via the helper below or use the legacy API directly.
export function tokenizeInline(text: string): Token[] {
  if (!text) return []
  const segments = splitAtomicTokens(text)
  const tokens: Token[] = []
  for (const seg of segments) {
    if (seg.kind === 'text') {
      tokens.push(...parseInlineTokens(seg.text))
    } else {
      tokens.push(seg)
    }
  }
  return flattenFlat(validateTokens(tokens))
}
