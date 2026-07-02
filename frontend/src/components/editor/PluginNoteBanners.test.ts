// #355: PluginNoteBanners dismiss host contract. Verifies the close affordance
// is present per banner and that dismissal removes the surface after the grace
// timeout (the host never wedges on an unresponsive plugin). The host→iframe
// 'dismiss' event envelope and the updatePluginSetting allowlist are covered by
// PluginSurfaceFrame.test.ts; this test covers the host-side teardown ordering.
import { describe, expect, it, beforeEach, vi } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/svelte'

vi.mock('../../plugins/grants.svelte', () => ({
  isGranted: vi.fn(() => true),
  initGrants: vi.fn(),
  refreshGrants: vi.fn(),
  resetGrantsForTests: vi.fn(),
  setGrantsForTests: vi.fn()
}))

vi.mock('../../plugins/context', () => ({
  makePluginContext: vi.fn(() => ({}))
}))

vi.mock('../../../wailsjs/go/main/App.js', () => ({
  GetPluginSettingsForNotebook: vi.fn().mockResolvedValue({}),
  UpdatePluginSetting: vi.fn().mockResolvedValue(undefined),
  PluginRawQuery: vi.fn()
}))

import {
  registerSurface,
  resetSurfacesForTests,
  getSurfaces
} from '../../plugins/surfaces'
import PluginNoteBanners from './PluginNoteBanners.svelte'

describe('PluginNoteBanners dismiss (#355)', () => {
  beforeEach(() => {
    cleanup()
    resetSurfacesForTests()
  })

  it('renders an accessible dismiss button per banner', () => {
    registerSurface({
      id: 'p:b1',
      pluginID: 'p',
      kind: 'note-banner',
      label: 'Summary',
      html: '<div>summary</div>'
    })
    render(PluginNoteBanners)
    expect(
      screen.getByRole('button', { name: /dismiss summary/i })
    ).toBeTruthy()
  })

  it('tears the surface down after the dismiss grace timeout', () => {
    registerSurface({
      id: 'p:b1',
      pluginID: 'p',
      kind: 'note-banner',
      label: 'One',
      html: '<div>1</div>'
    })

    vi.useFakeTimers()
    try {
      render(PluginNoteBanners)
      const closeBtn = screen.getByRole('button', { name: /dismiss one/i })
      fireEvent.click(closeBtn)

      // Grace window: the surface is still present immediately after click.
      expect(
        getSurfaces('note-banner').find((s) => s.id === 'p:b1')
      ).toBeTruthy()

      // After the timeout, the host removes it regardless of plugin response.
      vi.advanceTimersByTime(500)
      expect(
        getSurfaces('note-banner').find((s) => s.id === 'p:b1')
      ).toBeUndefined()
    } finally {
      vi.useRealTimers()
    }
  })
})

describe('PluginNoteBanners stacking collapse (#358)', () => {
  beforeEach(() => {
    cleanup()
    resetSurfacesForTests()
  })

  function registerN(n: number) {
    for (let i = 1; i <= n; i++) {
      registerSurface({
        id: `p:b${i}`,
        pluginID: 'p',
        kind: 'note-banner',
        label: `Banner ${i}`,
        html: `<div>${i}</div>`
      })
    }
  }

  it('renders all banners directly when at or under the threshold (1 and 2)', () => {
    registerN(1)
    const { unmount } = render(PluginNoteBanners)
    expect(
      screen.queryByRole('button', { name: /dismiss banner 1/i })
    ).toBeTruthy()
    expect(screen.queryByText(/plugin banners/i)).toBeNull() // no collapse toggle
    unmount()

    resetSurfacesForTests()
    registerN(2)
    render(PluginNoteBanners)
    expect(
      screen.queryByRole('button', { name: /dismiss banner 1/i })
    ).toBeTruthy()
    expect(
      screen.queryByRole('button', { name: /dismiss banner 2/i })
    ).toBeTruthy()
    expect(screen.queryByText(/2 plugin banners/i)).toBeNull()
  })

  it('collapses into a summary when more than 2 banners stack, and expands on click', async () => {
    registerN(3)
    render(PluginNoteBanners)

    // Collapsed by default: the summary is shown, individual banners hidden.
    const toggle = screen.getByRole('button', { name: /3 plugin banners/i })
    expect(toggle.getAttribute('aria-expanded')).toBe('false')
    expect(
      screen.queryByRole('button', { name: /dismiss banner 1/i })
    ).toBeNull()

    // Expand reveals all banners.
    await fireEvent.click(toggle)
    expect(toggle.getAttribute('aria-expanded')).toBe('true')
    expect(
      screen.queryByRole('button', { name: /dismiss banner 1/i })
    ).toBeTruthy()
    expect(
      screen.queryByRole('button', { name: /dismiss banner 3/i })
    ).toBeTruthy()
  })
})
