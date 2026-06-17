import { SvelteNodeViewRenderer } from 'svelte-tiptap'
import {
  TaskBlock,
  NoteBlock,
  HeaderBlock,
  EmbedNode,
  BlockReferenceNode
} from './schema'
import TaskBlockView from '../../components/editor/TaskBlockView.svelte'
import NoteBlockView from '../../components/editor/NoteBlockView.svelte'
import HeaderBlockView from '../../components/editor/HeaderBlockView.svelte'
import EmbedNodeView from '../../components/editor/EmbedNodeView.svelte'
import BlockReferenceNodeView from '../../components/editor/BlockReferenceNodeView.svelte'

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
  })
]
