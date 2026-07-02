// #214: bespoke plugin settings pages — registry either/or validation and the
// Settings shell dynamic-tab enumeration.

import { describe, expect, it, beforeEach, vi } from 'vitest'

// Mock the grant cache so isGranted returns true (the surface registry checks it).
vi.mock('./grants.svelte', () => ({
  isGranted: () => true,
  grantStore: { subscribe: () => () => {} }
}))

import { registerPlugin, getFirstParty } from './registry'
import { resetSurfacesForTests, registerSurface, getSurfaces } from './surfaces'
import type { RegisteredPlugin } from './sdk'

function makePlugin(
  overrides: Partial<RegisteredPlugin> = {}
): RegisteredPlugin {
  return {
    manifest: {
      id: 'test-bespoke',
      name: 'Test Bespoke',
      version: '1.0.0'
    },
    component: {}, // dummy
    source: 'first-party',
    ...overrides
  }
}

describe('bespoke settings pages (#214) — registry either/or', () => {
  beforeEach(() => {
    // Reset by re-registering the real first-party set is not needed here; we
    // use a unique id per test so the registry map doesn't collide.
  })

  it('accepts a plugin with settingsPageComponent (no manifest.settings)', () => {
    const plugin = makePlugin({
      settingsPageComponent: (() => {}) as any
    })
    plugin.manifest.id = 'bespoke-only'
    expect(() => registerPlugin(plugin)).not.toThrow()
    expect(getFirstParty('bespoke-only')?.settingsPageComponent).toBeDefined()
  })

  it('accepts a plugin with manifest.settings (no settingsPageComponent)', () => {
    const plugin = makePlugin({
      manifest: {
        id: 'schema-only',
        name: 'Schema Only',
        version: '1.0.0',
        settings: [{ key: 'k', label: 'K', type: 'string' }]
      }
    })
    expect(() => registerPlugin(plugin)).not.toThrow()
  })

  it('rejects a plugin declaring BOTH settingsPageComponent and manifest.settings', () => {
    const plugin = makePlugin({
      settingsPageComponent: (() => {}) as any,
      manifest: {
        id: 'both-declared',
        name: 'Both',
        version: '1.0.0',
        settings: [{ key: 'k', label: 'K', type: 'string' }]
      }
    })
    expect(() => registerPlugin(plugin)).toThrow(/cannot declare both/)
  })
})

describe('bespoke settings pages (#214) — surface-based detection', () => {
  beforeEach(() => {
    resetSurfacesForTests()
  })

  it('a settings-panel surface is detectable via getSurfaces', () => {
    registerSurface({
      id: 'third-party:settings',
      pluginID: 'third-party',
      kind: 'settings-panel',
      label: 'Third Party Settings',
      html: '<div>settings</div>'
    })
    const surfaces = getSurfaces('settings-panel')
    expect(surfaces).toHaveLength(1)
    expect(surfaces[0].pluginID).toBe('third-party')
    expect(surfaces[0].label).toBe('Third Party Settings')
  })
})
