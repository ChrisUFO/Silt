import { SvelteNodeViewRenderer } from 'svelte-tiptap'
import { TaskBlock, NoteBlock, HeaderBlock } from './schema'
import TaskBlockView from '../../components/editor/TaskBlockView.svelte'
import NoteBlockView from '../../components/editor/NoteBlockView.svelte'
import HeaderBlockView from '../../components/editor/HeaderBlockView.svelte'

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
  })
]
