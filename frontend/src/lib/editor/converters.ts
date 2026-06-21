// Pure converters between parser.ParsedBlock (the Wails IPC JSON shape) and
// ProseMirror / TipTap doc JSON. The bridge between Silt's block data model
// and the editor. No side effects, no editor dependency — fully unit-testable
// in isolation (converters.test.ts).
//
// Contract:
//   blocksToDoc(blocks) -> doc JSON      (used on load / setContent)
//   docToBlocks(doc)    -> blocks        (used on save, debounced)
//
// Round-trip identity holds for the semantic fields:
//   docToBlocks(blocksToDoc(blocks)) preserves id, type, depth, status, owner,
//   start_date, due_date, priority, clean_text. raw_text is a serialization
//   artifact (produced by Go's RenderFileContent from clean_text + attrs), so
//   it is NOT compared in the identity tests. parent_id and line_number are
//   derived (from depth and doc order respectively), not stored as node attrs.
//
// Go's parser.RenderFileContent remains the single on-disk serializer (#40).
// The frontend never produces markdown.
//
// --- Inline grammar pipeline (#198) ---
// The inline pipeline is split into three discrete stages so each can be
// reasoned about, tested, and evolved independently:
//
//   tokenize(text) -> Token[]    — recursive-descent grammar, emits the typed
//                                  Token[] representation (TextToken, MarkToken,
//                                  EmbedToken, BlockReferenceToken).
//   validate(tokens) -> Token[]  — centralized security sanitization. This is
//                                  the ONLY place future mark sanitizers are
//                                  added (link-scheme allowlist, color-span
//                                  attribute stripping).
//   serialize(content) -> string — mark-diff serializer (legacy NodeJSON[]-
//                                  based, byte-for-byte proven across #168).
//                                  Public API for ProseMirror callers.
//
// The legacy NodeJSON[] surface (`{ type: 'text', text, marks }`) is preserved
// via a thin adapter so the ProseMirror integration is unchanged. New code
// that doesn't need NodeJSON should consume the typed Token API directly.

import type { ParsedBlock, DocJSON, NodeJSON, BlockType } from './types'

// Extract the bullet prefix ('- ', '* ', '+ ', or '') from a note's raw_text,
// matching the detection logic in Go's renderBlock (parser.go ~line 515-527).
function detectBullet(rawText: string): string {
  const trimmed = rawText.trimStart()
  if (trimmed.startsWith('- ')) return '- '
  if (trimmed.startsWith('* ')) return '* '
  if (trimmed.startsWith('+ ')) return '+ '
  const match = trimmed.match(/^(\d+[.)]\s)/)
  if (match) {
    return match[1]
  }
  return ''
}

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
export type Token = TextToken | MarkToken | EmbedToken | BlockReferenceToken

// ---- Security sanitization (validate stage) -------------------------------
// Allowlisted URL schemes for link hrefs (#168, centralized in #198). Mirrors
// TipTap's Link.isAllowedUri default. Non-allowlisted schemes (javascript:,
// data:, vbscript:, etc.) are dropped — the link text survives as literal
// text. This is the ONLY place future mark sanitizers are added.
const SAFE_LINK_SCHEMES = new Set([
  'http',
  'https',
  'ftp',
  'ftps',
  'mailto',
  'tel',
  'callto',
  'sms',
  'cid',
  'xmpp'
])

function isSafeLinkHref(href: string): boolean {
  if (!href) return false
  if (
    href.startsWith('#') ||
    href.startsWith('/') ||
    href.startsWith('./') ||
    href.startsWith('../')
  )
    return true
  const colonIdx = href.indexOf(':')
  if (colonIdx === -1) return true
  const scheme = href.slice(0, colonIdx).toLowerCase()
  return SAFE_LINK_SCHEMES.has(scheme)
}

// Validate stage: security-critical pass run after tokenize. Walks the
// Token[] tree and rewrites unsafe structures into their safe equivalents:
// - link with disallowed scheme → drop the mark (text survives)
// - color span with extra attrs (e.g. onmouseover) → already stripped at
//   tokenize time via the [^>]* regex absorption; this is the documented
//   last-line-of-defense contract.
function validateTokens(tokens: Token[]): Token[] {
  return tokens.map((t) => validateOne(t))
}

function validateOne(t: Token): Token {
  if (t.kind === 'mark') {
    if (t.markType === 'link') {
      const href = (t.attrs?.href as string) || ''
      if (!isSafeLinkHref(href)) {
        // Drop the link mark — children survive as plain tokens.
        return { kind: 'mark', markType: '__flat__', children: t.children }
      }
    }
    return { ...t, children: validateTokens(t.children) }
  }
  return t
}

// `__flat__` is the internal sentinel that means "drop the enclosing mark,
// keep the children inline". Flatten it into the parent stream — children
// inherit the parent token's surrounding marks via this splice.
function flattenFlat(tokens: Token[]): Token[] {
  const out: Token[] = []
  for (const t of tokens) {
    if (t.kind === 'mark' && t.markType === '__flat__') {
      out.push(...flattenFlat(t.children))
    } else if (t.kind === 'mark') {
      out.push({ ...t, children: flattenFlat(t.children) })
    } else {
      out.push(t)
    }
  }
  return out
}

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

// ---- Smart Graph tokenization --------------------------------------------

// Smart Graph token regex (embed + block reference). UUIDs: 8-4-4-4-12 hex.
const SMART_GRAPH_TOKEN =
  /(\{\{embed:([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})\}\})|\(\(([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})\)\)/gi

// Split clean_text on Smart Graph tokens. Text segments are later parsed for
// inline marks; token segments are emitted as-is (#85).
function splitSmartGraph(text: string): Token[] {
  const tokens: Token[] = []
  let last = 0
  let match: RegExpExecArray | null
  SMART_GRAPH_TOKEN.lastIndex = 0
  while ((match = SMART_GRAPH_TOKEN.exec(text)) !== null) {
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
  const segments = splitSmartGraph(text)
  const tokens: Token[] = []
  for (const seg of segments) {
    if (seg.kind === 'text') {
      tokens.push(...parseInlineTokens(seg.text))
    } else if (seg.kind === 'embed') {
      tokens.push(seg)
    } else {
      tokens.push(seg)
    }
  }
  return flattenFlat(validateTokens(tokens))
}

// ---- NodeJSON <-> Token adapters ------------------------------------------
// Thin adapters between the typed Token representation and the legacy
// NodeJSON[] surface used by ProseMirror / the legacy serializer. Both
// directions live here so the rest of the pipeline can speak Token.

function tokenToNodeJSON(t: Token, inheritedMarks: MarkRef[] = []): NodeJSON[] {
  switch (t.kind) {
    case 'text': {
      const marks = [...inheritedMarks, ...t.marks]
      return [
        {
          type: 'text',
          text: t.text,
          marks: marks.length ? marks : undefined
        }
      ]
    }
    case 'embed':
      return [{ type: 'embedNode', attrs: { uuid: t.uuid } }]
    case 'blockReference':
      return [{ type: 'blockReferenceNode', attrs: { uuid: t.uuid } }]
    case 'mark': {
      const own: MarkRef = {
        type: t.markType,
        ...(t.attrs ? { attrs: t.attrs } : {})
      }
      const newInherited = [...inheritedMarks, own]
      const out: NodeJSON[] = []
      for (const child of t.children) {
        out.push(...tokenToNodeJSON(child, newInherited))
      }
      return out
    }
  }
}

function tokensToNodeJSON(tokens: Token[]): NodeJSON[] {
  const out: NodeJSON[] = []
  for (const t of tokens) {
    out.push(...tokenToNodeJSON(t))
  }
  return out
}

// Legacy tokenize helper (returns NodeJSON[] for ProseMirror). Thin wrapper
// around the new typed pipeline.
function legacyTokenizeInline(text: string): NodeJSON[] {
  return tokensToNodeJSON(tokenizeInline(text))
}

// ---- Serialize stage: NodeJSON[] → markdown ------------------------------
// Mark-diff approach: walks nodes left-to-right, emitting open/close
// delimiters as the active mark set changes. This produces clean markdown
// even when marks span multiple adjacent text nodes. Smart Graph nodes
// close all active marks, emit their token, then resume. Replaces the old
// plain-concatenation inlineText (#168).

// The opener syntax for each mark type.
function markOpen(mark: MarkRef): string {
  switch (mark.type) {
    case 'bold':
      return '**'
    case 'italic':
      return '*'
    case 'strike':
      return '~~'
    case 'highlight':
      return '=='
    case 'code':
      return '`'
    case 'underline':
      return '<u>'
    case 'subscript':
      return '<sub>'
    case 'superscript':
      return '<sup>'
    case 'textColor': {
      const color = (mark.attrs as Record<string, unknown> | undefined)?.color
      return color ? `<span style="color: ${color}">` : ''
    }
    case 'backgroundColor': {
      const color = (mark.attrs as Record<string, unknown> | undefined)?.color
      return color ? `<span style="background-color: ${color}">` : ''
    }
    case 'link':
      return '['
    default:
      return ''
  }
}

function markClose(mark: MarkRef): string {
  switch (mark.type) {
    case 'bold':
      return '**'
    case 'italic':
      return '*'
    case 'strike':
      return '~~'
    case 'highlight':
      return '=='
    case 'code':
      return '`'
    case 'underline':
      return '</u>'
    case 'subscript':
      return '</sub>'
    case 'superscript':
      return '</sup>'
    case 'textColor':
    case 'backgroundColor':
      return '</span>'
    case 'link': {
      const href = (mark.attrs as Record<string, unknown> | undefined)
        ?.href as string
      return `](${isSafeLinkHref(href) ? href : ''})`
    }
    default:
      return ''
  }
}

// Serialize inline content to a markdown string using the mark-diff approach.
// Public API (preserved from #168) — NodeJSON[] is the legacy surface; new
// callers should prefer tokenizeInline + a NodeJSON adapter for symmetry.
export function serializeInlineContent(content?: NodeJSON[]): string {
  if (!content) return ''
  let result = ''
  let active: MarkRef[] = []

  function closeAll(): void {
    for (let i = active.length - 1; i >= 0; i--) {
      result += markClose(active[i])
    }
    active = []
  }

  for (const child of content) {
    if (child.text !== undefined) {
      const nodeMarks = child.marks || []
      // Longest common prefix between active and nodeMarks.
      let common = 0
      while (
        common < active.length &&
        common < nodeMarks.length &&
        active[common].type === nodeMarks[common].type
      ) {
        common++
      }
      // Close marks that diverge (reverse order).
      for (let i = active.length - 1; i >= common; i--) {
        result += markClose(active[i])
      }
      // Open new marks.
      for (let i = common; i < nodeMarks.length; i++) {
        result += markOpen(nodeMarks[i])
      }
      active = nodeMarks
      result += child.text
    } else if (child.type === 'embedNode') {
      closeAll()
      result += `{{embed:${(child.attrs?.uuid as string) || ''}}}`
    } else if (child.type === 'blockReferenceNode') {
      closeAll()
      result += `((${(child.attrs?.uuid as string) || ''}))`
    } else if (child.content) {
      closeAll()
      result += serializeInlineContent(child.content)
    }
  }
  closeAll()
  return result
}

// ---- Alignment marker helpers (#173) -------------------------------------
// Block-level alignment is persisted as a trailing HTML-comment marker in
// clean_text: `text <!-- silt-align: center -->`. The marker is invisible
// in the rendered editor and any markdown viewer, but present in the raw
// file. Default 'left' emits no marker. TASK blocks never emit a marker
// (alignment is not supported on tasks).

const ALIGN_MARKER_RE =
  /\s*<!-- silt-align: (left|center|right|justify) -->\s*$/

export function stripAlignmentMarker(cleanText: string): {
  body: string
  align: string
} {
  const m = cleanText.match(ALIGN_MARKER_RE)
  if (m) {
    return { body: cleanText.slice(0, m.index).trimEnd(), align: m[1] }
  }
  return { body: cleanText, align: 'left' }
}

export function emitAlignmentMarker(align: string): string {
  return align && align !== 'left' ? ` <!-- silt-align: ${align} -->` : ''
}

// Derive parent_id from block depths via the stack-walk algorithm used by the
// Go parser (parser.go activeIDs) and the legacy BlockRenderer (getUpdatedParentIDs).
// parent_id = the id of the nearest preceding block at depth-1.
function deriveParentIDs(blocks: ParsedBlock[]): void {
  const activeIDs: string[] = []
  for (const b of blocks) {
    if (b.depth > 0) {
      b.parent_id = b.depth - 1 < activeIDs.length ? activeIDs[b.depth - 1] : ''
    } else {
      b.parent_id = ''
    }
    while (activeIDs.length <= b.depth) activeIDs.push('')
    activeIDs[b.depth] = b.id
    activeIDs.splice(b.depth + 1)
  }
}

// Match a clean_text that is solely a {{embed:uuid}} token (the entire
// block body is one embed). Such blocks become top-level embedNode blocks
// (group: 'block') rather than inline children of a noteBlock (which only
// allows inline content).
const SOLE_EMBED_RE =
  /^\{\{embed:([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\}\}$/i

// Silt-embed marker: a NOTE block whose clean_text is `<!-- silt-embed: {json} -->`
// becomes a generic embedBlock node (#110). The JSON carries the embed attrs
// (embedType, src, caption, openable, pluginID). The Go parser preserves the
// marker as the NOTE's clean_text (it only strips the trailing id comment), so
// the on-disk file round-trips byte-for-byte.
const SOLE_EMBED_BLOCK_RE = /^<!-- silt-embed: (\{.*\}) -->$/

export function embedBlockMarker(attrs: {
  embedType: string
  src: string
  caption?: string
  openable?: boolean
  pluginID?: string
  // notebook is the host notebook the block lives in. The marker carries it so
  // OpenAttachment + the embedBlock NodeView can resolve the relative src path
  // when the user clicks the card, even if the user has navigated to a
  // different page in the meantime (the block is the only place that knows
  // where the attachment was copied to).
  notebook?: string
}): string {
  return `<!-- silt-embed: ${JSON.stringify(attrs)} -->`
}

export function parseEmbedBlockMarker(text: string): {
  embedType: string
  src: string
  caption?: string
  openable?: boolean
  pluginID?: string
  notebook?: string
} | null {
  const m = text.match(SOLE_EMBED_BLOCK_RE)
  if (!m) return null
  try {
    return JSON.parse(m[1])
  } catch {
    return null
  }
}

// Convert a single ParsedBlock to its ProseMirror node JSON.
function blockToNode(block: ParsedBlock): NodeJSON {
  const rawText = block.clean_text || ''

  // Strip alignment marker from clean_text (#173). The marker is a trailing
  // HTML comment preserved by the Go parser as part of the line body. Embed
  // blocks don't carry alignment, but we strip defensively so a stray marker
  // never leaks into visible text.
  const { body: text, align } = stripAlignmentMarker(rawText)

  // A NOTE whose entire body is a single {{embed:uuid}} token becomes a
  // top-level embedNode. Wrapping a block-level node inside noteBlock's
  // inline-only content would violate the ProseMirror schema (#85).
  const embedMatch = text.match(SOLE_EMBED_RE)
  if (embedMatch) {
    return {
      type: 'embedNode',
      attrs: { id: block.id, uuid: embedMatch[1] }
    }
  }

  // A NOTE whose entire body is a `<!-- silt-embed: {json} -->` marker becomes
  // a generic embedBlock node (#110). Plugins specialize it via attrs.
  const embedBlockAttrs = parseEmbedBlockMarker(text)
  if (embedBlockAttrs) {
    return {
      type: 'embedBlock',
      attrs: { id: block.id, ...embedBlockAttrs }
    }
  }

  const content: NodeJSON[] = text ? legacyTokenizeInline(text) : []

  switch (block.type) {
    case 'TASK':
      return {
        type: 'taskBlock',
        attrs: {
          id: block.id,
          depth: block.depth,
          status: block.status || 'TODO',
          owner: block.owner || '',
          start_date: block.start_date || '',
          due_date: block.due_date || '',
          priority: block.priority || 3,
          file_date: block.file_date || ''
        },
        content
      }
    case 'HEADER':
      return {
        type: 'headerBlock',
        attrs: {
          id: block.id,
          depth: block.depth || 1,
          align,
          file_date: block.file_date || ''
        },
        content
      }
    case 'NOTE':
    default:
      // Defensive: unknown block types map to NOTE so a malformed doc never
      // drops content. The Go side also treats unrecognized lines as notes.
      return {
        type: 'noteBlock',
        attrs: {
          id: block.id,
          depth: block.depth,
          bullet: detectBullet(block.raw_text),
          align,
          file_date: block.file_date || ''
        },
        content
      }
  }
}

// blocksToDoc converts an ordered list of ParsedBlocks into a ProseMirror doc
// JSON suitable for editor.commands.setContent(). Each block becomes one
// top-level block node; nesting is expressed via the depth attr (rendered as
// indentation by the editor surface + NodeViews).
export function blocksToDoc(blocks: ParsedBlock[]): DocJSON {
  const content = blocks.map(blockToNode)
  // ProseMirror requires a doc to have at least one block child; an empty
  // blocks list yields a single empty note node so the editor always has a
  // place to type (the Placeholder extension shows its hint here).
  if (content.length === 0) {
    content.push({
      type: 'noteBlock',
      attrs: {
        id: crypto.randomUUID(),
        depth: 0,
        bullet: '- ',
        file_date: new Date().toISOString().slice(0, 10)
      },
      content: []
    })
  }
  return { type: 'doc', content }
}

// docToBlocks is the inverse of blocksToDoc: it walks a ProseMirror doc JSON
// and reconstructs the ordered ParsedBlock list for SaveFileBlocks. It derives
// parent_id (from depth) and line_number (from doc order) and reconstructs
// raw_text sufficiently for Go's renderBlock to detect the bullet style.
export function docToBlocks(doc: DocJSON | NodeJSON): ParsedBlock[] {
  const content = doc.content || []
  const blocks: ParsedBlock[] = []

  for (let i = 0; i < content.length; i++) {
    const node = content[i]
    const lineNumber = i + 1
    const attrs = (node.attrs || {}) as Record<string, any>
    const id: string = attrs.id || ''

    // Smart Graph block-level node: the embed token is its own line. We
    // emit a NOTE block carrying just the {{embed:uuid}} text in its body
    // (atomic; depth 0; no parent). The Go side will write the token
    // verbatim, so the on-disk file is unchanged.
    if (node.type === 'embedNode') {
      const uuid = (attrs.uuid as string) || ''
      blocks.push({
        id,
        parent_id: '',
        type: 'NOTE',
        depth: 0,
        raw_text: `{{embed:${uuid}}}`,
        clean_text: `{{embed:${uuid}}}`,
        status: '',
        owner: '',
        start_date: '',
        due_date: '',
        priority: 3,
        line_number: lineNumber,
        file_date: ''
      })
      continue
    }

    // Generic plugin embedBlock (#110): serialize to a NOTE carrying the
    // `<!-- silt-embed: {json} -->` marker as its clean_text. The Go renderer
    // emits it verbatim, so the on-disk file round-trips. `notebook` is
    // persisted so the embedBlock NodeView can resolve the relPath to the
    // correct attachments/ folder when the user clicks to open the file.
    if (node.type === 'embedBlock') {
      const marker = embedBlockMarker({
        embedType: attrs.embedType || '',
        src: attrs.src || '',
        caption: attrs.caption || undefined,
        openable: attrs.openable || undefined,
        pluginID: attrs.pluginID || undefined,
        notebook: attrs.notebook || undefined
      })
      blocks.push({
        id,
        parent_id: '',
        type: 'NOTE',
        depth: 0,
        raw_text: marker,
        clean_text: marker,
        status: '',
        owner: '',
        start_date: '',
        due_date: '',
        priority: 3,
        line_number: lineNumber,
        file_date: (attrs.file_date as string) || ''
      })
      continue
    }

    const baseCleanText = serializeInlineContent(node.content)

    // Emit alignment marker for NOTE and HEADER blocks (#173). TASK blocks
    // never emit a marker (alignment is not supported on tasks).
    const align = (attrs.align as string) || 'left'
    const cleanText =
      align !== 'left' && node.type !== 'taskBlock'
        ? baseCleanText + emitAlignmentMarker(align)
        : baseCleanText

    let type: BlockType
    switch (node.type) {
      case 'taskBlock':
        type = 'TASK'
        break
      case 'headerBlock':
        type = 'HEADER'
        break
      case 'noteBlock':
      default:
        type = 'NOTE'
        break
    }

    const block: ParsedBlock = {
      id,
      parent_id: '',
      type,
      depth: Number(attrs.depth ?? 0),
      raw_text: '',
      clean_text: cleanText,
      status: '',
      owner: '',
      start_date: '',
      due_date: '',
      priority: 3,
      line_number: lineNumber,
      file_date: (attrs.file_date as string) || ''
    }

    if (type === 'TASK') {
      block.status = attrs.status || 'TODO'
      block.owner = attrs.owner || ''
      block.start_date = attrs.start_date || ''
      block.due_date = attrs.due_date || ''
      block.priority = Number(attrs.priority ?? 3)
      block.raw_text = `- [${block.status === 'DOING' ? '/' : block.status === 'DONE' ? 'x' : ' '}] ${block.status} TASK ${cleanText}`
    } else if (type === 'NOTE') {
      const bullet: string = attrs.bullet !== undefined ? attrs.bullet : '- '
      block.raw_text = `${bullet}${cleanText}`
    } else {
      block.raw_text = `${'#'.repeat(block.depth || 1)} ${cleanText}`
    }

    blocks.push(block)
  }

  deriveParentIDs(blocks)
  return blocks
}
