import type { LoadedPlugins } from './sdk'

// Reactive store of loaded plugins, shared between the loader and PluginView.
// Svelte 5 permits $state in .svelte.ts modules.
export const loadedPlugins: LoadedPlugins = $state({
  plugins: new Map(),
  errors: []
})
