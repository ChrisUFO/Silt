// PluginsTab regression: cfg.plugins may be undefined when a hand-edited
// config.yaml omits the section. Without the guard added in the Sprint 4
// PR review, the toggle path would throw a TypeError on
// `cfg.plugins.disabled`. This test ensures the disabled-first-party flow
// is defensive against that schema drift.

import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest'
import { tick } from 'svelte'
import { render, screen, cleanup, fireEvent } from '@testing-library/svelte'

const mocks = vi.hoisted(() => ({
  listPlugins: vi.fn(),
  loadPlugins: vi.fn(),
  // First-party list mirrors what the real registry exports (a getter).
  firstPartyPluginsFn: vi.fn(() => [
    {
      manifest: {
        id: 'silt-kanban',
        name: 'Kanban',
        version: '1.0.0',
        author: 'Silt',
        description: '',
        icon: 'view_kanban'
      }
    }
  ]),
  loadedPlugins: {
    plugins: new Map(),
    errors: [] as { id: string; message: string }[]
  },
  // Mutable config (no `plugins` key) to exercise the guard.
  configNoPlugins: {} as any,
  saveConfig: vi.fn(),
  setConfig: (next: any) => {
    mocks.configNoPlugins = next
  }
}))

vi.mock('../../../wailsjs/go/main/App.js', () => ({
  ListPlugins: mocks.listPlugins,
  ValidatePluginArchive: vi.fn(),
  InstallPlugin: vi.fn(),
  UninstallPlugin: vi.fn(),
  EnablePlugin: vi.fn(),
  DisablePlugin: vi.fn(),
  PickPluginArchive: vi.fn()
}))

vi.mock('../../plugins/loader', () => ({
  loadPlugins: mocks.loadPlugins
}))

vi.mock('../../plugins/registry', () => ({
  firstPartyPlugins: mocks.firstPartyPluginsFn
}))

vi.mock('../../plugins/store.svelte', () => ({
  loadedPlugins: mocks.loadedPlugins
}))

vi.mock('../../settings/store.svelte', () => ({
  settings: {
    get config() {
      return mocks.configNoPlugins
    }
  },
  saveConfig: mocks.saveConfig
}))

import PluginsTab from './PluginsTab.svelte'

async function flush() {
  await tick()
  await new Promise((r) => setTimeout(r, 0))
}

describe('PluginsTab first-party disable guard', () => {
  beforeEach(() => {
    mocks.listPlugins.mockReset()
    mocks.loadPlugins.mockReset()
    mocks.saveConfig.mockReset()
    mocks.listPlugins.mockResolvedValue([])
    mocks.loadPlugins.mockResolvedValue(undefined)
    mocks.saveConfig.mockResolvedValue(true)
    mocks.configNoPlugins = {} // no `plugins` key
  })

  afterEach(() => {
    cleanup()
  })

  it('does not throw when toggling a first-party plugin and cfg.plugins is missing', async () => {
    render(PluginsTab, {
      activeNotebook: 'Work',
      activeSection: 'Journal',
      activePage: 'Daily'
    })
    await flush()

    // Locate the Kanban card (the only first-party plugin in the mock).
    const kanbanCard = screen.getByText('Kanban').closest('div')
    expect(kanbanCard).toBeTruthy()

    // The Disable toggle is a button with aria-label="Disable" inside the
    // first-party card row. Click it; without the guard, this throws
    // "Cannot read properties of undefined (reading 'disabled')".
    const disableBtn = screen.getByRole('button', { name: 'Disable' })

    await expect(fireEvent.click(disableBtn)).resolves.not.toThrow()

    // saveConfig must have been called with a normalized config that
    // includes the disabled plugin id.
    expect(mocks.saveConfig).toHaveBeenCalledTimes(1)
    const saved = mocks.saveConfig.mock.calls[0][0]
    expect(saved.plugins).toBeTruthy()
    expect(saved.plugins.disabled).toContain('silt-kanban')
  })
})
