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

// Concatenate all inline text from a node's content, matching how the Go
// parser reconstructs clean_text from a block's text content. Smart Graph
// nodes (embedNode, blockReferenceNode) emit their textual form
// ({{embed:uuid}} and ((uuid)) respectively) so the on-disk file is
// round-trip identical (#85).
function inlineText(content?: NodeJSON[]): string {
  if (!content) return ''
  return content
    .map((child) => {
      if (child.text !== undefined) return child.text
      if (child.type === 'embedNode') {
        const uuid = (child.attrs?.uuid as string) || ''
        return `{{embed:${uuid}}}`
      }
      if (child.type === 'blockReferenceNode') {
        const uuid = (child.attrs?.uuid as string) || ''
        return `((${uuid}))`
      }
      if (child.content) return inlineText(child.content)
      return ''
    })
    .join('')
}

// Tokenize clean_text into an ordered list of inline nodes (text + Smart
// Graph nodes). Matches {{embed:uuid}} as an embedNode and ((uuid)) as a
// blockReferenceNode (#85). UUIDs follow the 8-4-4-4-12 hex pattern.
const SMART_GRAPH_TOKEN =
  /(\{\{embed:([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})\}\})|\(\(([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})\)\)/gi

function tokenizeInline(text: string): NodeJSON[] {
  const out: NodeJSON[] = []
  if (!text) return out
  let last = 0
  let match: RegExpExecArray | null
  SMART_GRAPH_TOKEN.lastIndex = 0
  while ((match = SMART_GRAPH_TOKEN.exec(text)) !== null) {
    if (match.index > last) {
      out.push({ type: 'text', text: text.slice(last, match.index) })
    }
    if (match[1]) {
      out.push({ type: 'embedNode', attrs: { uuid: match[2] } })
    } else if (match[3]) {
      out.push({ type: 'blockReferenceNode', attrs: { uuid: match[3] } })
    }
    last = match.index + match[0].length
  }
  if (last < text.length) {
    out.push({ type: 'text', text: text.slice(last) })
  }
  return out
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
}): string {
  return `<!-- silt-embed: ${JSON.stringify(attrs)} -->`
}

export function parseEmbedBlockMarker(
  text: string
): {
  embedType: string
  src: string
  caption?: string
  openable?: boolean
  pluginID?: string
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
  const text = block.clean_text || ''

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
    // emits it verbatim, so the on-disk file round-trips.
    if (node.type === 'embedBlock') {
      const marker = embedBlockMarker({
        embedType: attrs.embedType || '',
        src: attrs.src || '',
        caption: attrs.caption || undefined,
        openable: attrs.openable || undefined,
        pluginID: attrs.pluginID || undefined
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

    const cleanText = inlineText(node.content)

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
