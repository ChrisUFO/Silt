import {
  GetSystemConfig,
  GetConfigLoadError,
  SaveSystemConfig
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
 * Persist a config. Returns true on success. On success the store is updated
 * and the dirty flag cleared; the Go side emits `config:changed`, which the
 * hot-reload subscription also honours.
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
