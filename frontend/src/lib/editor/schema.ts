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
import { newlineInCode } from '@tiptap/pm/commands'
import Highlight from '@tiptap/extension-highlight'
import {
  Details,
  DetailsContent,
  DetailsSummary
} from '@tiptap/extension-details'
import { Table } from '@tiptap/extension-table'
import { TableRow } from '@tiptap/extension-table-row'
import { TableCell } from '@tiptap/extension-table-cell'
import { TableHeader } from '@tiptap/extension-table-header'
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
        handler: (({
          state,
          range,
          match
        }: Parameters<NonNullable<InputRule['handler']>>[0]) => {
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
      // Blockquote prefix (#188). A `> ` (or nested `>> `, `>>> `) prefix is
      // just another recognized marker style, parallel to `bullet`. Empty
      // string = not a quote. The marker is stored verbatim so the exact `>`
      // run round-trips and the NodeView can render nested left borders.
      quote: {
        default: '',
        parseHTML: (el) => el.getAttribute('data-quote') || '',
        renderHTML: (attrs) =>
          attrs.quote ? { 'data-quote': attrs.quote } : {}
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
        handler: (({
          state,
          range,
          match
        }: Parameters<NonNullable<InputRule['handler']>>[0]) => {
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
        handler: (({
          state,
          range,
          match
        }: Parameters<NonNullable<InputRule['handler']>>[0]) => {
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

// ---- CalloutBlock (#180) -------------------------------------------------
// An Obsidian/GFM admonition: a `>`-prefixed line whose marker is `[!variant]`.
// On-disk it is `> [!variant] message`; the converter detects the marker and
// emits this node, stripping the prefix from the displayed text. The variant
// drives the icon + accent color (CALLOUT_VARIANTS). Pure-frontend: the Go
// parser sees the line as an opaque NOTE whose clean_text starts with
// `> [!variant]`, exactly like a quote — no parser change.
export type CalloutVariant =
  | 'note'
  | 'info'
  | 'tip'
  | 'warning'
  | 'danger'
  | 'success'
  | 'quote'

export const CALLOUT_VARIANTS: Record<
  CalloutVariant,
  { icon: string; label: string; accent: string; role: string }
> = {
  note: {
    icon: 'info',
    label: 'Note',
    accent: 'var(--color-accent-primary-start)',
    role: 'note'
  },
  info: {
    icon: 'campaign',
    label: 'Info',
    accent: 'var(--color-accent-secondary-start)',
    role: 'note'
  },
  tip: {
    icon: 'lightbulb',
    label: 'Tip',
    accent: 'var(--color-status-success, #30a46c)',
    role: 'note'
  },
  warning: {
    icon: 'warning',
    label: 'Warning',
    accent: 'var(--color-status-warn, #f5a623)',
    role: 'note'
  },
  danger: {
    icon: 'error',
    label: 'Danger',
    accent: 'var(--color-status-danger, #e5484d)',
    role: 'alert'
  },
  success: {
    icon: 'check_circle',
    label: 'Success',
    accent: 'var(--color-status-success, #30a46c)',
    role: 'status'
  },
  quote: {
    icon: 'format_quote',
    label: 'Quote',
    accent: 'var(--color-text-muted)',
    role: 'blockquote'
  }
}

export const CalloutBlock = Node.create({
  name: 'calloutBlock',
  group: 'block',
  // block+ lets a callout contain arbitrary block children (task lists, code
  // blocks, tables, nested callouts) like Obsidian and <details> already do.
  // This is safe because the serializer (serializeCalloutToText) emits an
  // explicit branch for every allowed block type — the same no-silent-drop
  // guarantee <details> relies on — so block content round-trips through the
  // `>`-prefixed on-disk lines instead of being flattened or lost. Plain body
  // lines parse as paragraphs, so legacy multi-paragraph callouts stay
  // byte-identical.
  content: 'block+',
  defining: true,
  isolating: true,

  addAttributes() {
    return {
      id: {
        default: null,
        parseHTML: (el) => el.getAttribute('data-id') || null,
        renderHTML: (attrs) => (attrs.id ? { 'data-id': attrs.id } : {})
      },
      variant: {
        default: 'note',
        parseHTML: (el) => el.getAttribute('data-variant') || 'note',
        renderHTML: (attrs) => ({ 'data-variant': attrs.variant })
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
    return [{ tag: 'div[data-type="callout"]' }]
  },

  renderHTML({ HTMLAttributes }) {
    return [
      'div',
      mergeAttributes({ 'data-type': 'callout' }, HTMLAttributes),
      0
    ]
  }
})

// ---- CodeBlock (#189) ----------------------------------------------------
// A managed fenced code block. The Go parser emits a BlockCode ParsedBlock
// whose clean_text retains internal newlines (parser.go); this node carries
// that text as plain `text*` content and a `language` attr (the info string).
// `code: true` disables inline marks inside (code is literal). Enter inserts a
// newline rather than a new block (newlineInCode). On-disk render goes through
// Go's renderBlock BlockCode branch, which emits the ``` ``` fence verbatim.
export const CodeBlock = Node.create({
  name: 'codeBlock',
  group: 'block',
  content: 'text*',
  defining: true,
  isolating: true,
  code: true,

  addAttributes() {
    return {
      id: {
        default: null,
        parseHTML: (el) => el.getAttribute('data-id') || null,
        renderHTML: (attrs) => (attrs.id ? { 'data-id': attrs.id } : {})
      },
      language: {
        default: '',
        parseHTML: (el) => el.getAttribute('data-language') || '',
        renderHTML: (attrs) =>
          attrs.language ? { 'data-language': attrs.language } : {}
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
    return [{ tag: 'div[data-type="code"]' }, { tag: 'pre[data-type="code"]' }]
  },

  renderHTML({ HTMLAttributes }) {
    return ['div', mergeAttributes({ 'data-type': 'code' }, HTMLAttributes), 0]
  },

  addKeyboardShortcuts() {
    return {
      // Enter inserts a newline inside the code instead of creating a new
      // block below — the defining behaviour of a code block.
      Enter: () => newlineInCode(this.editor.state, this.editor.view.dispatch)
    }
  }
})

// ---- Foldable details (#183) ---------------------------------------------
// TipTap's Details extension renders a native `<details><summary>` container,
// which gives free disclosure behaviour (click summary to toggle), keyboard
// operability, and an implicit aria-expanded. The on-disk form is the HTML
// itself, preserved line-by-line as opaque NOTE blocks by the Go parser (HTML
// passes through clean_text verbatim). The converter groups a `<details>` run
// into this node tree on load and re-emits the run on save. Collapse state is
// ephemeral in v1 (never written as `<details open>`).
//
// `open: false` default keeps sections collapsed on load (the outliner's "the
// file is the truth" model — collapse is a view concern, not persisted).
// The extensions are wrapped with `.extend()` to add `id` and `file_date`
// attributes — without these, ProseMirror silently drops the attrs on
// node creation, causing identity instability across save cycles (#310).
export const SiltDetailsExtensions = [
  Details.extend({
    addAttributes() {
      return {
        ...this.parent?.(),
        id: {
          default: null,
          parseHTML: (el) => el.getAttribute('data-id') || null,
          renderHTML: (attrs) => (attrs.id ? { 'data-id': attrs.id } : {})
        },
        file_date: {
          default: '',
          parseHTML: (el) => el.getAttribute('data-file-date') || '',
          renderHTML: (attrs) =>
            attrs.file_date ? { 'data-file-date': attrs.file_date } : {}
        }
      }
    }
  }).configure({ HTMLAttributes: { 'data-type': 'details' } }),
  DetailsSummary,
  DetailsContent
]

// ---- GFM Tables (#172) ---------------------------------------------------
// TipTap's Table family renders an editable grid with native Tab/arrow-key
// navigation, column resizing, and cell selection. The on-disk form is
// standard GFM pipe syntax; the converter groups a run of pipe-prefixed NOTE
// blocks (one per GFM line) into this node tree on load and re-emits the run
// on save. resizable lets the user drag column borders; the table's block
// identity lives on the LAST row (so the whole table has one id).
export const SiltTableExtensions = [
  Table.extend({
    addAttributes() {
      return {
        ...this.parent?.(),
        id: {
          default: null,
          parseHTML: (el) => el.getAttribute('data-id') || null,
          renderHTML: (attrs) => (attrs.id ? { 'data-id': attrs.id } : {})
        },
        file_date: {
          default: '',
          parseHTML: (el) => el.getAttribute('data-file-date') || '',
          renderHTML: (attrs) =>
            attrs.file_date ? { 'data-file-date': attrs.file_date } : {}
        }
      }
    }
  }).configure({
    resizable: true,
    allowTableNodeSelection: false,
    HTMLAttributes: { 'data-type': 'table' }
  }),
  TableRow,
  TableCell,
  TableHeader
]

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

// ---- MathNode (inline + block, atomic) -----------------------------------
// Renders LaTeX math via KaTeX (#191). Two nodes share one attr (`latex`,
// the raw source): InlineMathNode (`$...$`, inline atomic) and BlockMathNode
// (`$$...$$`, block atomic). The raw LaTeX round-trips through clean_text;
// only the view differs. No official TipTap math package is used — Silt's own
// converter/NodeView pipeline makes a custom node the cleaner fit.
export const InlineMathNode = Node.create({
  name: 'inlineMathNode',
  group: 'inline',
  inline: true,
  atom: true,
  selectable: true,
  draggable: false,

  addAttributes() {
    return {
      latex: {
        default: '',
        parseHTML: (el) => el.getAttribute('data-latex') || '',
        renderHTML: (attrs) =>
          attrs.latex ? { 'data-latex': attrs.latex } : {}
      }
    }
  },

  parseHTML() {
    return [{ tag: 'span[data-type="math-inline"]' }]
  },

  renderHTML({ HTMLAttributes }) {
    return [
      'span',
      mergeAttributes({ 'data-type': 'math-inline' }, HTMLAttributes)
    ]
  }
})

export const BlockMathNode = Node.create({
  name: 'blockMathNode',
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
      latex: {
        default: '',
        parseHTML: (el) => el.getAttribute('data-latex') || '',
        renderHTML: (attrs) =>
          attrs.latex ? { 'data-latex': attrs.latex } : {}
      }
    }
  },

  parseHTML() {
    return [{ tag: 'div[data-type="math-block"]' }]
  },

  renderHTML({ HTMLAttributes }) {
    return [
      'div',
      mergeAttributes({ 'data-type': 'math-block' }, HTMLAttributes)
    ]
  }
})

// ---- MentionNode (inline, atomic) ----------------------------------------
// Renders an @-mention chip (`@[name]`) for inline owner references (#184).
// Inline + atomic like BlockReferenceNode; the `name` attr is the owner label
// and the textual form `@[name]` is reconstructed in clean_text on save. The
// suggestion list comes from the read-only DistinctOwners index projection —
// no mention state lives in SQLite.
export const MentionNode = Node.create({
  name: 'mentionNode',
  group: 'inline',
  inline: true,
  atom: true,
  selectable: true,
  draggable: false,

  addAttributes() {
    return {
      name: {
        default: '',
        parseHTML: (el) => el.getAttribute('data-name') || '',
        renderHTML: (attrs) => (attrs.name ? { 'data-name': attrs.name } : {})
      }
    }
  },

  parseHTML() {
    return [{ tag: 'span[data-type="mention"]' }]
  },

  renderHTML({ HTMLAttributes }) {
    return ['span', mergeAttributes({ 'data-type': 'mention' }, HTMLAttributes)]
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
