// Granted-capabilities cache (#158). This is the TRUSTED source for
// client-side capability gates: the manifest declares what a plugin REQUESTS;
// this cache reflects what the user actually GRANTED (via Go). The three
// registries (slash-registry, surfaces, decorations) consult this cache
// internally so a plugin importing `registerSlashCommand` directly cannot
// skip the gate.
//
// Refreshed from GetGrantedCapabilities on boot + on every plugins:changed
// event. First-party plugins are implicitly granted every capability.

import { GetGrantedCapabilities } from '../../wailsjs/go/main/App.js'
import { EventsOn } from '../../wailsjs/runtime/runtime.js'
import { firstPartyPlugins } from './registry'

const ALL_CAPS = [
  'read-files',
  'write-files',
  'network',
  'os-open',
  'os-clipboard',
  'os-notify',
  'ui-surface',
  'editor-schema',
  'content-mutate'
]

const firstPartyIDs = new Set<string>()

function refreshFirstPartyIDs() {
  firstPartyIDs.clear()
  for (const fp of firstPartyPlugins()) {
    firstPartyIDs.add(fp.manifest.id)
  }
}

// Module-scoped grant map: pluginID → Set<capability>. Not reactive (the
// registries read it synchronously at registration time). Refreshed by
// refreshGrants().
let grantsMap = new Map<string, Set<string>>()

/**
 * Re-fetch the grant table from Go and merge first-party implicit grants.
 * Called on boot and on every `plugins:changed` event. Fail-closed: on
 * error, the cache stays empty so all gates deny.
 */
export async function refreshGrants(): Promise<void> {
  refreshFirstPartyIDs()
  try {
    const result = await GetGrantedCapabilities()
    const next = new Map<string, Set<string>>()
    if (result) {
      for (const [pid, caps] of Object.entries(result)) {
        if (caps && typeof caps === 'object') {
          next.set(pid, new Set(Object.keys(caps)))
        }
      }
    }
    for (const id of firstPartyIDs) {
      next.set(id, new Set(ALL_CAPS))
    }
    grantsMap = next
  } catch {
    // Best-effort: leave the previous cache in place on error.
  }
}

/**
 * Synchronous check: does pluginID currently have the capability granted?
 * First-party plugins always return true. Returns false for unknown plugins
 * or before the cache is first populated (fail-closed).
 */
export function isGranted(pluginID: string, capability: string): boolean {
  if (firstPartyIDs.has(pluginID)) return true
  return grantsMap.get(pluginID)?.has(capability) ?? false
}

let wired = false

/**
 * Wire the initial fetch + the plugins:changed subscription. Idempotent.
 */
export function initGrants(): void {
  if (wired) return
  wired = true
  refreshFirstPartyIDs()
  void refreshGrants()
  EventsOn('plugins:changed', () => void refreshGrants())
}

/** Test-only: reset the cache + wiring state. */
export function resetGrantsForTests(): void {
  grantsMap = new Map()
  firstPartyIDs.clear()
  wired = false
}

/** Test-only: directly set the grant cache (bypass Go IPC). */
export function setGrantsForTests(grants: Record<string, string[]>): void {
  refreshFirstPartyIDs()
  const next = new Map<string, Set<string>>()
  for (const [pid, caps] of Object.entries(grants)) {
    next.set(pid, new Set(caps))
  }
  for (const id of firstPartyIDs) {
    next.set(id, new Set(ALL_CAPS))
  }
  grantsMap = next
}
