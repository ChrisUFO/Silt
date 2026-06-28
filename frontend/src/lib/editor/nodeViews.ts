import { SvelteNodeViewRenderer } from 'svelte-tiptap'
import {
  TaskBlock,
  NoteBlock,
  HeaderBlock,
  EmbedNode,
  BlockReferenceNode,
  MentionNode,
  InlineMathNode,
  BlockMathNode,
  EmbedBlockNode,
  CalloutBlock,
  CodeBlock
} from './schema'
import TaskBlockView from '../../components/editor/TaskBlockView.svelte'
import NoteBlockView from '../../components/editor/NoteBlockView.svelte'
import HeaderBlockView from '../../components/editor/HeaderBlockView.svelte'
import EmbedNodeView from '../../components/editor/EmbedNodeView.svelte'
import BlockReferenceNodeView from '../../components/editor/BlockReferenceNodeView.svelte'
import MentionNodeView from '../../components/editor/MentionNodeView.svelte'
import MathNodeView from '../../components/editor/MathNodeView.svelte'
import EmbedBlockNodeView from '../../components/editor/EmbedBlockNodeView.svelte'
import CalloutBlockView from '../../components/editor/CalloutBlockView.svelte'
import CodeBlockView from '../../components/editor/CodeBlockView.svelte'

// Production extensions: the base schema nodes extended with Svelte NodeView
// rendering. NoteBlock first — it's the default block type (see schema.ts).
export const SiltBlockExtensionsWithNodeViews = [
  NoteBlock.extend({
    addNodeView() {
      return SvelteNodeViewRenderer(NoteBlockView)
    }
  }),
  TaskBlock.extend({
    addNodeView() {
      return SvelteNodeViewRenderer(TaskBlockView)
    }
  }),
  HeaderBlock.extend({
    addNodeView() {
      return SvelteNodeViewRenderer(HeaderBlockView)
    }
  }),
  // Smart Graph NodeViews (#85). EmbedNode is a block-level atomic node that
  // renders {{embed:uuid}} as a live portal; BlockReferenceNode is an inline
  // atomic node that renders ((uuid)) as a clickable chip.
  EmbedNode.extend({
    addNodeView() {
      return SvelteNodeViewRenderer(EmbedNodeView)
    }
  }),
  BlockReferenceNode.extend({
    addNodeView() {
      return SvelteNodeViewRenderer(BlockReferenceNodeView)
    }
  }),
  // @-mention chip (#184). Inline atomic node rendering @[name] as a
  // non-editable chip; the suggestion list comes from DistinctOwners.
  MentionNode.extend({
    addNodeView() {
      return SvelteNodeViewRenderer(MentionNodeView)
    }
  }),
  // KaTeX math (#191). One NodeView serves inline ($...$) and block ($$...$$);
  // displayMode follows the node type.
  InlineMathNode.extend({
    addNodeView() {
      return SvelteNodeViewRenderer(MathNodeView)
    }
  }),
  BlockMathNode.extend({
    addNodeView() {
      return SvelteNodeViewRenderer(MathNodeView)
    }
  }),
  // Generic plugin-extensible embed block (#110). The default NodeView renders
  // a minimal card; plugins with custom embed types provide their own NodeView.
  EmbedBlockNode.extend({
    addNodeView() {
      return SvelteNodeViewRenderer(EmbedBlockNodeView)
    }
  }),
  // Callout / admonition (#180). A `> [!variant]` block rendered as an
  // iconified, accent-bordered box with editable inline content.
  CalloutBlock.extend({
    addNodeView() {
      return SvelteNodeViewRenderer(CalloutBlockView)
    }
  }),
  // Fenced code block (#189). A dual-layer NodeView (transparent editable text
  // over a Shiki-highlighted layer) provides syntax highlighting while keeping
  // the content natively editable.
  CodeBlock.extend({
    addNodeView() {
      return SvelteNodeViewRenderer(CodeBlockView)
    }
  })
]
