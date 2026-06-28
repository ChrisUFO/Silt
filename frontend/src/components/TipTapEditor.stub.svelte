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
    onSaveStateChange,
    onReady
  } = $props()

  // Mirror the real editor's onCreate readiness signal so the parent's
  // post-mount work (scroll restore across Edit↔Source, #319) runs in tests.
  $effect(() => {
    onReady?.()
  })
</script>

<div data-testid="tiptap-stub" data-page={page}></div>
