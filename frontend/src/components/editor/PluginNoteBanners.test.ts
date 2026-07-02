// #215: PluginNoteBanners host component test — verifies the host renders
// registered banners, exposes the correct a11y attributes, and removes a
// banner on close (calling unregisterSurface) with focus management.
import { describe, expect, it, beforeEach, vi } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/svelte'

vi.mock('../../plugins/grants.svelte', () => ({
  isGranted: vi.fn(() => true),
  grantStore: { subscribe: () => () => {} }
}))

// Mock PluginSurfaceFrame so we don't need the full iframe bridge in jsdom.
vi.mock('../PluginSurfaceFrame.svelte', () => ({
  default: vi.fn().mockImplementation(() => ({
    $$render: () => '<div data-testid="mock-frame"></div>',
    render: () => {}
  }))
}))

// Mock makePluginContext so we don't hit wailsjs bindings.
vi.mock('../../plugins/context', () => ({
  makePluginContext: vi.fn(() => ({}))
}))

import PluginNoteBanners from './PluginNoteBanners.svelte'
import {
  registerSurface,
  resetSurfacesForTests,
  getSurfaces
} from '../../plugins/surfaces'

describe('PluginNoteBanners host component (#215)', () => {
  beforeEach(() => {
    resetSurfacesForTests()
  })

  it('renders a registered note-banner with correct a11y attributes', async () => {
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

  it('removes the banner when the close button is clicked', async () => {
    registerSurface({
      id: 'test:banner',
      pluginID: 'test',
      kind: 'note-banner',
      label: 'Summary',
      html: '<div>summary</div>'
    })
    render(PluginNoteBanners)

    // The close button exists with an accessible name derived from the label.
    const closeBtn = screen.getByRole('button', { name: /dismiss summary/i })
    expect(closeBtn).toBeTruthy()

    // Clicking it removes the banner from the surface registry.
    await fireEvent.click(closeBtn)
    expect(getSurfaces('note-banner')).toHaveLength(0)
  })

  it('renders multiple banners in registration order', () => {
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
    const banners = container.querySelectorAll('[role="status"]')
    expect(banners).toHaveLength(2)
    expect(banners[0].getAttribute('aria-label')).toBe('First')
    expect(banners[1].getAttribute('aria-label')).toBe('Second')
  })

  it('close button has accessible name and data attribute for focus management', () => {
    registerSurface({
      id: 'test:focus',
      pluginID: 'test',
      kind: 'note-banner',
      label: 'Alert Banner',
      html: '<div>alert</div>'
    })
    render(PluginNoteBanners)
    const closeBtn = screen.getByRole('button', {
      name: /dismiss alert banner/i
    })
    expect(closeBtn.getAttribute('data-banner-close')).toBe('test:focus')
    expect(closeBtn.getAttribute('title')).toBe('Dismiss Alert Banner')
  })
})
