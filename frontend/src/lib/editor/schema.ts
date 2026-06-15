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

import { Node, mergeAttributes } from '@tiptap/core'

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
        parseHTML: (el) => el.getAttribute('data-bullet') || '- ',
        renderHTML: (attrs) => ({ 'data-bullet': attrs.bullet })
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
