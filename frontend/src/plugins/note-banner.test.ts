// #215: note-banner surface kind — registration, rendering order, and teardown.
// Tests the surface-manager contract the PluginNoteBanners host relies on:
//   - registering a note-banner surface surfaces in getSurfaces('note-banner')
//   - banners render in registration order
//   - dismissal (unregisterSurface) removes the banner
//   - teardownPlugin (unregisterPluginSurfaces) removes all of a plugin's banners
//   - multiple banners coexist (stacking)
import { describe, expect, it, beforeEach, vi } from 'vitest'

vi.mock('./grants.svelte', () => ({
  isGranted: vi.fn(() => true),
  initGrants: vi.fn(),
  refreshGrants: vi.fn(),
  resetGrantsForTests: vi.fn(),
  setGrantsForTests: vi.fn()
}))

import {
  registerSurface,
  unregisterSurface,
  unregisterPluginSurfaces,
  getSurfaces,
  resetSurfacesForTests
} from './surfaces'

describe('note-banner surface (#215)', () => {
  beforeEach(() => {
    resetSurfacesForTests()
  })

  it('registers and retrieves a note-banner by kind', () => {
    const off = registerSurface({
      id: 'ai:summary-banner',
      pluginID: 'ai-summary',
      kind: 'note-banner',
      label: 'Summary',
      html: '<div>summary</div>'
    })
    const banners = getSurfaces('note-banner')
    expect(banners).toHaveLength(1)
    expect(banners[0].id).toBe('ai:summary-banner')
    expect(banners[0].label).toBe('Summary')
    off()
  })

  it('renders banners in registration order', () => {
    registerSurface({
      id: 'p:b1',
      pluginID: 'p',
      kind: 'note-banner',
      label: 'First',
      html: '<div>1</div>'
    })
    registerSurface({
      id: 'p:b2',
      pluginID: 'p',
      kind: 'note-banner',
      label: 'Second',
      html: '<div>2</div>'
    })
    registerSurface({
      id: 'p:b3',
      pluginID: 'p',
      kind: 'note-banner',
      label: 'Third',
      html: '<div>3</div>'
    })
    const banners = getSurfaces('note-banner')
    expect(banners.map((b) => b.label)).toEqual(['First', 'Second', 'Third'])
    // Stacking: multiple banners coexist.
    expect(banners.length).toBe(3)
    unregisterPluginSurfaces('p')
  })

  it('dismissal (unregisterSurface) removes a single banner', () => {
    registerSurface({
      id: 'p:b1',
      pluginID: 'p',
      kind: 'note-banner',
      label: 'One',
      html: '<div>1</div>'
    })
    registerSurface({
      id: 'p:b2',
      pluginID: 'p',
      kind: 'note-banner',
      label: 'Two',
      html: '<div>2</div>'
    })
    expect(getSurfaces('note-banner')).toHaveLength(2)
    // Dismiss the first banner (mirrors the host's close-affordance handler).
    unregisterSurface('p:b1')
    const remaining = getSurfaces('note-banner')
    expect(remaining).toHaveLength(1)
    expect(remaining[0].id).toBe('p:b2')
  })

  it("teardownPlugin (unregisterPluginSurfaces) removes all of a plugin's banners", () => {
    registerSurface({
      id: 'ai:b1',
      pluginID: 'ai-plugin',
      kind: 'note-banner',
      label: 'A',
      html: '<div>a</div>'
    })
    registerSurface({
      id: 'ai:b2',
      pluginID: 'ai-plugin',
      kind: 'note-banner',
      label: 'B',
      html: '<div>b</div>'
    })
    // A different plugin's banner survives.
    registerSurface({
      id: 'other:b1',
      pluginID: 'other',
      kind: 'note-banner',
      label: 'Other',
      html: '<div>o</div>'
    })
    expect(getSurfaces('note-banner')).toHaveLength(3)
    // Teardown the AI plugin (mirrors loader.teardownPlugin).
    unregisterPluginSurfaces('ai-plugin')
    const remaining = getSurfaces('note-banner')
    expect(remaining).toHaveLength(1)
    expect(remaining[0].pluginID).toBe('other')
  })

  it('note-banner surfaces do not leak into other kinds', () => {
    registerSurface({
      id: 'p:b',
      pluginID: 'p',
      kind: 'note-banner',
      label: 'Banner',
      html: '<div>b</div>'
    })
    expect(getSurfaces('sidebar-panel')).toHaveLength(0)
    expect(getSurfaces('note-banner')).toHaveLength(1)
  })
})
