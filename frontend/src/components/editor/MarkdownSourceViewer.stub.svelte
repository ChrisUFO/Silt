<script lang="ts">
  let { blocks, filePath }: { blocks: unknown[]; filePath: string } = $props()
</script>

<div data-testid="markdown-source-stub" data-file={filePath}></div>
