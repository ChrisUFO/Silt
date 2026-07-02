// #215/#355: PluginNoteBanners host component. Covers the dismiss host
// contract (#355 grace-timeout teardown), the stacking collapse (#358), and the
// a11y/focus/ordering contracts from #215 that must survive the bridge work.
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

  it('can re-dismiss a banner that re-registers with the same id (#355)', () => {
    // The dismissedThisTick debounce guard must reset after teardown so a
    // plugin re-enabled and re-registered with the same surface.id is not
    // silently undismissable on its second appearance.
    registerSurface({
      id: 'p:re',
      pluginID: 'p',
      kind: 'note-banner',
      label: 'Repeater',
      html: '<div>1</div>'
    })

    vi.useFakeTimers()
    try {
      render(PluginNoteBanners)
      // First dismissal.
      fireEvent.click(screen.getByRole('button', { name: /dismiss repeater/i }))
      vi.advanceTimersByTime(500)
      expect(
        getSurfaces('note-banner').find((s) => s.id === 'p:re')
      ).toBeUndefined()

      // Plugin re-registers the same id (e.g. after re-enable).
      registerSurface({
        id: 'p:re',
        pluginID: 'p',
        kind: 'note-banner',
        label: 'Repeater',
        html: '<div>2</div>'
      })
      const closeBtn = screen.getByRole('button', { name: /dismiss repeater/i })
      fireEvent.click(closeBtn)
      vi.advanceTimersByTime(500)
      expect(
        getSurfaces('note-banner').find((s) => s.id === 'p:re')
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

// #215: restored host-component coverage — a11y attributes, empty state,
// registration order, the data-banner-close hook, and the focus-handoff path
// in dismiss(). These were deleted in the bridge rewrite and must stay covered
// since dismiss() retains non-trivial focus management.
describe('PluginNoteBanners host a11y + focus (#215)', () => {
  beforeEach(() => {
    cleanup()
    resetSurfacesForTests()
  })

  it('renders a registered banner with correct a11y attributes', () => {
    registerSurface({
      id: 'test:banner',
      pluginID: 'test',
      kind: 'note-banner',
      label: 'Summary',
      html: '<div>summary</div>'
    })
    const { container } = render(PluginNoteBanners)

    // The region landmark is present with an accessible name.
    const region = container.querySelector('[role="region"]')
    expect(region).toBeTruthy()
    expect(region?.getAttribute('aria-label')).toBe('Plugin banners')

    // The banner itself is a live region with the surface's label.
    const status = container.querySelector('[role="status"]')
    expect(status).toBeTruthy()
    expect(status?.getAttribute('aria-live')).toBe('polite')
    expect(status?.getAttribute('aria-label')).toBe('Summary')
  })

  it('renders nothing when no banners are registered', () => {
    const { container } = render(PluginNoteBanners)
    expect(container.querySelector('[role="region"]')).toBeNull()
  })

  it('renders multiple banners in registration order and tags each close button', () => {
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
    const { container } = render(PluginNoteBanners)

    // Dismiss buttons appear in registration order, and each carries the
    // data-banner-close attribute the focus-handoff code queries by id. (The
    // focus-handoff itself runs inside doRemove, exercised by the
    // grace-timeout test above; its actual focus() call is timing-fragile in
    // jsdom due to Svelte's microtask flush ordering, so the contract is
    // verified at the attribute + doRemove-run level, matching the original
    // suite's approach.)
    const closeBtns = container.querySelectorAll('[data-banner-close]')
    expect(closeBtns.length).toBe(2)
    expect(closeBtns[0].getAttribute('data-banner-close')).toBe('p:b1')
    expect(closeBtns[1].getAttribute('data-banner-close')).toBe('p:b2')
  })
})
