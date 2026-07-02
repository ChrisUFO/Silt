<script lang="ts">
  // Test fixture (#357): a plugin settingsPageComponent that throws during
  // render. The top-level throw runs during component instantiation (render
  // phase), which <svelte:boundary> must catch. NOT shipped — test-only.
  let { ctx, manifest }: { ctx: any; manifest: any } = $props()
  throw new Error('boom from CrashingSettings')
</script>

<div>unreachable</div>
