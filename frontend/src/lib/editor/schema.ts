// ProseMirror / TipTap node schema for Silt's block model.
//
// Each Silt block type (TASK / NOTE / HEADER) maps to a ProseMirror block node
// whose attrs carry the block identity and metadata. The editor surface (Phase 3)
// and NodeViews (Phase 4/5) build on this schema; this file only defines the
// node types and their attrs so the converter layer (converters.ts) can
// produce/consume doc JSON that a TipTap editor accepts.
//
// Design notes:
// - `id` is the block UUID (parser.ParsedBlock.ID). It is the node identity, NOT
//   content. Pasted/duplicated nodes get a fresh UUID via uniqueIdPlugin.ts so
//   the blocks-table PK and embed/reference resolution stay consistent.
// - `parent_id` is NOT a node attr: it is derived from `depth` via the same
//   stack-walk algorithm the Go parser uses (parser.go activeIDs). Keeping a
//   single source of truth (depth) avoids drift between stored parent_id and
//   actual nesting.
// - `line_number` is NOT a node attr: it is reassigned by doc order in
//   docToBlocks. Line numbers are positional, not semantic.
// - Notes carry a `bullet` attr ('- ', '* ', '+ ', or '' for plain prose) so
//   the Go serializer (renderBlock) preserves the original bullet marker.
//   The editor-created default is '- ' (matching renderBlock's default).

import { Node, Mark, mergeAttributes, InputRule } from '@tiptap/core'
import type { Transaction } from 'prosemirror-state'
import Highlight from '@tiptap/extension-highlight'
import Subscript from '@tiptap/extension-subscript'
import Superscript from '@tiptap/extension-superscript'

// ---- Inline mark extensions ----------------------------------------------
// TipTap 3.x StarterKit includes Bold, Italic, Strike, Code, Link, and
// Underline marks by default. These three (Highlight, Subscript, Superscript)
// are NOT in StarterKit and must be added explicitly. They are composed into
// the editor's extension array alongside StarterKit.
//
// On-disk serialization:
//   highlight   → ==text==
//   subscript   → <sub>text</sub>
//   superscript → <sup>text</sup>
// All three round-trip through clean_text (the Go parser preserves HTML/tags
// and ==...== verbatim — clean_text is opaque to Go). The converter
// (converters.ts) handles parse/serialize for all 9 marks symmetrically.
export const SiltInlineMarkExtensions = [Highlight, Subscript, Superscript]

// ---- TextColor mark (#170) -----------------------------------------------
// Inline character color. Serialized on-disk as
// `<span style="color: #hex">text</span>`. The Go parser preserves HTML in
// clean_text verbatim, so the span round-trips with zero parser changes.
export const TextColor = Mark.create({
  name: 'textColor',
  inclusive: true,
  addAttributes() {
    return {
      color: {
        default: null,
        parseHTML: (el) => (el as HTMLElement).style.color?.trim() || null,
        renderHTML: (attrs) =>
          attrs.color ? { style: `color: ${attrs.color}` } : {}
      }
    }
  },
  parseHTML() {
    return [
      {
        tag: 'span[style]',
        getAttrs: (el) => {
          const color = (el as HTMLElement).style.color
          return color ? { color: color.trim() } : false
        }
      }
    ]
  },
  renderHTML({ HTMLAttributes }) {
    return ['span', mergeAttributes(HTMLAttributes), 0]
  }
})

// ---- BackgroundColor mark (#170) ------------------------------------------
// Inline background (highlighter) color. Serialized on-disk as
// `<span style="background-color: #hex">text</span>`. Separate from the
// Highlight mark (==text==, which uses the theme accent color).
export const BackgroundColor = Mark.create({
  name: 'backgroundColor',
  inclusive: true,
  addAttributes() {
    return {
      color: {
        default: null,
        parseHTML: (el) =>
          (el as HTMLElement).style.backgroundColor?.trim() || null,
        renderHTML: (attrs) =>
          attrs.color ? { style: `background-color: ${attrs.color}` } : {}
      }
    }
  },
  parseHTML() {
    return [
      {
        tag: 'span[style]',
        getAttrs: (el) => {
          const bg = (el as HTMLElement).style.backgroundColor
          return bg ? { color: bg.trim() } : false
        }
      }
    ]
  },
  renderHTML({ HTMLAttributes }) {
    return ['span', mergeAttributes(HTMLAttributes), 0]
  }
})

export const SiltColorMarkExtensions = [TextColor, BackgroundColor]

// ---- TaskBlock -----------------------------------------------------------
// Renders on-disk as:
//   <indent>- [x] STATUS TASK [owner](start,due)#priority desc <!-- id: uuid -->
export const TaskBlock = Node.create({
  name: 'taskBlock',
  group: 'block',
  content: 'inline*',
  defining: true,
  isolating: true,

  addAttributes() {
    return {
      id: {
        default: null,
        parseHTML: (el) => el.getAttribute('data-id') || null,
        renderHTML: (attrs) => (attrs.id ? { 'data-id': attrs.id } : {})
      },
      depth: {
        default: 0,
        parseHTML: (el) => Number(el.getAttribute('data-depth') || 0),
        renderHTML: (attrs) => ({ 'data-depth': String(attrs.depth) })
      },
      status: {
        default: 'TODO',
        parseHTML: (el) => el.getAttribute('data-status') || 'TODO',
        renderHTML: (attrs) => ({ 'data-status': attrs.status })
      },
      owner: {
        default: '',
        parseHTML: (el) => el.getAttribute('data-owner') || '',
        renderHTML: (attrs) =>
          attrs.owner ? { 'data-owner': attrs.owner } : {}
      },
      start_date: {
        default: '',
        parseHTML: (el) => el.getAttribute('data-start-date') || '',
        renderHTML: (attrs) =>
          attrs.start_date ? { 'data-start-date': attrs.start_date } : {}
      },
      due_date: {
        default: '',
        parseHTML: (el) => el.getAttribute('data-due-date') || '',
        renderHTML: (attrs) =>
          attrs.due_date ? { 'data-due-date': attrs.due_date } : {}
      },
      priority: {
        default: 3,
        parseHTML: (el) => Number(el.getAttribute('data-priority') || 3),
        renderHTML: (attrs) => ({ 'data-priority': String(attrs.priority) })
      },
      file_date: {
        default: '',
        parseHTML: (el) => el.getAttribute('data-file-date') || '',
        renderHTML: (attrs) =>
          attrs.file_date ? { 'data-file-date': attrs.file_date } : {}
      }
    }
  },

  parseHTML() {
    return [{ tag: 'div[data-type="task"]' }]
  },

  renderHTML({ HTMLAttributes }) {
    return ['div', mergeAttributes({ 'data-type': 'task' }, HTMLAttributes), 0]
  },

  addInputRules() {
    return [
      // Markdown-style checkbox shortcut: typing "[]", "[ ]", or "[x]"
      // followed by a space converts the current block into a task (#262).
      // Matches inside a noteBlock or headerBlock; the matched text is
      // deleted and the node type is swapped via setNodeMarkup so inline
      // content survives.
      new InputRule({
        find: /^\s*\[([ xX]?)]\s$/,
        handler: (({ state, range, match }) => {
          const $start = state.doc.resolve(range.from)
          if ($start.parentOffset !== range.from - $start.start()) {
            return null
          }
          const depth = $start.depth
          const nodePos = $start.before(depth)
          const node = $start.node(depth)
          if (
            node.type.name !== 'noteBlock' &&
            node.type.name !== 'headerBlock'
          ) {
            return null
          }
          const taskType = state.schema.nodes.taskBlock
          const isDone = match[1] === 'x' || match[1] === 'X'
          const tr = state.tr.delete(range.from, range.to)
          tr.setNodeMarkup(nodePos, taskType, {
            id: node.attrs.id,
            depth: node.attrs.depth || 0,
            status: isDone ? 'DONE' : 'TODO',
            owner: '',
            start_date: '',
            due_date: '',
            priority: 3,
            file_date: node.attrs.file_date || ''
          })
          return tr
        }) as unknown as InputRule['handler']
      })
    ]
  }
})

// ---- NoteBlock -----------------------------------------------------------
// Renders on-disk as: <indent><bullet?>desc <!-- id: uuid -->
export const NoteBlock = Node.create({
  name: 'noteBlock',
  group: 'block',
  content: 'inline*',
  defining: true,
  isolating: true,

  addAttributes() {
    return {
      id: {
        default: null,
        parseHTML: (el) => el.getAttribute('data-id') || null,
        renderHTML: (attrs) => (attrs.id ? { 'data-id': attrs.id } : {})
      },
      depth: {
        default: 0,
        parseHTML: (el) => Number(el.getAttribute('data-depth') || 0),
        renderHTML: (attrs) => ({ 'data-depth': String(attrs.depth) })
      },
      bullet: {
        // '- ' (default), '* ', '+ ', or '' (plain prose, no bullet marker)
        default: '- ',
        parseHTML: (el) => {
          const b = el.getAttribute('data-bullet')
          return b !== null ? b : '- '
        },
        renderHTML: (attrs) => ({ 'data-bullet': attrs.bullet })
      },
      // Block-level text alignment (#173). Only NOTE and HEADER support it;
      // TASK blocks do not get this attr. Default 'left' = no marker emitted.
      align: {
        default: 'left',
        parseHTML: (el) => el.getAttribute('data-align') || 'left',
        renderHTML: (attrs) =>
          attrs.align && attrs.align !== 'left'
            ? { 'data-align': attrs.align }
            : {}
      },
      file_date: {
        default: '',
        parseHTML: (el) => el.getAttribute('data-file-date') || '',
        renderHTML: (attrs) =>
          attrs.file_date ? { 'data-file-date': attrs.file_date } : {}
      }
    }
  },

  parseHTML() {
    return [{ tag: 'div[data-type="note"]' }]
  },

  renderHTML({ HTMLAttributes }) {
    return ['div', mergeAttributes({ 'data-type': 'note' }, HTMLAttributes), 0]
  },

  addInputRules() {
    return [
      new InputRule({
        find: /^\s*([-*+])\s$/,
        handler: (({ state, range, match }) => {
          const $start = state.doc.resolve(range.from)
          if ($start.parentOffset !== range.from - $start.start()) {
            return null
          }
          const depth = $start.depth
          const nodePos = $start.before(depth)
          const node = $start.node(depth)
          if (node.type.name !== 'noteBlock') {
            return null
          }
          const tr = state.tr.delete(range.from, range.to)
          tr.setNodeAttribute(nodePos, 'bullet', match[1] + ' ')
          return tr
        }) as unknown as InputRule['handler']
      }),
      new InputRule({
        find: /^\s*(\d+[.)])\s$/,
        handler: (({ state, range, match }) => {
          const $start = state.doc.resolve(range.from)
          if ($start.parentOffset !== range.from - $start.start()) {
            return null
          }
          const depth = $start.depth
          const nodePos = $start.before(depth)
          const node = $start.node(depth)
          if (node.type.name !== 'noteBlock') {
            return null
          }
          const tr = state.tr.delete(range.from, range.to)
          tr.setNodeAttribute(nodePos, 'bullet', match[1] + ' ')
          return tr
        }) as unknown as InputRule['handler']
      })
    ]
  }
})

// ---- HeaderBlock ---------------------------------------------------------
// Renders on-disk as: <hashes> desc <!-- id: uuid -->
// `depth` is the heading level (1-6), matching block.Depth for headers.
export const HeaderBlock = Node.create({
  name: 'headerBlock',
  group: 'block',
  content: 'inline*',
  defining: true,
  isolating: true,

  addAttributes() {
    return {
      id: {
        default: null,
        parseHTML: (el) => el.getAttribute('data-id') || null,
        renderHTML: (attrs) => (attrs.id ? { 'data-id': attrs.id } : {})
      },
      depth: {
        default: 1,
        parseHTML: (el) => Number(el.getAttribute('data-depth') || 1),
        renderHTML: (attrs) => ({ 'data-depth': String(attrs.depth) })
      },
      // Block-level text alignment (#173).
      align: {
        default: 'left',
        parseHTML: (el) => el.getAttribute('data-align') || 'left',
        renderHTML: (attrs) =>
          attrs.align && attrs.align !== 'left'
            ? { 'data-align': attrs.align }
            : {}
      },
      file_date: {
        default: '',
        parseHTML: (el) => el.getAttribute('data-file-date') || '',
        renderHTML: (attrs) =>
          attrs.file_date ? { 'data-file-date': attrs.file_date } : {}
      }
    }
  },

  parseHTML() {
    return [{ tag: 'div[data-type="header"]' }]
  },

  renderHTML({ HTMLAttributes }) {
    return [
      'div',
      mergeAttributes({ 'data-type': 'header' }, HTMLAttributes),
      0
    ]
  }
})

// Silt-specific block extensions. NoteBlock MUST be first — ProseMirror uses
// the first registered block type as the "default" for content normalization.
// NoteBlock (plain text) is the natural default; TaskBlock and HeaderBlock
// are opt-in types the user creates via the slash menu.
export const SiltBlockExtensions = [NoteBlock, TaskBlock, HeaderBlock]

// ---- EmbedNode (block-level, atomic) -------------------------------------
// Renders Smart Graph `{{embed:uuid}}` as a live EmbedPortal NodeView (#85).
// Atomic (no editable children); the NodeView fetches the referenced block
// via ResolveBlockReference and renders it as a nested live portal. The
// `uuid` attr is the block UUID; the textual form `{{embed:uuid}}` is
// reconstructed in clean_text on save.
export const EmbedNode = Node.create({
  name: 'embedNode',
  group: 'block',
  atom: true,
  selectable: true,
  draggable: false,

  addAttributes() {
    return {
      id: {
        default: null,
        parseHTML: (el) => el.getAttribute('data-id') || null,
        renderHTML: (attrs) => (attrs.id ? { 'data-id': attrs.id } : {})
      },
      uuid: {
        default: '',
        parseHTML: (el) => el.getAttribute('data-uuid') || '',
        renderHTML: (attrs) => (attrs.uuid ? { 'data-uuid': attrs.uuid } : {})
      }
    }
  },

  parseHTML() {
    return [{ tag: 'div[data-type="embed"]' }]
  },

  renderHTML({ HTMLAttributes }) {
    return ['div', mergeAttributes({ 'data-type': 'embed' }, HTMLAttributes)]
  }
})

// ---- BlockReferenceNode (inline, atomic) --------------------------------
// Renders Smart Graph `((uuid))` as an inline BlockReferenceChip NodeView
// (#85). Inline (sits inside a noteBlock's content); clicking the chip
// navigates to the referenced block. The `uuid` attr is the block UUID;
// the textual form `((uuid))` is reconstructed in clean_text on save.
export const BlockReferenceNode = Node.create({
  name: 'blockReferenceNode',
  group: 'inline',
  inline: true,
  atom: true,
  selectable: true,
  draggable: false,

  addAttributes() {
    return {
      uuid: {
        default: '',
        parseHTML: (el) => el.getAttribute('data-uuid') || '',
        renderHTML: (attrs) => (attrs.uuid ? { 'data-uuid': attrs.uuid } : {})
      }
    }
  },

  parseHTML() {
    return [{ tag: 'span[data-type="block-ref"]' }]
  },

  renderHTML({ HTMLAttributes }) {
    return [
      'span',
      mergeAttributes({ 'data-type': 'block-ref' }, HTMLAttributes)
    ]
  }
})

// ---- EmbedBlockNode (generic plugin-extensible block, #110 Phase 1) ---------
// A generic block-level atomic node that plugins specialize via attrs (type,
// src, caption, openable, pluginID, data). Covers attachments (#101), image
// embeds, link cards, and most custom-block use cases without requiring a
// static ProseMirror schema change per plugin.
//
// Serialization round-trip: the node serializes to a NOTE block whose clean_text
// is an HTML-comment marker `<!-- silt-embed: {json attrs} -->`, consistent with
// the existing `<!-- id: uuid @ date -->` convention. The Go parser preserves
// the marker as the NOTE's clean_text (it only strips the trailing id comment),
// so the on-disk file round-trips byte-for-byte.
export interface EmbedBlockAttrs {
  embedType: string // "attachment" | "image" | plugin-defined
  src: string // relative path / url / identifier
  caption?: string
  openable?: boolean
  pluginID?: string
  data?: Record<string, unknown> // arbitrary plugin-specific attrs
}

export const SiltEmbedMarker = 'silt-embed'

export const EmbedBlockNode = Node.create({
  name: 'embedBlock',
  group: 'block',
  atom: true,
  selectable: true,
  draggable: true,

  addAttributes() {
    return {
      embedType: {
        default: '',
        parseHTML: (el) => el.getAttribute('data-embed-type') || '',
        renderHTML: (attrs) =>
          attrs.embedType ? { 'data-embed-type': attrs.embedType } : {}
      },
      src: {
        default: '',
        parseHTML: (el) => el.getAttribute('data-src') || '',
        renderHTML: (attrs) => (attrs.src ? { 'data-src': attrs.src } : {})
      },
      caption: {
        default: '',
        parseHTML: (el) => el.getAttribute('data-caption') || '',
        renderHTML: (attrs) =>
          attrs.caption ? { 'data-caption': attrs.caption } : {}
      },
      openable: {
        default: false,
        parseHTML: (el) => el.getAttribute('data-openable') === 'true',
        renderHTML: (attrs) =>
          attrs.openable ? { 'data-openable': 'true' } : {}
      },
      pluginID: {
        default: '',
        parseHTML: (el) => el.getAttribute('data-plugin') || '',
        renderHTML: (attrs) =>
          attrs.pluginID ? { 'data-plugin': attrs.pluginID } : {}
      }
    }
  },

  parseHTML() {
    return [{ tag: 'div[data-type="embed-block"]' }]
  },

  renderHTML({ HTMLAttributes }) {
    return [
      'div',
      mergeAttributes({ 'data-type': 'embed-block' }, HTMLAttributes)
    ]
  }
})
