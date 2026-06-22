<script lang="ts">
  let {
    notebook,
    section,
    page,
    blocks,
    activeFocusedBlockAncestors,
    onBlockFocus,
    onBlockBlur,
    onUpdate,
    editorInstance,
    activeMarks,
    viewMode,
    onSaveStateChange
  } = $props()
</script>

<div data-testid="tiptap-stub" data-page={page}></div>
