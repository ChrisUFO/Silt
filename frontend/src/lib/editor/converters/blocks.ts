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

// Push one opaque NOTE line — used for `<details>` runs (#183) and GFM table
// rows (#172). The Go parser preserves the clean_text verbatim (HTML passes
// through, and pipe characters are just text), so these lines round-trip
// byte-for-byte. Pass the block id only on the line that should carry the
// identity (the `<details>` opener / the LAST table row).
function pushOpaqueNoteLine(
  blocks: ParsedBlock[],
  id: string,
  text: string,
  lineNumber: number,
  fileDate: string
): void {
  blocks.push({
    id,
    parent_id: '',
    type: 'NOTE',
    depth: 0,
    raw_text: text,
    clean_text: text,
    status: '',
    owner: '',
    start_date: '',
    due_date: '',
    priority: 3,
    line_number: lineNumber,
    file_date: fileDate
  })
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

// Detect a callout / admonition (#180). An Obsidian-style callout is a `>`
// line whose body starts with `[!variant]`. Returns the variant + the message
// (everything after the marker), or null when the line is not a callout. A
// callout takes precedence over a plain quote (it IS a quote whose first token
// is the `[!type]` marker).
const CALLOUT_RE =
  /^\[!(note|info|tip|warning|danger|success|quote)\](?:\s+(.*))?$/
function detectCallout(
  body: string
): { variant: string; message: string } | null {
  const q = body.match(QUOTE_PREFIX_RE)
  if (!q) return null
  const afterMarker = body.slice(q[0].length)
  const m = afterMarker.match(CALLOUT_RE)
  if (!m) return null
  return { variant: m[1], message: m[2] ?? '' }
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
      // Callout detection (#180) takes precedence over quote: a `> [!variant]`
      // line is a callout node, not a plain quote.
      const callout = detectCallout(text)
      if (callout) {
        return {
          type: 'calloutBlock',
          attrs: {
            id: block.id,
            variant: callout.variant,
            file_date: block.file_date || ''
          },
          content: callout.message ? legacyTokenizeInline(callout.message) : []
        }
      }
      // Quote detection (#188): a `> ` prefix (stripped of any alignment
      // marker first) is a blockquote marker, parallel to `bullet`. The
      // marker is stored on the node so it round-trips verbatim.
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

// Detect the opening of an HTML `<details>` region (#183). The Go parser
// preserves each line of a `<details>` block as an opaque NOTE (HTML passes
// through clean_text verbatim, like the color spans), so a foldable section
// is a RUN of NOTE blocks the converter groups into one Details node tree.
// `<details>` and `<details open>` both open; the run ends at `</details>`.
const DETAILS_OPEN_RE = /^<details(?:\s+[^>]*)?>$/i
const DETAILS_CLOSE_RE = /^<\/details>$/i
const DETAILS_SUMMARY_RE = /^<summary(?:\s[^>]*)?>([\s\S]*?)<\/summary>$/i

// ---- GFM table parsing (#172) --------------------------------------------
// A table is a run of pipe-prefixed NOTE blocks: a header row, a `| --- |`
// separator row, and one or more data rows. The converter groups the run into
// one `table` node tree (each row → tableRow; header cells → tableHeader,
// data cells → tableCell). Literal `|` in a cell is escaped as `\|` per GFM.
const GFM_ROW_RE = /^\|.*\|$/
const GFM_SEP_RE = /^\|[\s:|-]+\|$/

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

// Test whether a NOTE block's clean_text is a GFM table row.
function isGfmRow(text: string): boolean {
  return GFM_ROW_RE.test((text || '').trim())
}
function isGfmSeparator(text: string): boolean {
  return GFM_SEP_RE.test((text || '').trim())
}

// Does a table run START at blocks[idx]? Requires a header row followed by a
// separator row (the two-line minimum that distinguishes a table from a stray
// pipe-prefixed note).
function tableRunStartsAt(blocks: ParsedBlock[], idx: number): boolean {
  return (
    idx + 1 < blocks.length &&
    isGfmRow(blocks[idx].clean_text || '') &&
    isGfmSeparator(blocks[idx + 1].clean_text || '')
  )
}

// Read a GFM table run starting at blocks[idx] (header + separator + data
// rows) and return the assembled `table` node JSON + the number of blocks
// consumed. The block id comes from the LAST row of the run (so the whole
// table has one identity — matches how it round-trips on disk).
function buildTableNode(
  blocks: ParsedBlock[],
  startIdx: number
): { node: NodeJSON; consumed: number; id: string } {
  // Consume consecutive pipe rows.
  let endIdx = startIdx
  while (endIdx < blocks.length && isGfmRow(blocks[endIdx].clean_text || '')) {
    endIdx++
  }
  // endIdx is now one past the last table row.
  const run = blocks.slice(startIdx, endIdx)
  const headerCells = parseGfmRow(run[0].clean_text || '')
  const dataRows = run.slice(2).map((b) => parseGfmRow(b.clean_text || ''))

  const today = new Date().toISOString().slice(0, 10)
  const id = run[run.length - 1].id || null

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

  const node: NodeJSON = {
    type: 'table',
    attrs: { id, file_date: run[run.length - 1].file_date || today },
    content: rows
  }
  return { node, consumed: run.length, id: id || '' }
}

// blocksToDoc converts an ordered list of ParsedBlocks into a ProseMirror doc
// JSON suitable for editor.commands.setContent(). Most blocks become one
// top-level block node (nesting is expressed via the depth attr), but a
// `<details>` run (#183) is grouped into a single Details node tree whose
// inner lines become normal child blocks. An index-based scanner lets a
// group consume multiple input blocks.
export function blocksToDoc(blocks: ParsedBlock[]): DocJSON {
  const content: NodeJSON[] = []
  let i = 0
  while (i < blocks.length) {
    const block = blocks[i]
    // GFM table run (#172): header + separator + data rows → one table node.
    if (tableRunStartsAt(blocks, i)) {
      const { node, consumed } = buildTableNode(blocks, i)
      content.push(node)
      i += consumed
      continue
    }
    if (DETAILS_OPEN_RE.test((block.clean_text || '').trim())) {
      const { node, consumed } = buildDetailsNode(blocks, i)
      if (node) {
        content.push(node)
        i += consumed
        continue
      }
    }
    content.push(blockToNode(block))
    i++
  }
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

// buildDetailsNode reads a `<details>` run starting at blocks[startIdx] and
// returns the assembled Details node JSON plus the number of input blocks it
// consumed (opener..closer inclusive). Nested `<details>` are handled by
// depth counting. The opener's id becomes the Details node's id; the
// `<summary>` line (if present) seeds the summary; every line between becomes
// a child block (recursively, so a nested `<details>` is a child Details).
function buildDetailsNode(
  blocks: ParsedBlock[],
  startIdx: number
): { node: NodeJSON | null; consumed: number } {
  const opener = blocks[startIdx]
  let depth = 1
  let endIdx = startIdx + 1
  while (endIdx < blocks.length && depth > 0) {
    const t = (blocks[endIdx].clean_text || '').trim()
    if (DETAILS_OPEN_RE.test(t)) depth++
    else if (DETAILS_CLOSE_RE.test(t)) depth--
    if (depth === 0) break
    endIdx++
  }
  if (depth !== 0) {
    // Unterminated `<details>` — leave the opener as a plain NOTE.
    return { node: null, consumed: 1 }
  }

  const inner = blocks.slice(startIdx + 1, endIdx) // between the tags
  let summaryText = ''
  let bodyStart = 0
  if (inner.length > 0) {
    const sm = (inner[0].clean_text || '').trim().match(DETAILS_SUMMARY_RE)
    if (sm) {
      summaryText = sm[1]
      bodyStart = 1
    }
  }
  const bodyBlocks = inner.slice(bodyStart)
  const childNodes = bodyBlocks.length ? blocksToDoc(bodyBlocks).content : []

  const today = new Date().toISOString().slice(0, 10)
  const node: NodeJSON = {
    type: 'details',
    attrs: {
      id: opener.id || null,
      open: false,
      file_date: opener.file_date || today
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
  return { node, consumed: endIdx - startIdx + 1 }
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

    // Callout / admonition (#180): serialize to a NOTE whose clean_text is the
    // Obsidian `> [!variant] message` line. renderBlock sees the leading `>`
    // (not a bullet) and emits a plain line, so the marker survives verbatim.
    if (node.type === 'calloutBlock') {
      const variant = (attrs.variant as string) || 'note'
      const message = serializeInlineContent(node.content)
      const line = `> [!${variant}]${message ? ' ' + message : ''}`
      blocks.push({
        id,
        parent_id: '',
        type: 'NOTE',
        depth: 0,
        raw_text: line,
        clean_text: line,
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

    // Foldable details (#183): serialize to a RUN of NOTE blocks — the
    // `<details>` opener, a `<summary>` line, the child blocks (recursively
    // serialized), and the `</details>` closer. The Go parser preserves each
    // line verbatim (HTML passes through clean_text), and blocksToDoc regroups
    // the run on load. The opener carries the block id; collapse state is
    // ephemeral (never persisted as `<details open>` in v1).
    if (node.type === 'details') {
      const fileDate = (attrs.file_date as string) || ''
      const summaryNode = (node.content || []).find(
        (c) => c.type === 'detailsSummary'
      )
      const contentNode = (node.content || []).find(
        (c) => c.type === 'detailsContent'
      )
      const summaryText = summaryNode
        ? serializeInlineContent(summaryNode.content)
        : ''
      pushOpaqueNoteLine(blocks, id, '<details>', lineNumber, fileDate)
      pushOpaqueNoteLine(
        blocks,
        '',
        `<summary>${summaryText}</summary>`,
        lineNumber,
        fileDate
      )
      if (contentNode?.content) {
        // Child blocks recurse through docToBlocks so nested details, code,
        // tables, etc. inside a foldable section round-trip correctly.
        const childBlocks = docToBlocks({
          type: 'doc',
          content: contentNode.content
        })
        for (const cb of childBlocks) blocks.push(cb)
      }
      pushOpaqueNoteLine(blocks, '', '</details>', lineNumber, fileDate)
      continue
    }

    // GFM table (#172): serialize the table node to a run of NOTE blocks —
    // the header row, the `| --- |` separator, and one block per data row.
    // Auto-width padding keeps the file human-readable; literal `|` is
    // escaped as `\|`; cell inline content goes through serializeInlineContent
    // so marks + Smart Graph tokens survive. The block id lands on the LAST
    // row so the whole table has one identity.
    if (node.type === 'table') {
      const rows = (node.content || []).filter((c) => c.type === 'tableRow')
      if (rows.length === 0) continue
      const grid: { header: boolean; cells: string[] }[] = rows.map((r) => ({
        header: (r.content || []).some((c) => c.type === 'tableHeader'),
        cells: (r.content || []).map((c) =>
          escapeGfmCell(serializeInlineContent(c.content))
        )
      }))
      const colCount = Math.max(...grid.map((r) => r.cells.length))
      // Pad short rows; compute column widths for readability.
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
      // Header row (first).
      lines.push(renderRow(grid[0]))
      // Separator.
      lines.push('| ' + widths.map((w) => ''.padEnd(w, '-')).join(' | ') + ' |')
      // Data rows.
      for (let r = 1; r < grid.length; r++) lines.push(renderRow(grid[r]))

      const fileDate = (attrs.file_date as string) || ''
      lines.forEach((line, idx) => {
        const isLast = idx === lines.length - 1
        pushOpaqueNoteLine(blocks, isLast ? id : '', line, lineNumber, fileDate)
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
