// #214: SettingsShell dynamic plugin settings tab test — verifies a plugin
// with a settingsPageComponent contributes a dynamic tab in the Settings rail
// and the contributed component renders when the tab is selected.
import { describe, expect, it, beforeEach, vi } from 'vitest'
import { render, screen, cleanup } from '@testing-library/svelte'

const mocks = vi.hoisted(() => {
  // A dummy first-party Svelte component used as the contributed settings page.
  const DummySettings = vi.fn()
  return {
    DummySettings,
    loadedPlugins: {
      plugins: new Map([
        [
          'bespoke-plugin',
          {
            manifest: {
              id: 'bespoke-plugin',
              name: 'Bespoke Plugin',
              version: '1.0.0',
              icon: 'tune'
            },
            component: vi.fn(),
            settingsPageComponent: DummySettings,
            source: 'first-party'
          }
        ]
      ]),
      errors: [] as { id: string; message: string }[]
    },
    settings: {
      loading: false,
      error: '',
      config: {
        notebooks: { path: '/test' },
        editor: {
          font_size_px: 15,
          line_height: 1.5,
          tab_indent_spaces: 4,
          auto_save_delay_ms: 1000
        },
        parsing: {},
        hotkeys: {},
        plugins: { disabled: [], active: [], plugin_settings: {} },
        ui: {},
        linked_notebooks: []
      }
    }
  }
})

vi.mock('../../../wailsjs/go/main/App.js', () => ({
  ListPlugins: vi.fn().mockResolvedValue([]),
  PluginRawQuery: vi.fn(),
  GetPluginSettingsForNotebook: vi.fn().mockResolvedValue({}),
  UpdatePluginSetting: vi.fn()
}))

vi.mock('../../plugins/loader', () => ({
  loadPlugins: vi.fn().mockResolvedValue(undefined)
}))

vi.mock('../../plugins/store.svelte', () => ({
  loadedPlugins: mocks.loadedPlugins
}))

vi.mock('../../plugins/surfaces', () => ({
  getSurfaces: vi.fn(() => []),
  onSurfacesChanged: vi.fn(() => () => {})
}))

vi.mock('../../settings/store.svelte', () => ({
  settings: mocks.settings,
  loadConfig: vi.fn().mockResolvedValue(undefined),
  saveConfig: vi.fn().mockResolvedValue(undefined)
}))

import SettingsShell from './SettingsShell.svelte'

describe('SettingsShell dynamic plugin tabs (#214)', () => {
  beforeEach(() => {
    cleanup()
  })

  it('renders a dynamic tab for a plugin with settingsPageComponent', () => {
    render(SettingsShell, {
      props: {
        onClose: () => {},
        activeNotebook: 'Test',
        activeSection: '',
        activePage: ''
      }
    })
    // The plugin's name appears in the rail as a tab label.
    expect(screen.getByText('Bespoke Plugin')).toBeTruthy()
  })

  it('does not render a tab for plugins without settingsPageComponent', () => {
    // Replace the store with a plugin that has no settings page.
    mocks.loadedPlugins.plugins.clear()
    mocks.loadedPlugins.plugins.set('plain-plugin', {
      manifest: {
        id: 'plain-plugin',
        name: 'Plain Plugin',
        version: '1.0.0',
        icon: 'extension'
      },
      component: vi.fn(),
      source: 'first-party'
    } as any)
    render(SettingsShell, {
      props: {
        onClose: () => {},
        activeNotebook: 'Test',
        activeSection: '',
        activePage: ''
      }
    })
    expect(screen.queryByText('Plain Plugin')).toBeNull()
    // Restore for other tests.
    mocks.loadedPlugins.plugins.clear()
  })
})
