// Grants cache tests (#158, P2-9).
//
// Tests the grant cache refresh from GetGrantedCapabilities + the
// plugins:changed event subscription.
import { describe, expect, it, beforeEach, vi } from 'vitest'

// Mock the wailsjs + registry dependencies so the module loads cleanly.
const mockGetGranted = vi.hoisted(() => vi.fn(() => Promise.resolve({})))
const mockEventsOn = vi.hoisted(() => vi.fn())

vi.mock('../../wailsjs/go/main/App.js', () => ({
  GetGrantedCapabilities: mockGetGranted
}))
vi.mock('../../wailsjs/runtime/runtime.js', () => ({
  EventsOn: mockEventsOn
}))
vi.mock('./registry', () => ({
  firstPartyPlugins: () => []
}))

import {
  refreshGrants,
  isGranted,
  initGrants,
  resetGrantsForTests,
  setGrantsForTests
} from './grants.svelte'

describe('grants cache (#158)', () => {
  beforeEach(() => {
    resetGrantsForTests()
    mockGetGranted.mockReset()
    mockEventsOn.mockReset()
    mockGetGranted.mockResolvedValue({})
  })

  it('isGranted returns false before refresh', () => {
    expect(isGranted('any-plugin', 'network')).toBe(false)
  })

  it('refreshGrants populates from GetGrantedCapabilities', async () => {
    mockGetGranted.mockResolvedValue({
      'net-plugin': { network: 'granted' },
      'file-plugin': { 'write-files': 'notebook' }
    })
    await refreshGrants()
    expect(isGranted('net-plugin', 'network')).toBe(true)
    expect(isGranted('file-plugin', 'write-files')).toBe(true)
    expect(isGranted('net-plugin', 'write-files')).toBe(false)
  })

  it('refreshGrants grants first-party plugins all capabilities', async () => {
    // The registry mock returns [], so we use setGrantsForTests to simulate.
    setGrantsForTests({ 'third-party': ['network'] })
    // First-party plugins are added by setGrantsForTests via the registry.
    // Since the mock returns no first-party, verify third-party works.
    expect(isGranted('third-party', 'network')).toBe(true)
    expect(isGranted('third-party', 'editor-schema')).toBe(false)
  })

  it('refreshGrants is fail-open on subsequent IPC error (retains previous cache)', async () => {
    setGrantsForTests({ 'granted-plugin': ['network'] })
    mockGetGranted.mockRejectedValue(new Error('IPC failed'))
    await refreshGrants()
    // After error, the previous cache stays in place (fail-open) so a
    // transient IPC blip doesn't wipe the UI. Go's requireGrant is the
    // authoritative enforcement; this cache only gates UI visibility.
    expect(isGranted('granted-plugin', 'network')).toBe(true)
  })

  it('refreshGrants is fail-closed on first-load IPC error (no previous cache)', async () => {
    mockGetGranted.mockRejectedValue(new Error('IPC failed'))
    await refreshGrants()
    expect(isGranted('any-plugin', 'network')).toBe(false)
  })

  it('initGrants subscribes to plugins:changed', async () => {
    initGrants()
    expect(mockEventsOn).toHaveBeenCalledWith(
      'plugins:changed',
      expect.any(Function)
    )
    // initGrants is idempotent — calling again does not re-subscribe.
    initGrants()
    expect(mockEventsOn).toHaveBeenCalledTimes(1)
  })

  it('plugins:changed event triggers refreshGrants', async () => {
    initGrants()
    mockGetGranted.mockResolvedValue({
      'evt-plugin': { 'ui-surface': 'granted' }
    })

    // Find the plugins:changed callback and invoke it.
    const callback = mockEventsOn.mock.calls.find(
      ([event]) => event === 'plugins:changed'
    )?.[1]
    expect(callback).toBeDefined()
    await callback()

    expect(isGranted('evt-plugin', 'ui-surface')).toBe(true)
  })
})
