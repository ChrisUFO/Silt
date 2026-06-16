import { ReadPluginSource, ListPlugins } from '../../wailsjs/go/main/App.js'
import { getFirstParty, firstPartyPlugins } from './registry'
import { makePluginContext } from './context'
import { setActiveLocation } from './location.svelte'
import { loadedPlugins } from './store.svelte'
import { settings } from '../settings/store.svelte'
import type { LoadedPlugins, RegisteredPlugin, SiltPlugin } from './sdk'
import DiskPluginNotice from './DiskPluginNotice.svelte'

/**
 * Discover and initialize all active plugins:
 *   1. First-party bundled plugins (always available).
 *   2. On-disk plugins discovered under .system/plugins/ (skipping any with
 *      a .disabled sentinel), loaded from index.js as native ESM via a blob
 *      URL (so Vite does not try to resolve them at build time). Discovery is
 *      purely folder-based + the .disabled sentinel, so install "just works"
 *      with no config.yaml editing. (The legacy `plugins.active` list in
 *      config.yaml is no longer a whitelist.)
 * Each plugin's init(ctx) receives the same PluginContext. Per-plugin load
 * failures are collected rather than aborting the whole boot.
 */
export async function loadPlugins(
  activeNotebook: string,
  activeSection: string,
  activePage: string
): Promise<LoadedPlugins> {
  // Keep the reactive location state in sync (#69). Plugins that read
  // ctx.activeNotebook at query time see the live value.
  setActiveLocation(activeNotebook, activeSection, activePage)
  const ctx = makePluginContext()
  const plugins = new Map<string, RegisteredPlugin>()
  const errors: { id: string; message: string }[] = []

  // Discover on-disk plugins by folder.
  let installed: { id: string; disabled: boolean; has_index: boolean }[] = []
  try {
    installed = (await ListPlugins()) ?? []
  } catch {
    installed = []
  }

  for (const p of installed) {
    const id = p.id
    if (getFirstParty(id)) continue // first-party wins; handled below
    if (p.disabled) continue // .disabled sentinel → skip
    try {
      if (!p.has_index) {
        errors.push({ id, message: 'missing index.js' })
        continue
      }
      const src = await ReadPluginSource(id)
      const blob = new Blob([src], { type: 'text/javascript' })
      const url = URL.createObjectURL(blob)
      let mod: any
      try {
        mod = await import(/* @vite-ignore */ url)
      } finally {
        URL.revokeObjectURL(url)
      }
      const def: SiltPlugin | undefined = mod?.default ?? mod
      const manifest = def?.manifest ?? { id, name: id, version: '0.0.0' }
      def?.init?.(ctx)
      const reg: RegisteredPlugin = {
        manifest,
        component: mod?.default?.component ?? DiskPluginNotice,
        init: def?.init,
        source: 'disk'
      }
      plugins.set(id, reg)
    } catch (e) {
      errors.push({
        id,
        message: e instanceof Error ? e.message : String(e)
      })
    }
  }

  // First-party plugins: always available but the user can disable them via
  // Settings → Plugins (stored in config.yaml plugins.disabled). Uninstall is
  // not available for bundled plugins.
  const disabledIds = new Set(settings.config?.plugins?.disabled ?? [])
  for (const fp of firstPartyPlugins()) {
    if (disabledIds.has(fp.manifest.id)) continue
    if (!plugins.has(fp.manifest.id)) {
      fp.init?.(ctx)
      plugins.set(fp.manifest.id, fp)
    }
  }

  loadedPlugins.plugins = plugins
  loadedPlugins.errors = errors
  return { plugins, errors }
}
