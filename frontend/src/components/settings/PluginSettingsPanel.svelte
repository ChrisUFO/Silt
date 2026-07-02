<script lang="ts">
  import type { RegisteredPlugin } from '../../plugins/sdk'
  import PluginSurfaceFrame from '../PluginSurfaceFrame.svelte'
  import { getSurfaces, onSurfacesChanged } from '../../plugins/surfaces'
  import { makePluginContext } from '../../plugins/context'
  import { onDestroy } from 'svelte'

  // PluginSettingsPanel renders a single plugin's bespoke Settings page (#214).
  // First-party plugins ship a compiled Svelte component (settingsPageComponent);
  // third-party plugins register a 'settings-panel' iframe surface. This
  // component resolves which path applies and renders it.

  interface Props {
    plugin: RegisteredPlugin
    activeNotebook: string
    activeSection: string
    activePage: string
  }

  let { plugin, activeNotebook, activeSection, activePage }: Props = $props()

  // For third-party plugins, look up the registered 'settings-panel' surface.
  // The surface list is reactive so a plugin registering/unregistering its page
  // at runtime is reflected immediately.
  let thirdPartySurfaces = $state(
    getSurfaces('settings-panel').filter(
      (s) => s.pluginID === plugin.manifest.id
    )
  )
  const offSurfacesChanged = onSurfacesChanged((all) => {
    thirdPartySurfaces = all.filter(
      (s) => s.kind === 'settings-panel' && s.pluginID === plugin.manifest.id
    )
  })
  onDestroy(() => offSurfacesChanged())

  // Build the real PluginContext for the first-party component so it can call
  // ctx.getPluginSettings() / ctx.updatePluginSetting(). Memoized per plugin —
  // rebuild only when the plugin id changes (not on every reactive re-render).
  let ctx = $derived(makePluginContext(plugin.manifest.id) as any)
</script>

{#if plugin.settingsPageComponent}
  <!-- First-party: render the compiled Svelte component (Svelte 5 runes mode:
       dynamic components are rendered directly via a capitalized binding) -->
  {@const Comp = plugin.settingsPageComponent}
  <Comp
    {ctx}
    manifest={plugin.manifest}
    {activeNotebook}
    {activeSection}
    {activePage}
  />
{:else if thirdPartySurfaces.length > 0}
  <!-- Third-party: render the settings-panel iframe surface -->
  <PluginSurfaceFrame surface={thirdPartySurfaces[0]} ctxProxy={ctx} />
{:else}
  <div class="p-6 text-text-muted font-body-md">
    This plugin has no configurable settings.
  </div>
{/if}
