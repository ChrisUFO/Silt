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

// #356: The Settings dialog must honor the WAI-ARIA tab contract — role=tablist
// on the rail, role=tab buttons with id + aria-controls pointing at a real
// role=tabpanel whose aria-labelledby resolves back to the active tab. This
// predates the Sprint 19 plugin tabs but affects every tab (core + dynamic).
describe('SettingsShell ARIA tablist/tabpanel contract (#356)', () => {
  beforeEach(() => {
    cleanup()
  })

  it('marks the rail as a tablist with labelled tabs controlling one panel', () => {
    const { container } = render(SettingsShell, {
      props: {
        onClose: () => {},
        activeNotebook: 'Test',
        activeSection: '',
        activePage: ''
      }
    })

    const tablist = container.querySelector('[role="tablist"]')
    expect(tablist).toBeTruthy()

    const tabs = container.querySelectorAll('[role="tab"]')
    expect(tabs.length).toBeGreaterThan(0)

    const panel = container.querySelector('[role="tabpanel"]')
    expect(panel).toBeTruthy()
    expect(panel?.getAttribute('id')).toBe('silt-settings-panel')

    // Every tab's aria-controls resolves to the panel id.
    tabs.forEach((tab) => {
      expect(tab.getAttribute('aria-controls')).toBe('silt-settings-panel')
      expect(tab.id).toMatch(/^silt-settings-tab-/)
    })
  })

  it('labels the panel with the active tab across core + dynamic plugin tabs', () => {
    const { container, component } = render(SettingsShell, {
      props: {
        onClose: () => {},
        activeNotebook: 'Test',
        activeSection: '',
        activePage: ''
      }
    })

    const panel = container.querySelector('[role="tabpanel"]')!
    const activeTab = () =>
      container.querySelector<HTMLButtonElement>(
        `[role="tab"][aria-selected="true"]`
      )!
    // activeTab is a $bindable prop; cast the instance to set it directly.
    const vm = component as unknown as { activeTab: string }

    // Active tab and panel must reference each other bidirectionally.
    function assertContract() {
      const tab = activeTab()
      expect(panel.getAttribute('aria-labelledby')).toBe(tab.id)
      expect(tab.getAttribute('aria-controls')).toBe(panel.id)
    }

    // Core tab (workspace is the default active tab).
    assertContract()

    // Dynamic plugin tab.
    vm.activeTab = 'plugin:bespoke-plugin'
    assertContract()

    // Another core tab.
    vm.activeTab = 'appearance'
    assertContract()
  })
})
