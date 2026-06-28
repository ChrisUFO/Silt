<script lang="ts">
  // Test-only Svelte component (#321). Renders a marker element so tests
  // can assert the routing layer actually rendered a registered
  // sidebarComponent instead of falling back to the page tree. Exposes
  // the props it received on `globalThis.__lastStubSidebarProps` so the
  // test can inspect the ctx + manifest plumbing.
  import type { PluginContext, PluginManifest } from '../../plugins/sdk'
  interface Props {
    ctx: PluginContext
    manifest?: PluginManifest | null
  }
  let { ctx, manifest }: Props = $props()
  // svelte-ignore state_referenced_locally: the props are captured for test
  // inspection only; reactivity is not needed here (mirrors PluginView.svelte).
  ;(globalThis as any).__lastStubSidebarProps = { ctx, manifest }
</script>

<div data-test-stub-sidebar data-plugin-id={manifest?.id ?? ''}>
  STUB-SIDEBAR-RENDERED
</div>
