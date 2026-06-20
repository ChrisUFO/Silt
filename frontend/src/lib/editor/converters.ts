// Pure converters between parser.ParsedBlock (the Wails IPC JSON shape) and
// ProseMirror / TipTap doc JSON. These are the ONLY bridge between Silt's
// block data model and the editor. They have no side effects and no editor
// dependency, so they are fully unit-testable in isolation (converters.test.ts).
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

import type { ParsedBlock, DocJSON, NodeJSON, BlockType } from './types'

// Extract the bullet prefix ('- ', '* ', '+ ', or '') from a note's raw_text,
// matching the detection logic in Go's renderBlock (parser.go ~line 515-527).
function detectBullet(rawText: string): string {
  const trimmed = rawText.trimStart()
  if (trimmed.startsWith('- ')) return '- '
  if (trimmed.startsWith('* ')) return '* '
  if (trimmed.startsWith('+ ')) return '+ '
  return ''
}

// ---- Inline mark parse/serialize -----------------------------------------
// Extends the Smart Graph tokenizer with standard markdown inline marks:
// bold (**), italic (* or _), strike (~~), code (`), highlight (==),
// underline (<u>), subscript (<sub>), superscript (<sup>), and link ([t](u)).
//
// The parser is a recursive-descent tokenizer: at each position it tries mark
// openers in priority order; the first match wins, and the inner content is
// recursively parsed (except code, which shields its content). This handles
// nesting (***bold+italic***, [**bold link**](url)) correctly.
//
// The serializer uses a mark-diff approach: it walks nodes left-to-right,
// emitting open/close delimiters as the active mark set changes. This produces
// clean markdown even when marks span multiple adjacent text nodes.

type MarkRef = { type: string; attrs?: Record<string, unknown> }

// The opener syntax for each mark type.
function markOpen(mark: MarkRef): string {
  switch (mark.type) {
    case 'bold': return '**'
    case 'italic': return '*'
    case 'strike': return '~~'
    case 'highlight': return '=='
    case 'code': return '`'
    case 'underline': return '<u>'
    case 'subscript': return '<sub>'
    case 'superscript': return '<sup>'
    case 'textColor': {
      const color = (mark.attrs as Record<string, unknown> | undefined)?.color
      return color ? `<span style="color: ${color}">` : ''
    }
    case 'backgroundColor': {
      const color = (mark.attrs as Record<string, unknown> | undefined)?.color
      return color ? `<span style="background-color: ${color}">` : ''
    }
    case 'link': return '['
    default: return ''
  }
}

// The closer syntax for each mark type. Link needs the href from attrs.
function markClose(mark: MarkRef): string {
  switch (mark.type) {
    case 'bold': return '**'
    case 'italic': return '*'
    case 'strike': return '~~'
    case 'highlight': return '=='
    case 'code': return '`'
    case 'underline': return '</u>'
    case 'subscript': return '</sub>'
    case 'superscript': return '</sup>'
    case 'textColor':
    case 'backgroundColor':
      return '</span>'
    case 'link': {
      const href = (mark.attrs as Record<string, unknown> | undefined)?.href
      return `](${href || ''})`
    }
    default: return ''
  }
}

// Serialize inline content to a markdown string using the mark-diff approach.
// Walks nodes left-to-right, emitting open/close delimiters as the active mark
// set changes. Smart Graph nodes close all active marks, emit their token, then
// resume. Replaces the old plain-concatenation inlineText (#168).
function serializeInlineContent(content?: NodeJSON[]): string {
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

// ---- Inline mark parser ---------------------------------------------------

// A mark pattern tried at each position. `regex` is sticky (matches only at
// lastIndex). Capture group 1 = inner content. For links, group 2 = href.
interface MarkPattern {
  type: string
  regex: RegExp
  shield?: boolean // if true, inner content is NOT recursively parsed (code)
  wordBoundary?: boolean // if true, only match at word boundaries (_, __)
  extractAttrs?: (m: RegExpExecArray) => Record<string, unknown>
  innerGroup?: number // capture group for inner content (default 1; color marks use 2)
}

// Ordered by priority: code first (shields), then longer delimiters before
// shorter (** before *, __ before _) to avoid false matches.
const MARK_PATTERNS: MarkPattern[] = [
  { type: 'code', regex: /`([^`]+)`/y, shield: true },
  {
    type: 'link',
    regex: /\[([^\]]*)\]\(([^)\s]*)\)/y,
    extractAttrs: (m) => ({ href: m[2] })
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
  // Text color (#170): <span style="color: X">text</span>
  {
    type: 'textColor',
    regex: /<span style="color:\s*([^;"]+?)\s*;?">(.+?)<\/span>/y,
    innerGroup: 2,
    extractAttrs: (m) => ({ color: m[1].trim() })
  },
  // Background color (#170): <span style="background-color: X">text</span>
  {
    type: 'backgroundColor',
    regex: /<span style="background-color:\s*([^;"]+?)\s*;?">(.+?)<\/span>/y,
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

// Try to match any mark pattern at position `pos` in `text`. Returns the first
// match (priority order) or null.
function tryMatchMarkAt(text: string, pos: number): MarkMatch | null {
  for (const pattern of MARK_PATTERNS) {
    pattern.regex.lastIndex = pos
    const m = pattern.regex.exec(text)
    if (!m || m.index !== pos) continue
    // Intraword boundary check for underscore-based marks.
    if (pattern.wordBoundary) {
      const before = pos > 0 ? text[pos - 1] : ''
      const afterEnd = pos + m[0].length
      const after = afterEnd < text.length ? text[afterEnd] : ''
      if (/[a-zA-Z0-9]/.test(before) || /[a-zA-Z0-9]/.test(after)) continue
    }
    return {
      type: pattern.type,
      inner: m[pattern.innerGroup ?? 1],
      end: pos + m[0].length,
      shield: pattern.shield === true,
      attrs: pattern.extractAttrs?.(m)
    }
  }
  return null
}

// Recursively parse inline marks in `text`, returning an ordered list of text
// nodes (with marks). `inheritedMarks` carries marks from outer nesting levels
// so e.g. `[**bold link**](url)` produces text(bold+link, "bold link").
function parseInlineMarks(
  text: string,
  inheritedMarks: MarkRef[] = []
): NodeJSON[] {
  const nodes: NodeJSON[] = []
  let plain = ''
  let i = 0

  while (i < text.length) {
    const match = tryMatchMarkAt(text, i)
    if (match) {
      if (plain) {
        nodes.push({
          type: 'text',
          text: plain,
          marks: inheritedMarks.length ? [...inheritedMarks] : undefined
        })
        plain = ''
      }
      const newMark: MarkRef = { type: match.type }
      if (match.attrs) newMark.attrs = match.attrs
      const childMarks = [...inheritedMarks, newMark]
      if (match.shield) {
        // Code shields content: emit as-is, no recursive parsing.
        nodes.push({
          type: 'text',
          text: match.inner,
          marks: childMarks.length ? childMarks : undefined
        })
      } else {
        nodes.push(...parseInlineMarks(match.inner, childMarks))
      }
      i = match.end
    } else {
      plain += text[i]
      i++
    }
  }
  if (plain) {
    nodes.push({
      type: 'text',
      text: plain,
      marks: inheritedMarks.length ? [...inheritedMarks] : undefined
    })
  }
  return nodes
}

// ---- Smart Graph tokenization + mark parsing integration -----------------

// Smart Graph token regex (embed + block reference). UUIDs: 8-4-4-4-12 hex.
const SMART_GRAPH_TOKEN =
  /(\{\{embed:([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})\}\})|\(\(([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})\)\)/gi

type Segment =
  | { type: 'text'; text: string }
  | { type: 'embedNode'; uuid: string }
  | { type: 'blockReferenceNode'; uuid: string }

// Split clean_text on Smart Graph tokens. Text segments are later parsed for
// inline marks; token segments are emitted as-is (#85).
function splitSmartGraph(text: string): Segment[] {
  const segs: Segment[] = []
  let last = 0
  let match: RegExpExecArray | null
  SMART_GRAPH_TOKEN.lastIndex = 0
  while ((match = SMART_GRAPH_TOKEN.exec(text)) !== null) {
    if (match.index > last) {
      segs.push({ type: 'text', text: text.slice(last, match.index) })
    }
    if (match[1]) {
      segs.push({ type: 'embedNode', uuid: match[2] })
    } else if (match[3]) {
      segs.push({ type: 'blockReferenceNode', uuid: match[3] })
    }
    last = match.index + match[0].length
  }
  if (last < text.length) {
    segs.push({ type: 'text', text: text.slice(last) })
  }
  return segs
}

// Tokenize clean_text into an ordered list of inline nodes (text with marks +
// Smart Graph nodes). Two-pass: first splits on Smart Graph tokens (#85),
// then parses inline marks in each text segment (#168).
function tokenizeInline(text: string): NodeJSON[] {
  if (!text) return []
  const segments = splitSmartGraph(text)
  const nodes: NodeJSON[] = []
  for (const seg of segments) {
    if (seg.type === 'text') {
      nodes.push(...parseInlineMarks(seg.text))
    } else if (seg.type === 'embedNode') {
      nodes.push({ type: 'embedNode', attrs: { uuid: seg.uuid } })
    } else {
      nodes.push({ type: 'blockReferenceNode', attrs: { uuid: seg.uuid } })
    }
  }
  return nodes
}

// ---- Alignment marker helpers (#173) -------------------------------------
// Block-level alignment is persisted as a trailing HTML-comment marker in
// clean_text: `text <!-- silt-align: center -->`. The marker is invisible
// in the rendered editor and any markdown viewer, but present in the raw
// file. Default 'left' emits no marker. TASK blocks never emit a marker
// (alignment is not supported on tasks).

const ALIGN_MARKER_RE = /\s*<!-- silt-align: (left|center|right|justify) -->\s*$/

export function stripAlignmentMarker(
  cleanText: string
): { body: string; align: string } {
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
  // activeIDs[d] = id of the most recent block seen at depth d
  const activeIDs: string[] = []
  for (const b of blocks) {
    if (b.depth > 0) {
      b.parent_id = b.depth - 1 < activeIDs.length ? activeIDs[b.depth - 1] : ''
    } else {
      b.parent_id = ''
    }
    // Record this block at its depth level, truncating any deeper entries.
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

  const content: NodeJSON[] = text ? tokenizeInline(text) : []

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
      // raw_text is not used by renderBlock for tasks (it builds the line from
      // typed fields), but set a non-empty value so the block is never treated
      // as brand-new prose.
      block.raw_text = `- [${block.status === 'DOING' ? '/' : block.status === 'DONE' ? 'x' : ' '}] ${block.status} TASK ${cleanText}`
    } else if (type === 'NOTE') {
      const bullet: string = attrs.bullet || '- '
      block.raw_text = `${bullet}${cleanText}`
    } else {
      // HEADER
      block.raw_text = `${'#'.repeat(block.depth || 1)} ${cleanText}`
    }

    blocks.push(block)
  }

  deriveParentIDs(blocks)
  return blocks
}
