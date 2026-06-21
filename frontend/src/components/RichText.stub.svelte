<script lang="ts">
  // Test-only stub for RichText.svelte. Renders the text prop verbatim so
  // tests can assert on the rendered content without pulling in the full
  // Smart Graph / TipTap resolution chain. Used by EmbedPortal.test.ts.
  let { text }: { text: string } = $props()
</script>

<span data-testid="rich-text-stub">{text}</span>
