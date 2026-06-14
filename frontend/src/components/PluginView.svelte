<script lang="ts">
  import { loadedPlugins } from '../plugins/store.svelte'
  import { makePluginContext } from '../plugins/context'
  import type { PluginContext } from '../plugins/sdk'

  interface Props {
    pluginId: string
    activeNotebook: string
    activeSection: string
    activePage: string
  }

  let { pluginId, activeNotebook, activeSection, activePage }: Props = $props()

  let plugin = $derived(loadedPlugins.plugins.get(pluginId))
  let loadError = $derived(loadedPlugins.errors.find((e) => e.id === pluginId))

  // Build a context bound to the current location for the plugin component.
  let ctx = $derived(
    makePluginContext(activeNotebook, activeSection, activePage)
  ) as PluginContext
</script>

{#if loadError}
  <div class="flex-1 p-8 flex flex-col select-none">
    <h1
      class="font-headline-lg text-headline-lg text-text-primary mb-2 capitalize"
    >
      {pluginId}
    </h1>
    <p class="text-error font-body-md">
      Plugin failed to load: {loadError.message}
    </p>
  </div>
{:else if !plugin}
  <div class="flex-1 p-8 flex flex-col select-none">
    <div class="flex items-center gap-3 mb-3">
      <span class="material-symbols-outlined text-text-muted text-[28px]"
        >extension_off</span
      >
      <div>
        <h1
          class="font-headline-lg text-headline-lg text-text-primary capitalize"
        >
          {pluginId}
        </h1>
        <p class="text-text-muted text-[12px] font-body-md">
          plugin not registered
        </p>
      </div>
    </div>
    <p class="text-text-muted font-body-md">
      This plugin slot is reserved for a future plugin. First-party plugins
      (Agenda, Calendar) are bundled; install others via the plugin manager.
    </p>
  </div>
{:else}
  {@const Plugin = plugin.component}
  <Plugin {ctx} manifest={plugin.manifest} />
{/if}
