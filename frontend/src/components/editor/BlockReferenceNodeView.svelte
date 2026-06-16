<script lang="ts">
  // NodeView wrapper for BlockReferenceNode (#85). Thin pass-through to the
  // existing read-mode BlockReferenceChip; the chip handles resolution +
  // navigation on click.
  import { NodeViewWrapper } from 'svelte-tiptap'
  import type { NodeViewProps } from '@tiptap/core'
  import BlockReferenceChip from '../BlockReferenceChip.svelte'

  let { node }: NodeViewProps = $props()
</script>

<NodeViewWrapper>
  <BlockReferenceChip uuid={node.attrs.uuid} />
</NodeViewWrapper>
