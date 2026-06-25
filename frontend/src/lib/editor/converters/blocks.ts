// Block-level converters between parser.ParsedBlock (the Wails IPC JSON
// shape) and ProseMirror / TipTap doc JSON. The bridge between Silt's block
// data model and the editor.
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

import { serializeInlineContent, legacyTokenizeInline } from './serialize'
import type { ParsedBlock, DocJSON, NodeJSON, BlockType } from '../types'

// Concatenate the text descendants of a NodeJSON[] (used for code blocks,
// whose content is plain `text*` with no marks). Newlines inside a text node
// are preserved, so a multi-line code block round-trips verbatim.
function extractTextContent(content?: NodeJSON[]): string {
  if (!content) return ''
  let out = ''
  for (const child of content) {
    if (child.text !== undefined) out += child.text
    else if (child.content) out += extractTextContent(child.content)
  }
  return out
}

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

// Detect a blockquote prefix (#188). A leading run of `>` followed by a space
// is a quote marker (`> `, `>> `, `>>> `). Returns the marker (with trailing
// space) and the body with the marker stripped, or '' + the original body when
// the line is not a quote. Quote and bullet markers are mutually exclusive.
const QUOTE_PREFIX_RE = /^(>+)\s/
function detectQuote(body: string): { quote: string; body: string } {
  const m = body.match(QUOTE_PREFIX_RE)
  if (!m) return { quote: '', body }
  return { quote: m[1] + ' ', body: body.slice(m[0].length) }
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

  // A CODE block (#189) carries multi-line fenced content verbatim. Its
  // clean_text keeps internal newlines (the Go parser preserves them); the
  // codeBlock node stores that text as a single text node. CODE uses the raw
  // clean_text directly — alignment markers are a prose-block concept, and
  // stripping would corrupt code that happened to end in that pattern.
  if (block.type === 'CODE') {
    const codeText = rawText
    return {
      type: 'codeBlock',
      attrs: {
        id: block.id,
        language: block.language || '',
        file_date: block.file_date || ''
      },
      content: codeText ? [{ type: 'text', text: codeText }] : []
    }
  }

  // A TABLE block (#310) carries the raw GFM pipe rows as multi-line
  // clean_text. Parse the rows into a table node tree (tableRow/tableHeader/
  // tableCell). Uses the raw clean_text directly — alignment markers are a
  // prose-block concept.
  if (block.type === 'TABLE') {
    const lines = rawText.split('\n').filter(Boolean)
    if (lines.length >= 2) {
      const headerCells = parseGfmRow(lines[0])
      const dataRows = lines.slice(2).map(parseGfmRow)
      const mkCell = (
        type: 'tableHeader' | 'tableCell',
        text: string
      ): NodeJSON => ({
        type,
        attrs: {},
        content: text ? legacyTokenizeInline(text) : []
      })
      const mkRow = (
        cells: string[],
        type: 'tableHeader' | 'tableCell'
      ): NodeJSON => ({
        type: 'tableRow',
        attrs: {},
        content: cells.map((c) => mkCell(type, c))
      })
      const rows: NodeJSON[] = [mkRow(headerCells, 'tableHeader')]
      for (const r of dataRows) rows.push(mkRow(r, 'tableCell'))
      return {
        type: 'table',
        attrs: { id: block.id || null, file_date: block.file_date || '' },
        content: rows
      }
    }
  }

  // A DETAILS block (#310) carries the full <details>…</details> HTML as
  // multi-line clean_text. Parse into a details/detailsSummary/detailsContent
  // tree. The body lines become child block nodes.
  if (block.type === 'DETAILS') {
    return parseDetailsHTML(rawText, block.id, block.file_date || '')
  }

  // A CALLOUT block (#308) carries multi-line Obsidian callout syntax as
  // clean_text (`> [!variant] message` + subsequent `>` body lines). Parse
  // into a calloutBlock node with paragraph children. Each `>` line becomes
  // a paragraph; the variant is extracted from the first line's `[!variant]`.
  if (block.type === 'CALLOUT') {
    return parseCalloutText(rawText, block.id, block.file_date || '')
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
      //
      // Quote detection (#188): a `> ` prefix (stripped of any alignment
      // marker first) is a blockquote marker, parallel to `bullet`. The
      // marker is stored on the node so it round-trips verbatim. Callouts
      // (#180/#308) are now CALLOUT-typed blocks — no longer detected here.
      const { quote, body: quoteStripped } = detectQuote(text)
      const noteContent: NodeJSON[] = quoteStripped
        ? legacyTokenizeInline(quoteStripped)
        : []
      return {
        type: 'noteBlock',
        attrs: {
          id: block.id,
          depth: block.depth,
          bullet: quote ? '' : detectBullet(block.raw_text),
          quote,
          align,
          file_date: block.file_date || ''
        },
        content: noteContent
      }
  }
}

// ---- GFM table helpers (used by blockToNode + docToBlocks TABLE branches) --
// A TABLE ParsedBlock's clean_text is the raw GFM rows. These helpers split
// rows into cells and escape cell values for round-trip serialization.

// Split a GFM row into cell strings. The leading/trailing `|` are stripped;
// cells are split on unescaped `|`; `\|` is unescaped back to `|`.
function parseGfmRow(line: string): string[] {
  let s = line.trim()
  if (s.startsWith('|')) s = s.slice(1)
  if (s.endsWith('|')) s = s.slice(0, -1)
  // Walk the string handling GFM cell escapes. `\\` → `\` and `\|` → `|`
  // (the escapes escapeGfmCell emits); a bare `|` is a cell delimiter. `\\`
  // is checked before `\|` so an escaped backslash adjacent to a pipe
  // (`\\|` on disk) parses as `\` + delimiter, not an escaped pipe.
  const cells: string[] = []
  let cur = ''
  for (let i = 0; i < s.length; i++) {
    const ch = s[i]
    if (ch === '\\') {
      const next = s[i + 1]
      if (next === '\\') {
        cur += '\\'
        i++
      } else if (next === '|') {
        cur += '|'
        i++
      } else {
        cur += ch
      }
    } else if (ch === '|') {
      cells.push(cur.trim())
      cur = ''
    } else {
      cur += ch
    }
  }
  cells.push(cur.trim())
  return cells
}

// Escape a cell value for GFM. Backslashes are escaped first (`\` → `\\`),
// then pipes (`|` → `\|`), so a literal backslash before a delimiter can't be
// misread as an escaped pipe on re-parse. Newlines collapse to spaces (a cell
// is a single line).
function escapeGfmCell(s: string): string {
  return s.replace(/\\/g, '\\\\').replace(/\|/g, '\\|').replace(/\n/g, ' ')
}

// ---- Details HTML parsing (#310) ------------------------------------------
// The Go parser produces ONE DETAILS ParsedBlock whose clean_text is the full
// <details>…</details> HTML. These helpers parse that HTML into a details node
// tree (load) and serialize it back (save). The body lines between <summary>
// and </details> become child block nodes.

const DETAILS_OPEN_RE = /^<details(?:\s+[^>]*)?>$/i
const DETAILS_CLOSE_RE = /^<\/details>$/i
const DETAILS_SUMMARY_RE = /^<summary(?:\s[^>]*)?>([\s\S]*?)<\/summary>$/i

// Parse <details> HTML into a details node tree. The opener's line carries the
// block id (inherited from the old converter model). Body lines become child
// block nodes via synthetic ParsedBlocks fed through blockToNode.
function parseDetailsHTML(
  html: string,
  blockId: string,
  fileDate: string
): NodeJSON {
  const lines = html.split('\n')
  const today = new Date().toISOString().slice(0, 10)

  let summaryText = ''
  let bodyStartIdx = 1

  // Extract summary from the second line (if present).
  if (lines.length > 1) {
    const sm = lines[1].trim().match(DETAILS_SUMMARY_RE)
    if (sm) {
      summaryText = sm[1]
      bodyStartIdx = 2
    }
  }

  // Body lines: everything between summary and </details>.
  const bodyLines = lines.slice(bodyStartIdx, -1)

  // Convert body lines to child nodes, handling nested <details>.
  const childNodes = detailsBodyLinesToNodes(bodyLines)

  return {
    type: 'details',
    attrs: {
      id: blockId || null,
      open: false,
      file_date: fileDate || today
    },
    content: [
      {
        type: 'detailsSummary',
        attrs: { id: null },
        content: summaryText ? [{ type: 'text', text: summaryText }] : []
      },
      {
        type: 'detailsContent',
        attrs: { id: null },
        content: childNodes.length
          ? childNodes
          : [
              {
                type: 'noteBlock',
                attrs: { id: null, depth: 0, bullet: '', file_date: today },
                content: []
              }
            ]
      }
    ]
  }
}

// Convert body lines (inside <details>) to child block nodes. Detects nested
// multi-line constructs (code fences, GFM tables, callouts, nested <details>)
// and accumulates them into typed synthetic blocks before falling through to
// regular NOTE lines.
function detailsBodyLinesToNodes(lines: string[]): NodeJSON[] {
  const nodes: NodeJSON[] = []
  let i = 0
  while (i < lines.length) {
    const trimmed = lines[i].trim()

    // Nested <details> → recursively parse.
    if (DETAILS_OPEN_RE.test(trimmed)) {
      let depth = 1
      let j = i + 1
      while (j < lines.length && depth > 0) {
        if (DETAILS_OPEN_RE.test(lines[j].trim())) depth++
        else if (DETAILS_CLOSE_RE.test(lines[j].trim())) depth--
        if (depth === 0) break
        j++
      }
      if (depth === 0) {
        const nestedHTML = lines.slice(i, j + 1).join('\n')
        nodes.push(parseDetailsHTML(nestedHTML, '', ''))
        i = j + 1
        continue
      }
    }

    // Code fence → accumulate to closing fence → CODE synthetic block.
    if (/^```/.test(trimmed)) {
      let j = i + 1
      while (j < lines.length && !/^```/.test(lines[j].trim())) j++
      if (j < lines.length) {
        const code = lines.slice(i + 1, j).join('\n')
        const lang = trimmed.slice(3)
        nodes.push(
          blockToNode({
            id: '',
            parent_id: '',
            type: 'CODE',
            depth: 0,
            raw_text: '',
            clean_text: code,
            status: '',
            owner: '',
            start_date: '',
            due_date: '',
            priority: 3,
            line_number: i + 1,
            file_date: '',
            language: lang
          })
        )
        i = j + 1
        continue
      }
    }

    // GFM table → accumulate pipe rows → TABLE synthetic block.
    if (
      /^\|.*\|$/.test(trimmed) &&
      i + 1 < lines.length &&
      /^\|[\s:|-]+\|$/.test(lines[i + 1].trim())
    ) {
      let j = i
      while (j < lines.length && /^\|.*\|$/.test(lines[j].trim())) j++
      const gfm = lines.slice(i, j).join('\n')
      nodes.push(
        blockToNode({
          id: '',
          parent_id: '',
          type: 'TABLE',
          depth: 0,
          raw_text: '',
          clean_text: gfm,
          status: '',
          owner: '',
          start_date: '',
          due_date: '',
          priority: 3,
          line_number: i + 1,
          file_date: ''
        })
      )
      i = j
      continue
    }

    // Callout → accumulate `>` lines → CALLOUT synthetic block.
    if (/^>\s*\[!/i.test(trimmed)) {
      let j = i + 1
      while (j < lines.length && /^>/.test(lines[j].trim())) j++
      const calloutText = lines.slice(i, j).join('\n')
      nodes.push(
        blockToNode({
          id: '',
          parent_id: '',
          type: 'CALLOUT',
          depth: 0,
          raw_text: calloutText,
          clean_text: calloutText,
          status: '',
          owner: '',
          start_date: '',
          due_date: '',
          priority: 3,
          line_number: i + 1,
          file_date: ''
        })
      )
      i = j
      continue
    }

    // Regular body line → synthetic NOTE → blockToNode.
    const syntheticBlock: ParsedBlock = {
      id: '',
      parent_id: '',
      type: 'NOTE',
      depth: 0,
      raw_text: lines[i],
      clean_text: lines[i],
      status: '',
      owner: '',
      start_date: '',
      due_date: '',
      priority: 3,
      line_number: i + 1,
      file_date: ''
    }
    nodes.push(blockToNode(syntheticBlock))
    i++
  }
  return nodes
}

// Serialize a details node tree back to <details> HTML text (for docToBlocks).
// The child nodes' text representations become body lines (without id comments
// — the DETAILS block has its own trailing id).
function serializeDetailsToHTML(node: NodeJSON): string {
  const attrs = (node.attrs || {}) as Record<string, any>
  const summaryNode = (node.content || []).find(
    (c) => c.type === 'detailsSummary'
  )
  const contentNode = (node.content || []).find(
    (c) => c.type === 'detailsContent'
  )
  const summaryText = summaryNode
    ? serializeInlineContent(summaryNode.content)
    : ''

  const lines: string[] = ['<details>']
  if (summaryText) lines.push(`<summary>${summaryText}</summary>`)

  if (contentNode?.content) {
    // Strip leading empty placeholder noteBlocks (the synthetic seed).
    let children = contentNode.content
    while (
      children.length > 0 &&
      children[0].type === 'noteBlock' &&
      (children[0].content || []).length === 0
    ) {
      children = children.slice(1)
    }
    for (const child of children) {
      lines.push(serializeChildNodeToBodyLine(child))
    }
  }
  lines.push('</details>')
  return lines.join('\n')
}

// Convert a child node (inside detailsContent) to a text body line (no id).
// Handles multi-line constructs (codeBlock, table, calloutBlock) by emitting
// their full on-disk syntax — the caller embeds the multi-line result in the
// <details> body.
function serializeChildNodeToBodyLine(node: NodeJSON): string {
  const attrs = (node.attrs || {}) as Record<string, any>

  // Nested details → recursively serialize to HTML.
  if (node.type === 'details') {
    return serializeDetailsToHTML(node)
  }

  // Code block → emit fenced code (preserves language + body).
  if (node.type === 'codeBlock') {
    const lang = (attrs.language as string) || ''
    const code = extractTextContent(node.content)
    const fence = '```'
    return `${fence}${lang}\n${code}\n${fence}`
  }

  // Table → emit GFM rows (reuses the table serialization logic).
  if (node.type === 'table') {
    return serializeTableToGFM(node)
  }

  // Callout → emit Obsidian callout lines.
  if (node.type === 'calloutBlock') {
    return serializeCalloutToText(node)
  }

  const text = serializeInlineContent(node.content)

  if (node.type === 'noteBlock') {
    const bullet = attrs.bullet !== undefined ? attrs.bullet : ''
    const quote = (attrs.quote as string) || ''
    if (quote) return `${quote}${text}`
    return `${bullet}${text}`
  }
  if (node.type === 'taskBlock') {
    const checkbox =
      attrs.status === 'DOING' ? '/' : attrs.status === 'DONE' ? 'x' : ' '
    return `- [${checkbox}] ${text}`
  }
  if (node.type === 'headerBlock') {
    const depth = Number(attrs.depth || 1)
    return `${'#'.repeat(depth)} ${text}`
  }
  // paragraph or unknown: just the text.
  return text
}

// Serialize a table node to GFM pipe rows (shared by docToBlocks TABLE branch
// and serializeChildNodeToBodyLine for nested tables in details).
function serializeTableToGFM(node: NodeJSON): string {
  const rows = (node.content || []).filter((c) => c.type === 'tableRow')
  if (rows.length === 0) return ''
  const grid: { header: boolean; cells: string[] }[] = rows.map((r) => ({
    header: (r.content || []).some((c) => c.type === 'tableHeader'),
    cells: (r.content || []).map((c) =>
      escapeGfmCell(serializeInlineContent(c.content))
    )
  }))
  const colCount = Math.max(...grid.map((r) => r.cells.length))
  const widths = new Array(colCount).fill(0)
  for (const r of grid) {
    while (r.cells.length < colCount) r.cells.push('')
    for (let c = 0; c < colCount; c++)
      widths[c] = Math.max(widths[c], r.cells[c].length, 3)
  }
  const pad = (cell: string, c: number) => cell.padEnd(widths[c], ' ')
  const renderRow = (r: { cells: string[] }) =>
    '| ' + r.cells.map((c, i) => pad(c, i)).join(' | ') + ' |'
  const lines: string[] = []
  lines.push(renderRow(grid[0]))
  lines.push('| ' + widths.map((w) => ''.padEnd(w, '-')).join(' | ') + ' |')
  for (let r = 1; r < grid.length; r++) lines.push(renderRow(grid[r]))
  return lines.join('\n')
}

// ---- Callout text parsing (#308) ------------------------------------------
// The Go parser produces ONE CALLOUT ParsedBlock whose clean_text is the full
// Obsidian callout syntax (`> [!variant] message` + subsequent `>` body lines).
// These helpers parse that text into a calloutBlock node with paragraph
// children (load) and serialize it back (save).

const CALLOUT_MARKER_RE =
  /^\s*>\s*\[!(note|info|tip|warning|danger|success|quote)\](?:\s+(.*))?$/i

// Parse callout text into a calloutBlock node. Each `>` line becomes a
// paragraph child; the variant is extracted from the first line's marker.
function parseCalloutText(
  text: string,
  blockId: string,
  fileDate: string
): NodeJSON {
  const lines = text.split('\n')
  const today = new Date().toISOString().slice(0, 10)

  // Extract variant from the first line.
  let variant = 'note'
  let firstMessage = ''
  const firstMatch = lines[0]?.match(CALLOUT_MARKER_RE)
  if (firstMatch) {
    variant = firstMatch[1].toLowerCase()
    firstMessage = firstMatch[2] ?? ''
  }

  // Build paragraph children: first line's message is paragraph 1; each
  // subsequent `>` line is another paragraph (bare `>` = empty paragraph).
  const mkParagraph = (content: string): NodeJSON => ({
    type: 'paragraph',
    attrs: {},
    content: content ? legacyTokenizeInline(content) : []
  })

  const children: NodeJSON[] = [mkParagraph(firstMessage)]
  for (let i = 1; i < lines.length; i++) {
    // Strip the `> ` or `>` prefix from each body line.
    const stripped = lines[i].replace(/^\s*>\s?/, '')
    children.push(mkParagraph(stripped))
  }

  return {
    type: 'calloutBlock',
    attrs: {
      id: blockId || null,
      variant,
      file_date: fileDate || today
    },
    content: children
  }
}

// Serialize a calloutBlock node back to Obsidian callout text (for docToBlocks).
// The first paragraph becomes `> [!variant] message`; subsequent paragraphs
// become `> body` lines.
function serializeCalloutToText(node: NodeJSON): string {
  const attrs = (node.attrs || {}) as Record<string, any>
  const variant = (attrs.variant as string) || 'note'
  const children = (node.content || []).filter((c) => c.type === 'paragraph')

  const lines: string[] = []
  // First paragraph: the title/message line.
  const firstText = children.length
    ? serializeInlineContent(children[0].content)
    : ''
  lines.push(`> [!${variant}]${firstText ? ' ' + firstText : ''}`)
  // Subsequent paragraphs: body lines.
  for (let i = 1; i < children.length; i++) {
    const text = serializeInlineContent(children[i].content)
    lines.push(text ? `> ${text}` : '>')
  }
  return lines.join('\n')
}

// blocksToDoc converts an ordered list of ParsedBlocks into a ProseMirror doc
// JSON suitable for editor.commands.setContent(). Each ParsedBlock maps 1:1
// to one top-level block node (nesting is expressed via the depth attr).
// The unified region-block model (#310) eliminated the old multi-block
// regrouping layer — tables, details, and callouts arrive as single typed
// ParsedBlocks, not runs of NOTE blocks.
export function blocksToDoc(blocks: ParsedBlock[]): DocJSON {
  const content: NodeJSON[] = blocks.map(blockToNode)
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

    // Callout / admonition (#180/#308): serialize to ONE CALLOUT ParsedBlock
    // whose clean_text is the Obsidian `> [!variant] message` + body lines.
    if (node.type === 'calloutBlock') {
      const calloutText = serializeCalloutToText(node)
      blocks.push({
        id,
        parent_id: '',
        type: 'CALLOUT',
        depth: 0,
        raw_text: calloutText,
        clean_text: calloutText,
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

    // Code block (#189): a multi-line CODE ParsedBlock. The Go renderer's
    // BlockCode branch re-emits the ``` ``` fence verbatim (newlines
    // preserved). The code text is the concatenation of the node's text
    // descendants — code is literal (no marks).
    if (node.type === 'codeBlock') {
      const code = extractTextContent(node.content)
      blocks.push({
        id,
        parent_id: '',
        type: 'CODE',
        depth: 0,
        raw_text: '',
        clean_text: code,
        status: '',
        owner: '',
        start_date: '',
        due_date: '',
        priority: 3,
        line_number: lineNumber,
        file_date: (attrs.file_date as string) || '',
        language: (attrs.language as string) || ''
      })
      continue
    }

    // Foldable details (#183/#310): serialize to ONE DETAILS ParsedBlock whose
    // clean_text is the full <details>…</details> HTML. The Go parser's
    // DETAILS region accumulator produces this on parse; renderBlock emits it
    // verbatim + a trailing id line.
    if (node.type === 'details') {
      const fileDate = (attrs.file_date as string) || ''
      const html = serializeDetailsToHTML(node)
      blocks.push({
        id,
        parent_id: '',
        type: 'DETAILS',
        depth: 0,
        raw_text: html,
        clean_text: html,
        status: '',
        owner: '',
        start_date: '',
        due_date: '',
        priority: 3,
        line_number: lineNumber,
        file_date: fileDate
      })
      continue
    }

    // GFM table (#172/#310): serialize to ONE TABLE ParsedBlock via the
    // shared serializeTableToGFM helper (also used for nested tables in
    // details). Literal `|` is escaped as `\|`; auto-width padding.
    if (node.type === 'table') {
      const gfm = serializeTableToGFM(node)
      if (!gfm) continue
      blocks.push({
        id,
        parent_id: '',
        type: 'TABLE',
        depth: 0,
        raw_text: '',
        clean_text: gfm,
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

    // Quote prefix (#188): a noteBlock carrying a `quote` attr re-emits the
    // marker (`> `, `>> `…) as the leading text so the on-disk line is
    // standard markdown blockquote syntax. The marker prepends the body
    // BEFORE the alignment marker so `> text <!-- silt-align: center -->`
    // round-trips. TASK/HEADER blocks never carry a quote.
    const quoteMarker: string =
      node.type === 'noteBlock' ? (attrs.quote as string) || '' : ''

    // Emit alignment marker for NOTE and HEADER blocks (#173). TASK blocks
    // never emit a marker (alignment is not supported on tasks).
    const align = (attrs.align as string) || 'left'
    const cleanText =
      (quoteMarker ? quoteMarker : '') +
      (align !== 'left' && node.type !== 'taskBlock'
        ? baseCleanText + emitAlignmentMarker(align)
        : baseCleanText)

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
      if (quoteMarker) {
        // Quote blocks carry the marker as the prefix; renderBlock sees the
        // leading `>` (not a `-/*/+` bullet) and emits a plain line, so the
        // `> ` survives verbatim. Bullet is cleared so the two markers never
        // coexist.
        block.raw_text = `${quoteMarker}${baseCleanText}`
      } else {
        const bullet: string = attrs.bullet !== undefined ? attrs.bullet : '- '
        block.raw_text = `${bullet}${cleanText}`
      }
    } else {
      block.raw_text = `${'#'.repeat(block.depth || 1)} ${cleanText}`
    }

    blocks.push(block)
  }

  deriveParentIDs(blocks)
  return blocks
}
