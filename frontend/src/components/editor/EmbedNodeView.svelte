<script lang="ts">
  // NodeView wrapper for EmbedNode (#85). Passes the host context (notebook,
  // section, page, file_date) to the existing read-mode EmbedPortal. The
  // host fields come from the TipTap editor's editorProps; if not supplied
  // (e.g. in tests), defaults to empty strings.
  import { NodeViewWrapper } from 'svelte-tiptap'
  import type { NodeViewProps } from '@tiptap/core'
  import EmbedPortal from '../EmbedPortal.svelte'

  let { node }: NodeViewProps = $props()
</script>

<NodeViewWrapper>
  <EmbedPortal
    uuid={node.attrs.uuid}
    hostNotebook=""
    hostSection=""
    hostPage=""
    hostFileDate=""
  />
</NodeViewWrapper>
