import {
  GetSystemConfig,
  GetConfigLoadError,
  SaveSystemConfig,
  UpdatePluginSetting,
  AppendDismissedTip,
  SetShowFormatToolbar,
  SetFocusMode
} from '../../wailsjs/go/main/App.js'
import { EventsOn } from '../../wailsjs/runtime/runtime.js'
import type { config } from '../../wailsjs/go/models.js'

export type SystemConfig = config.SystemConfig

// Reactive settings store (Svelte 5 runes in a .svelte.ts module, mirroring
// plugins/store.svelte.ts: a const object whose properties are mutated).
// `config` is null until the first successful load; consumers guard on that.
export const settings = $state({
  config: null as SystemConfig | null,
  loading: false,
  saving: false,
  error: '' as string,
  // True when the user has unsaved edits in the panel; used to warn before
  // discarding (close, or an external hot-reload overwriting local edits).
  dirty: false,
  // Set when an external config.yaml edit landed while the user had unsaved
  // edits. The draft is preserved (never silently clobbered); this flag lets
  // the panel offer a "discard my edits / reload" affordance.
  pendingExternal: false
})

/** Load the system config from the Go backend into the store. */
export async function loadConfig(): Promise<void> {
  settings.loading = true
  settings.error = ''
  try {
    settings.config = await GetSystemConfig()
    settings.dirty = false
    // Surface a startup config-load error that was emitted before this
    // frontend subscribed to config:error (one-shot: retrieved then cleared
    // on the Go side, so a broken config.yaml isn't silently masked).
    const loadErr = await GetConfigLoadError()
    if (loadErr) settings.error = loadErr
  } catch (e) {
    settings.error = errMsg(e)
  } finally {
    settings.loading = false
  }
}

/**
 * Persist a full config. Returns true on success. On success the store is
 * updated and the dirty flag cleared. NOTE: the Go side (SaveSystemConfig)
 * deliberately does NOT emit `config:changed` for internal saves — the local
 * mirror is updated optimistically here, and external edits flow back through
 * the watcher → `config:changed` (honoured by the hot-reload subscription).
 * Use {@link updatePluginSetting} for plugin-scoped writes — it does a Go-side
 * atomic read-modify-write that cannot clobber a concurrent external edit (#120).
 */
export async function saveConfig(cfg: SystemConfig): Promise<boolean> {
  settings.saving = true
  settings.error = ''
  try {
    await SaveSystemConfig(cfg)
    settings.config = cfg
    settings.dirty = false
    return true
  } catch (e) {
    settings.error = errMsg(e)
    return false
  } finally {
    settings.saving = false
  }
}

/**
 * Atomically update a single per-plugin setting key on the Go side (#120).
 * The mutation + atomic write happen under the Go-side `configMu`, so — unlike
 * the read-mutate-`saveConfig` dance — a concurrent external config.yaml edit
 * cannot be silently clobbered. Only `plugins.plugin_settings[pluginID][key]`
 * is touched. The local `settings.config` mirror is updated to match (the Go
 * side does not emit `config:changed` for internal saves).
 */
export async function updatePluginSetting(
  pluginID: string,
  key: string,
  value: unknown
): Promise<boolean> {
  settings.saving = true
  settings.error = ''
  try {
    await UpdatePluginSetting(pluginID, key, value)
    // Mirror the targeted write into the local snapshot so consumers reflect
    // it immediately (no config:changed round-trip for internal saves).
    const cfg = settings.config
    if (cfg) {
      if (!cfg.plugins) {
        cfg.plugins = { active: [], disabled: [], plugin_settings: {} } as any
      }
      if (!cfg.plugins.plugin_settings) {
        cfg.plugins.plugin_settings = {}
      }
      const ps = cfg.plugins.plugin_settings as Record<string, any>
      if (!ps[pluginID] || typeof ps[pluginID] !== 'object') {
        ps[pluginID] = {}
      }
      ps[pluginID][key] = value
    }
    return true
  } catch (e) {
    settings.error = errMsg(e)
    return false
  } finally {
    settings.saving = false
  }
}

/**
 * Atomically append a tip ID to `ui.dismissed_tips` on the Go side (#197).
 * Mirrors the UpdatePluginSetting contract: the mutation + atomic write happen
 * under `configMu`, so a concurrent external config.yaml edit cannot be
 * silently clobbered. Idempotent — the Go side returns early if the tipID is
 * already present. The local mirror is updated optimistically (no
 * config:changed round-trip for internal saves).
 */
export async function appendDismissedTip(tipID: string): Promise<boolean> {
  settings.saving = true
  settings.error = ''
  try {
    await AppendDismissedTip(tipID)
    const cfg = settings.config
    if (cfg) {
      if (!cfg.ui) cfg.ui = {} as any
      const tips = cfg.ui.dismissed_tips
      if (!Array.isArray(tips) || !tips.includes(tipID)) {
        cfg.ui.dismissed_tips = [...(tips ?? []), tipID]
      }
    }
    return true
  } catch (e) {
    settings.error = errMsg(e)
    return false
  } finally {
    settings.saving = false
  }
}

function errMsg(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

// --- hot-reload -------------------------------------------------------------
// Go re-parses .system/config.yaml on any change (including external edits)
// and emits config:changed / config:error. Refresh the store accordingly.
// If the user has unsaved local edits, the draft is preserved (never silently
// clobbered); pendingExternal signals that a newer config is available.
let offConfigChanged: (() => void) | null = null
let offConfigError: (() => void) | null = null

export function initConfigHotReload(): void {
  if (offConfigChanged) return // idempotent
  offConfigChanged = EventsOn('config:changed', (cfg: SystemConfig) => {
    settings.config = cfg
    settings.error = ''
    if (settings.dirty) {
      settings.pendingExternal = true
    }
  })
  offConfigError = EventsOn('config:error', (msg: string) => {
    // A reload failed to parse (e.g. external edit broke the YAML). Keep the
    // last-good config; surface a non-blocking error so the user knows.
    settings.error = msg
  })
}

/** Reload from the backend and clear the pending-external flag. */
export async function reloadFromBackend(): Promise<void> {
  await loadConfig()
  settings.pendingExternal = false
}

/**
 * Toggle the formatting toolbar visibility state. Writes the single field
 * atomically via the backend (SetShowFormatToolbar) and mirrors it into the
 * config snapshot — it deliberately does NOT call saveConfig, which would clear
 * `dirty` and silently clobber a user's unsaved EditorTab draft. Mirrors the
 * appendDismissedTip pattern. Returns the new effective value.
 */
export async function toggleFormatToolbar(): Promise<boolean | null> {
  const cfg = settings.config
  if (!cfg) return null
  if (!cfg.ui) cfg.ui = {} as any
  const next = cfg.ui.show_format_toolbar === false ? true : false
  settings.saving = true
  settings.error = ''
  try {
    await SetShowFormatToolbar(next)
    cfg.ui.show_format_toolbar = next
    return next
  } catch (e) {
    settings.error = errMsg(e)
    return null
  } finally {
    settings.saving = false
  }
}

/**
 * Toggle the editor focus mode (dimming inactive paragraphs). Same atomic
 * single-field write as toggleFormatToolbar — avoids clobbering an unsaved
 * settings draft.
 */
export async function toggleFocusMode(): Promise<boolean | null> {
  const cfg = settings.config
  if (!cfg) return null
  if (!cfg.editor) cfg.editor = {} as any
  const next = !cfg.editor.focus_mode
  settings.saving = true
  settings.error = ''
  try {
    await SetFocusMode(next)
    cfg.editor.focus_mode = next
    return next
  } catch (e) {
    settings.error = errMsg(e)
    return null
  } finally {
    settings.saving = false
  }
}
