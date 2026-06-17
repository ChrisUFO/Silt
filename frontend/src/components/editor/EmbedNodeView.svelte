<script lang="ts">
  import { NodeViewWrapper } from 'svelte-tiptap'
  import type { NodeViewProps } from '@tiptap/core'
  import EmbedPortal from '../EmbedPortal.svelte'

  let { node }: NodeViewProps = $props()
</script>

<NodeViewWrapper>
  <EmbedPortal uuid={node.attrs.uuid} />
</NodeViewWrapper>
