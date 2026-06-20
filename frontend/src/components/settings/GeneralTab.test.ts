// Component coverage for the font picker (#82) — the explicit PLAN Phase 2
// deliverable. fonts.test.ts covers the registry; this renders GeneralTab and
// asserts the picker interaction: the combobox reflects the current value, the
// "Reset to theme default" button appears only when the active theme overrides
// the font, and clicking it clears the config field.

import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest'
import { tick } from 'svelte'
import { render, screen, cleanup, fireEvent } from '@testing-library/svelte'

const mocks = vi.hoisted(() => {
  // A minimal valid SystemConfig (matches config.SystemConfig shape).
  const baseConfig = {
    notebooks: { path: '/vault', default_active: 'Work' },
    editor: {
      font_family: 'Plus Jakarta Sans',
      mono_font_family: 'JetBrains Mono',
      font_size_px: 14,
      line_height: 1.6,
      tab_indent_spaces: 4,
      auto_save_delay_ms: 500,
      focus_highlight_ancestors: true
    },
    parsing: {
      auto_inject_uuid: true,
      shorthand_regex: '.*',
      default_task_priority: 3
    },
    hotkeys: { open_search: 'Ctrl+P' },
    plugins: { active: [], disabled: [], plugin_settings: {} }
  }
  return {
    settings: {
      config: baseConfig,
      loading: false,
      saving: false,
      error: '',
      dirty: false,
      pendingExternal: false
    },
    saveConfig: vi.fn(async () => true),
    reloadFromBackend: vi.fn(async () => {}),
    // themeState: darkTokens carries the theme typography overrides. Both
    // modes carry the same --font-* (theme-level), so darkTokens suffices.
    themeState: {
      id: 'cyber_forest',
      name: 'Cyber Forest',
      mode: 'dark' as 'dark' | 'light' | 'system',
      darkTokens: {
        '--color-void': '#0c0c0e',
        '--font-body': "'Plus Jakarta Sans', sans-serif",
        '--font-mono': "'JetBrains Mono', monospace",
        '--font-headline': "'Hanken Grotesk', sans-serif"
      } as Record<string, string>,
      lightTokens: {} as Record<string, string>,
      error: null as string | null
    }
  }
})

vi.mock('../../../wailsjs/runtime/runtime.js', () => ({
  EventsOn: vi.fn(),
  EventsOff: vi.fn(),
  EventsEmit: vi.fn()
}))

// VaultActionModal + VaultArchiveModal (rendered as children when a relocate
// or archive action is chosen) import the wailsjs binding module, so it must
// be mocked here too. VaultArchiveModal also subscribes to the
// vault:archive:progress event, so the runtime module is mocked as well.
const appMocks = vi.hoisted(() => ({
  PickVaultDestination: vi.fn(),
  MoveVault: vi.fn(),
  CopyVault: vi.fn(),
  SwitchVault: vi.fn(),
  PickVaultExportPath: vi.fn(),
  ExportVault: vi.fn(),
  PickVaultArchive: vi.fn(),
  ImportVault: vi.fn()
}))
vi.mock('../../../wailsjs/go/main/App.js', () => appMocks)
vi.mock('../../../wailsjs/runtime/runtime.js', () => ({
  EventsOn: vi.fn(() => () => {}),
  EventsOff: vi.fn(),
  EventsEmit: vi.fn()
}))

vi.mock('../../settings/store.svelte', () => ({
  settings: mocks.settings,
  saveConfig: mocks.saveConfig,
  reloadFromBackend: mocks.reloadFromBackend
}))
vi.mock('../../theme/store.svelte', () => ({ themeState: mocks.themeState }))

import GeneralTab from './GeneralTab.svelte'

function resetThemeState(withTypography: boolean) {
  mocks.themeState.darkTokens = withTypography
    ? {
        '--color-void': '#0c0c0e',
        '--font-body': "'Plus Jakarta Sans', sans-serif",
        '--font-mono': "'JetBrains Mono', monospace"
      }
    : { '--color-void': '#0c0c0e' }
}

describe('GeneralTab font picker (#82)', () => {
  beforeEach(() => {
    // Each test starts from the saved config + clean dirty flag.
    mocks.settings.config.editor.font_family = 'Plus Jakarta Sans'
    mocks.settings.config.editor.mono_font_family = 'JetBrains Mono'
    mocks.settings.dirty = false
    mocks.saveConfig.mockClear()
  })
  afterEach(() => cleanup())

  it('renders a combobox for the body font reflecting the current config value', async () => {
    resetThemeState(true)
    render(GeneralTab)
    await tick()
    const combo = screen.getByRole('combobox', { name: 'Font family' })
    expect(combo).toBeInTheDocument()
    // The trigger shows the current family (rendered in-font).
    expect(combo.textContent).toContain('Plus Jakarta Sans')
  })

  it('shows a Reset button for the body font when the theme overrides it', async () => {
    resetThemeState(true)
    render(GeneralTab)
    await tick()
    expect(
      screen.getByLabelText('Reset body font to theme default')
    ).toBeInTheDocument()
  })

  it('hides the Reset button when the active theme has no typography override', async () => {
    resetThemeState(false)
    render(GeneralTab)
    await tick()
    expect(
      screen.queryByLabelText('Reset body font to theme default')
    ).toBeNull()
    expect(
      screen.queryByLabelText('Reset monospace font to theme default')
    ).toBeNull()
  })

  it('clicking Reset clears the config field (so the CSS fallback resolves to the theme font)', async () => {
    resetThemeState(true)
    render(GeneralTab)
    await tick()
    const reset = screen.getByLabelText('Reset body font to theme default')
    await fireEvent.click(reset)
    await tick()
    // The config field is now empty; the combobox shows the theme-default
    // leading option (which is only present because the theme overrides it).
    const combo = screen.getByRole('combobox', { name: 'Font family' })
    expect(combo.textContent).toContain('Theme default')
    // The edit marked the form dirty (Save path).
    expect(mocks.settings.dirty).toBe(true)
  })
})

describe('GeneralTab vault relocate menu (#141)', () => {
  beforeEach(() => {
    mocks.settings.dirty = false
    appMocks.PickVaultDestination.mockClear()
    appMocks.MoveVault.mockClear()
    appMocks.CopyVault.mockClear()
    appMocks.SwitchVault.mockClear()
    appMocks.PickVaultExportPath.mockClear()
    appMocks.ExportVault.mockClear()
    appMocks.PickVaultArchive.mockClear()
    appMocks.ImportVault.mockClear()
  })
  afterEach(() => cleanup())

  it('renders the vault actions kebab button', async () => {
    render(GeneralTab)
    await tick()
    expect(
      screen.getByRole('button', { name: 'Vault actions' })
    ).toBeInTheDocument()
  })

  it('opening the menu reveals Move, Copy, Export, and Import actions', async () => {
    render(GeneralTab)
    await tick()
    await fireEvent.click(screen.getByRole('button', { name: 'Vault actions' }))
    await tick()
    expect(
      screen.getByRole('menuitem', { name: /Move vault/ })
    ).toBeInTheDocument()
    expect(
      screen.getByRole('menuitem', { name: /Copy vault/ })
    ).toBeInTheDocument()
    expect(
      screen.getByRole('menuitem', { name: /Export vault/ })
    ).toBeInTheDocument()
    expect(
      screen.getByRole('menuitem', { name: /Import vault/ })
    ).toBeInTheDocument()
  })

  it('selecting Move opens the VaultActionModal in move mode', async () => {
    render(GeneralTab)
    await tick()
    await fireEvent.click(screen.getByRole('button', { name: 'Vault actions' }))
    await tick()
    await fireEvent.click(screen.getByRole('menuitem', { name: /Move vault/ }))
    await tick()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: 'Move vault' })
    ).toBeInTheDocument()
  })

  it('selecting Export opens the VaultArchiveModal in export mode', async () => {
    render(GeneralTab)
    await tick()
    await fireEvent.click(screen.getByRole('button', { name: 'Vault actions' }))
    await tick()
    await fireEvent.click(
      screen.getByRole('menuitem', { name: /Export vault/ })
    )
    await tick()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: 'Export vault' })
    ).toBeInTheDocument()
  })

  it('selecting Import opens the VaultArchiveModal in import mode', async () => {
    render(GeneralTab)
    await tick()
    await fireEvent.click(screen.getByRole('button', { name: 'Vault actions' }))
    await tick()
    await fireEvent.click(
      screen.getByRole('menuitem', { name: /Import vault/ })
    )
    await tick()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: 'Import vault' })
    ).toBeInTheDocument()
  })

  it('Escape on a menu item collapses the menu', async () => {
    render(GeneralTab)
    await tick()
    await fireEvent.click(screen.getByRole('button', { name: 'Vault actions' }))
    await tick()
    const moveItem = screen.getByRole('menuitem', { name: /Move vault/ })
    moveItem.focus()
    await fireEvent.keyDown(moveItem, { key: 'Escape' })
    await tick()
    expect(screen.queryByRole('menuitem', { name: /Move vault/ })).toBeNull()
  })

  it('clicking outside the menu collapses it', async () => {
    render(GeneralTab)
    await tick()
    await fireEvent.click(screen.getByRole('button', { name: 'Vault actions' }))
    await tick()
    expect(
      screen.getByRole('menuitem', { name: /Move vault/ })
    ).toBeInTheDocument()
    // A window click outside the menu wrapper closes it.
    await fireEvent.click(document.body)
    await tick()
    expect(screen.queryByRole('menuitem', { name: /Move vault/ })).toBeNull()
  })
})
