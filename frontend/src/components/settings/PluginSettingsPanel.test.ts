// #357: <svelte:boundary> around a bespoke plugin settings page component
// catches render-phase crashes so a misbehaving plugin cannot take down the
// whole Settings dialog (and trap the user). Verifies the fallback renders and
// the "Retry" button re-mounts the component.
import { describe, expect, it, beforeEach, vi } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/svelte'

import CrashingSettings from './__fixtures__/CrashingSettings.svelte'
import PluginSettingsPanel from './PluginSettingsPanel.svelte'
import type { RegisteredPlugin } from '../../plugins/sdk'

vi.mock('../../../wailsjs/go/main/App.js', () => ({
  GetPluginSettingsForNotebook: vi.fn().mockResolvedValue({}),
  UpdatePluginSetting: vi.fn().mockResolvedValue(undefined)
}))

vi.mock('../../plugins/surfaces', () => ({
  getSurfaces: vi.fn(() => []),
  onSurfacesChanged: vi.fn(() => () => {})
}))

function makePlugin(comp: unknown): RegisteredPlugin {
  return {
    manifest: { id: 'crasher', name: 'Crasher', version: '1.0.0' },
    component: {},
    settingsPageComponent: comp,
    source: 'first-party'
  } as unknown as RegisteredPlugin
}

describe('PluginSettingsPanel error boundary (#357)', () => {
  beforeEach(() => {
    cleanup()
  })

  it('renders the fallback when the settings page throws on mount', () => {
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
    render(PluginSettingsPanel, {
      props: {
        plugin: makePlugin(CrashingSettings),
        activeNotebook: 'N',
        activeSection: '',
        activePage: ''
      }
    })

    // The fallback card is rendered, not the thrown error bubbling up.
    expect(screen.getByText(/Crasher settings failed to load/i)).toBeTruthy()
    expect(screen.getByText(/boom from CrashingSettings/)).toBeTruthy()
    // The onerror hook logged the crash.
    expect(errorSpy).toHaveBeenCalled()
    errorSpy.mockRestore()
  })

  it('re-mounts the component when Retry is clicked', async () => {
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
    render(PluginSettingsPanel, {
      props: {
        plugin: makePlugin(CrashingSettings),
        activeNotebook: 'N',
        activeSection: '',
        activePage: ''
      }
    })

    // Retry calls reset(), which re-runs CrashingSettings init → throws again
    // → fallback re-renders. The crash count going up confirms re-mount.
    const before = errorSpy.mock.calls.length
    await fireEvent.click(screen.getByRole('button', { name: /retry/i }))
    expect(errorSpy.mock.calls.length).toBeGreaterThan(before)
    // Fallback is still showing (the fixture always throws).
    expect(screen.getByText(/Crasher settings failed to load/i)).toBeTruthy()
    errorSpy.mockRestore()
  })
})
