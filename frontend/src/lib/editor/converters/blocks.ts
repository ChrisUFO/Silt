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
    case 'CODE':
      return {
        type: 'codeBlock',
        attrs: {
          id: block.id,
          lang: block.code_lang || '',
          file_date: block.file_date || ''
        },
        content: [{ type: 'text', text: block.clean_text || '' }]
      }
    case 'NOTE':
    default: {
      let body = text

      // Detect <details> HTML for foldable sections (#183). The Go parser
      // preserves HTML verbatim, so no Go-side changes are needed.
      if (text.startsWith('<details>')) {
        const summaryMatch = text.match(/<summary>(.*?)<\/summary>/)
        const summary = summaryMatch ? summaryMatch[1] : ''
        const bodyText = text
          .replace(/<details><summary>.*?<\/summary>/, '')
          .replace(/<\/details>\s*$/, '')
        const contentNodes: NodeJSON[] = bodyText
          ? legacyTokenizeInline(bodyText)
          : []
        return {
          type: 'detailsBlock',
          attrs: {
            id: block.id,
            summary,
            open: false,
            file_date: block.file_date || ''
          },
          content: contentNodes
        }
      }

      // Detect blockquote `> ` prefix (#188). The Go parser does not strip
      // `> ` from clean_text, so the prefix is present in the raw text.
      // Number of `>` characters determines nesting depth (e.g. `>> ` = 2).
      const quoteMatch = text.match(/^(>+)\s(.*)$/)
      const isQuote = quoteMatch !== null
      const quoteDepth = isQuote ? quoteMatch![1].length : 1
      if (isQuote) body = quoteMatch![2]
      const contentNodes: NodeJSON[] = body ? legacyTokenizeInline(body) : []

      return {
        type: 'noteBlock',
        attrs: {
          id: block.id,
          depth: block.depth,
          bullet: isQuote ? '' : detectBullet(block.raw_text),
          align,
          quote: isQuote,
          quoteDepth: isQuote ? quoteDepth : 1,
          file_date: block.file_date || ''
        },
        content: contentNodes
      }
    }
  }
}

// Regex to detect a callout marker on a NOTE block's clean_text (#180):
// `> [!type] Title` where type is one of the seven variants.
const CALLOUT_RE =
  /^>\s*\[!(note|info|tip|warning|danger|success|quote)\]\s*(.*)$/i

// Detect if a clean_text is a body continuation of a callout (starts with `> ` but no [!type]).
function isCalloutBodyLine(text: string): boolean {
  // Accept `> text`, `>> nested`, and bare `>` (Obsidian blank line) but reject `> [!type]`.
  return /^>(\s|$|>)/.test(text) && !text.match(/^>\s*\[!/)
}

// ---- GFM pipe-table helpers (#172) ----------------------------------------

// GFM header row: `| col1 | col2 |`
const GFM_TABLE_ROW_RE = /^\|.*\|$/
// GFM separator row: `| --- | --- |` or `| :--- | ---: |`
const GFM_TABLE_SEP_RE = /^\|[\s:-]+(\|[\s:-]+)*\|\s*$/

// Detect if a clean_text is a GFM table row (pipe-delimited).
function isGFMTableRow(text: string): boolean {
  return GFM_TABLE_ROW_RE.test(text.trim())
}

// Detect if a line is a GFM separator row.
function isGFMTableSep(text: string): boolean {
  return GFM_TABLE_SEP_RE.test(text.trim())
}

// Parse a GFM table row (e.g. `| a | b |`) into cell strings.
function parseGFMRow(line: string): string[] {
  const trimmed = line.trim()
  // Strip leading and trailing |, split on |
  const inner = trimmed.startsWith('|') ? trimmed.slice(1) : trimmed
  const end = inner.endsWith('|') ? inner.slice(0, -1) : inner
  return end.split('|').map((s) => {
    const cell = s.trim()
    // Unescape escaped pipes (\| → |)
    return cell.replace(/\\\|/g, '|')
  })
}

// Build a GFM table row string from cell strings. Handles pipe escaping.
function buildGFMRow(cells: string[]): string {
  return '| ' + cells.map((c) => c.replace(/\|/g, '\\|')).join(' | ') + ' |'
}

// Serialize a TipTap table node JSON to GFM pipe rows for docToBlocks.
function tableToGFMRows(node: NodeJSON): string[] {
  const rows: string[] = []
  const content = node.content || []
  if (content.length === 0) return rows
  // First row is the header row.
  const headerCells = (content[0].content || []).map((cell: NodeJSON) => {
    const cellContent = cell.content || []
    return cellContent
      .map((n: NodeJSON) => {
        if (n.text !== undefined) return n.text || ''
        if (n.type === 'blockReferenceNode') return `((${n.attrs?.uuid || ''}))`
        if (n.type === 'embedNode') return `{{embed:${n.attrs?.uuid || ''}}}`
        return ''
      })
      .join('')
  })
  rows.push(buildGFMRow(headerCells))
  // Separator row: one `---` per column.
  const sep = '| ' + headerCells.map(() => '---').join(' | ') + ' |'
  rows.push(sep)
  // Data rows.
  for (let ri = 1; ri < content.length; ri++) {
    const row = content[ri]
    const cells = (row.content || []).map((cell: NodeJSON) => {
      const cellContent = cell.content || []
      return cellContent
        .map((n: NodeJSON) => {
          if (n.text !== undefined) return n.text || ''
          if (n.type === 'blockReferenceNode')
            return `((${n.attrs?.uuid || ''}))`
          if (n.type === 'embedNode') return `{{embed:${n.attrs?.uuid || ''}}}`
          return ''
        })
        .join('')
    })
    rows.push(buildGFMRow(cells))
  }
  return rows
}

// Helper to word-wrap text into ~80-char `> ` lines (#180). Preserves
// paragraph boundaries (double newlines) so multi-paragraph callout body
// content survives a round-trip without collapsing into one block.
function wrapCalloutBody(body: string, maxLen: number = 80): string[] {
  const paragraphs = body.split(/\n\n+/)
  const lines: string[] = []
  for (let pi = 0; pi < paragraphs.length; pi++) {
    if (pi > 0) lines.push('') // blank line between paragraphs
    const words = paragraphs[pi].split(' ')
    let current = ''
    for (const w of words) {
      if (current && current.length + w.length + 1 > maxLen) {
        lines.push(current)
        current = ''
      }
      current = current ? `${current} ${w}` : w
    }
    if (current) lines.push(current)
  }
  return lines
}

// blocksToDoc converts an ordered list of ParsedBlocks into a ProseMirror doc
// JSON suitable for editor.commands.setContent(). Each block becomes one
// top-level block node; nesting is expressed via the depth attr (rendered as
// indentation by the editor surface + NodeViews). Callout-style NOTE blocks
// with `> [!type]` prefix are merged into a single calloutBlock node (#180).
export function blocksToDoc(blocks: ParsedBlock[]): DocJSON {
  const content: NodeJSON[] = []
  let i = 0
  while (i < blocks.length) {
    const rawText = blocks[i].clean_text || ''

    // Multi-line <details> detection (#183). When a NOTE starts with <details>
    // but does not end with </details>, walk forward consuming subsequent NOTE
    // blocks until the closing tag is found.
    if (
      blocks[i].type === 'NOTE' &&
      rawText.trimStart().startsWith('<details>') &&
      !rawText.includes('</details>')
    ) {
      const parts: string[] = []
      while (i < blocks.length) {
        const ct = blocks[i].clean_text || ''
        parts.push(ct)
        if (ct.includes('</details>')) break
        i++
      }
      const merged = parts.join('\n')
      const summaryMatch = merged.match(/<summary>(.*?)<\/summary>/)
      const summary = summaryMatch ? summaryMatch[1] : ''
      const bodyContent = merged
        .replace(/<details><summary>.*?<\/summary>/, '')
        .replace(/<\/details>\s*$/, '')
      const bodyNodes: NodeJSON[] = bodyContent
        ? legacyTokenizeInline(bodyContent)
        : []
      content.push({
        type: 'detailsBlock',
        attrs: {
          id: blocks[i]?.id || crypto.randomUUID(),
          summary,
          open: false,
          file_date: blocks[i]?.file_date || ''
        },
        content: bodyNodes
      })
      i++
      continue
    }

    // GFM pipe table detection (#172). Consecutive NOTE blocks matching the
    // GFM pipe-table pattern are merged into a single tableBlock. Requires
    // at least a header row + separator row to be recognized.
    if (
      blocks[i].type === 'NOTE' &&
      isGFMTableRow(rawText) &&
      i + 1 < blocks.length &&
      isGFMTableSep(blocks[i + 1].clean_text || '')
    ) {
      const tableRows: string[] = []
      while (i < blocks.length && isGFMTableRow(blocks[i].clean_text || '')) {
        tableRows.push(blocks[i].clean_text || '')
        i++
      }
      const tableContent: NodeJSON[] = []
      // Parse header row
      if (tableRows.length > 0) {
        const headerCells = parseGFMRow(tableRows[0])
        const headerRow: NodeJSON = {
          type: 'tableRow',
          content: headerCells.map((cellText) => ({
            type: 'tableHeader',
            content: [{ type: 'text', text: cellText }]
          }))
        }
        tableContent.push(headerRow)
        // Data rows (skip the separator row at index 1)
        for (let di = 2; di < tableRows.length; di++) {
          const rowCells = parseGFMRow(tableRows[di])
          const dataRow: NodeJSON = {
            type: 'tableRow',
            content: rowCells.map((cellText) => ({
              type: 'tableCell',
              content: [{ type: 'text', text: cellText }]
            }))
          }
          tableContent.push(dataRow)
        }
      }
      content.push({
        type: 'table',
        attrs: { id: crypto.randomUUID() },
        content: tableContent
      })
      continue
    }

    const calloutMatch = rawText.match(CALLOUT_RE)
    if (calloutMatch) {
      const headerBlock = blocks[i]
      const variant = calloutMatch[1].toLowerCase()
      const title = calloutMatch[2] || ''
      const bodyParts: string[] = []
      i++
      // Merge subsequent callout body lines.
      while (
        i < blocks.length &&
        isCalloutBodyLine(blocks[i].clean_text || '')
      ) {
        const body = (blocks[i].clean_text || '').replace(/^>\s*/, '')
        if (body) {
          bodyParts.push(body)
        } else if (bodyParts.length > 0) {
          // Empty >  line — paragraph separator
          bodyParts.push('\n\n')
        }
        i++
      }
      const bodyText = bodyParts.join('')
      const bodyContent: NodeJSON[] = bodyText
        ? legacyTokenizeInline(bodyText)
        : []
      content.push({
        type: 'calloutBlock',
        attrs: {
          id: headerBlock.id || crypto.randomUUID(),
          variant,
          title,
          file_date: headerBlock.file_date || ''
        },
        content: bodyContent
      })
    } else {
      content.push(blockToNode(blocks[i]))
      i++
    }
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

    // Table block (#172): serialize as GFM pipe NOTE blocks. Each row becomes
    // one NOTE block; the header row is followed by a separator row.
    if (node.type === 'table') {
      const gfmRows = tableToGFMRows(node)
      for (let ri = 0; ri < gfmRows.length; ri++) {
        blocks.push({
          id: ri === 0 ? id : crypto.randomUUID(),
          parent_id: '',
          type: 'NOTE' as const,
          depth: 0,
          raw_text: gfmRows[ri],
          clean_text: gfmRows[ri],
          status: '',
          owner: '',
          start_date: '',
          due_date: '',
          priority: 3,
          line_number: lineNumber + ri,
          file_date: (attrs.file_date as string) || ''
        })
      }
      continue
    }

    // Details block (#183): serialize to <details><summary>...</summary>...</details>
    // in clean_text. The Go parser preserves HTML verbatim. Summary text is
    // HTML-escaped to prevent injection of markup characters.
    if (node.type === 'detailsBlock') {
      const baseText = serializeInlineContent(node.content)
      const summary = ((attrs.summary as string) || '')
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;')
      const html = `<details><summary>${summary}</summary>${baseText}</details>`
      blocks.push({
        id,
        parent_id: '',
        type: 'NOTE',
        depth: 0,
        raw_text: html,
        clean_text: html,
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

    // Code block (#189): serialize as a 'CODE' block. The Go RenderFileContent
    // handles the fenced syntax using clean_text (with preserved newlines) and
    // code_lang. The content is text* (plain text only, no marks).
    if (node.type === 'codeBlock') {
      const codeText = serializeInlineContent(node.content)
      blocks.push({
        id,
        parent_id: '',
        type: 'CODE',
        depth: 0,
        raw_text: '',
        clean_text: codeText,
        status: '',
        owner: '',
        start_date: '',
        due_date: '',
        priority: 3,
        code_lang: (attrs.lang as string) || '',
        line_number: lineNumber,
        file_date: (attrs.file_date as string) || ''
      })
      continue
    }

    // Callout block (#180): emit the `> [!variant] title` header line and one
    // `> body` line per word-wrapped segment of the body content.
    if (node.type === 'calloutBlock') {
      const variant: string = attrs.variant || 'note'
      const title: string = attrs.title || ''
      const baseText = serializeInlineContent(node.content)
      const headerLine = `> [!${variant}]${title ? ` ${title}` : ''}`
      blocks.push({
        id,
        parent_id: '',
        type: 'NOTE',
        depth: 0,
        raw_text: headerLine,
        clean_text: headerLine,
        status: '',
        owner: '',
        start_date: '',
        due_date: '',
        priority: 3,
        line_number: lineNumber,
        file_date: (attrs.file_date as string) || ''
      })
      if (baseText) {
        const bodyLines = wrapCalloutBody(baseText)
        for (let bi = 0; bi < bodyLines.length; bi++) {
          blocks.push({
            id: bi === 0 ? id : crypto.randomUUID(),
            parent_id: '',
            type: 'NOTE',
            depth: 0,
            raw_text: `> ${bodyLines[bi]}`,
            clean_text: `> ${bodyLines[bi]}`,
            status: '',
            owner: '',
            start_date: '',
            due_date: '',
            priority: 3,
            line_number: lineNumber + 1 + bi,
            file_date: (attrs.file_date as string) || ''
          })
        }
      }
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
      const isQuote = attrs.quote === true
      const bullet: string = isQuote
        ? ''
        : attrs.bullet !== undefined
          ? attrs.bullet
          : '- '
      if (isQuote) {
        const qd = Number(attrs.quoteDepth) || 1
        block.clean_text = `${new Array(qd + 1).join('>')} ${cleanText}`
      }
      block.raw_text = `${bullet}${block.clean_text}`
    } else {
      block.raw_text = `${'#'.repeat(block.depth || 1)} ${cleanText}`
    }

    blocks.push(block)
  }

  deriveParentIDs(blocks)
  return blocks
}
