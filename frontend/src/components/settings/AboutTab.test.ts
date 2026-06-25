// Component coverage for the About-tab update UI (#312). The IPC layer is
// mocked via vi.hoisted + vi.mock over the wailsjs binding + runtime modules
// (the canonical pattern — see AppearanceTab.test.ts / VaultArchiveModal.test.ts).
// The updates store is mocked so each test can drive updateState into a known
// status before render. No real IPC in a test.

import { describe, expect, it, afterEach, vi } from 'vitest'
import { tick } from 'svelte'
import { render, screen, cleanup, fireEvent } from '@testing-library/svelte'

const mocks = vi.hoisted(() => ({
  GetAppVersion: vi.fn(),
  BrowserOpenURL: vi.fn(),
  // Plain (non-$state) object the component reads from; mutated per test.
  updateState: {
    status: 'idle' as string,
    latestVersion: '',
    releaseUrl: '',
    releaseNotes: '',
    assetUrl: '',
    downloadProgress: null as number | null,
    lastChecked: '',
    autoCheck: true,
    error: ''
  },
  loadSettings: vi.fn(),
  checkNow: vi.fn(),
  downloadAndInstall: vi.fn(),
  setAutoCheck: vi.fn(),
  setAutoCheckImpl: null as ((_on: boolean) => void) | null
}))

vi.mock('../../../wailsjs/go/main/App.js', () => ({
  GetAppVersion: mocks.GetAppVersion
}))
vi.mock('../../../wailsjs/runtime/runtime.js', () => ({
  BrowserOpenURL: mocks.BrowserOpenURL,
  EventsOn: vi.fn(),
  EventsOff: vi.fn(),
  EventsEmit: vi.fn()
}))
vi.mock('../../updates/store.svelte', () => ({
  updateState: mocks.updateState,
  loadSettings: mocks.loadSettings,
  checkNow: mocks.checkNow,
  downloadAndInstall: mocks.downloadAndInstall,
  setAutoCheck: mocks.setAutoCheck
}))

import AboutTab from './AboutTab.svelte'

function resetState() {
  mocks.updateState.status = 'idle'
  mocks.updateState.latestVersion = ''
  mocks.updateState.releaseUrl = ''
  mocks.updateState.releaseNotes = ''
  mocks.updateState.assetUrl = ''
  mocks.updateState.downloadProgress = null
  mocks.updateState.lastChecked = ''
  mocks.updateState.autoCheck = true
  mocks.updateState.error = ''
}

describe('AboutTab update UI (#312)', () => {
  afterEach(() => {
    cleanup()
    vi.clearAllMocks()
    resetState()
  })

  it('renders the version, Check button, and auto-check switch on idle', async () => {
    mocks.GetAppVersion.mockResolvedValue('0.4.0')
    mocks.loadSettings.mockResolvedValue(undefined)
    render(AboutTab)
    await tick()
    // onMount awaits GetAppVersion; findByText polls until it resolves.
    expect(await screen.findByText(/Version 0\.4\.0/)).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: /Check for updates/i })
    ).toBeInTheDocument()
    // role=switch present, default on.
    const sw = screen.getByRole('switch', {
      name: /Automatically check for updates/i
    })
    expect(sw).toHaveAttribute('aria-checked', 'true')
    // Never checked when lastChecked is empty.
    expect(screen.getByText(/Last checked: Never/i)).toBeInTheDocument()
  })

  it('renders the up-to-date status when status is up-to-date', async () => {
    mocks.GetAppVersion.mockResolvedValue('0.4.0')
    mocks.loadSettings.mockResolvedValue(undefined)
    mocks.updateState.status = 'up-to-date'
    render(AboutTab)
    await tick()
    expect(screen.getByText(/You're up to date/i)).toBeInTheDocument()
  })

  it('renders the available status with notes excerpt, Install, and View notes', async () => {
    mocks.GetAppVersion.mockResolvedValue('0.4.0')
    mocks.loadSettings.mockResolvedValue(undefined)
    mocks.updateState.status = 'available'
    mocks.updateState.latestVersion = '0.5.0'
    mocks.updateState.releaseUrl =
      'https://github.com/Chelydra-Labs/Silt/releases/v0.5.0'
    mocks.updateState.releaseNotes = '## New\n- in-app updates\n- bugfix'
    mocks.updateState.assetUrl = 'https://example/asset.exe'
    render(AboutTab)
    await tick()
    expect(screen.getByText(/Silt 0\.5\.0 is available/i)).toBeInTheDocument()
    expect(screen.getByText(/in-app updates/i)).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: /Install update/i })
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: /View full notes/i })
    ).toBeInTheDocument()
  })

  it('does not render the Install button when no platform asset is available', async () => {
    mocks.GetAppVersion.mockResolvedValue('0.4.0')
    mocks.loadSettings.mockResolvedValue(undefined)
    mocks.updateState.status = 'available'
    mocks.updateState.latestVersion = '0.5.0'
    mocks.updateState.releaseUrl = 'https://example/release'
    mocks.updateState.assetUrl = '' // no asset for this platform
    render(AboutTab)
    await tick()
    expect(screen.queryByRole('button', { name: /Install update/i })).toBeNull()
    expect(
      screen.getByRole('button', { name: /View full notes/i })
    ).toBeInTheDocument()
  })

  it('renders a progressbar with the live percent while downloading', async () => {
    mocks.GetAppVersion.mockResolvedValue('0.4.0')
    mocks.loadSettings.mockResolvedValue(undefined)
    mocks.updateState.status = 'downloading'
    mocks.updateState.downloadProgress = 42
    render(AboutTab)
    await tick()
    const bar = screen.getByRole('progressbar')
    expect(bar).toHaveAttribute('aria-valuenow', '42')
    expect(screen.getByText(/Downloading/i)).toBeInTheDocument()
  })

  it('surfaces errors in a role=alert region', async () => {
    mocks.GetAppVersion.mockResolvedValue('0.4.0')
    mocks.loadSettings.mockResolvedValue(undefined)
    mocks.updateState.status = 'error'
    mocks.updateState.error = "Couldn't reach GitHub."
    render(AboutTab)
    await tick()
    const alert = screen.getByRole('alert')
    expect(alert).toHaveTextContent(/Couldn't reach GitHub/i)
  })

  it('calls checkNow when the Check button is clicked', async () => {
    mocks.GetAppVersion.mockResolvedValue('0.4.0')
    mocks.loadSettings.mockResolvedValue(undefined)
    mocks.checkNow.mockResolvedValue(undefined)
    render(AboutTab)
    await tick()
    await fireEvent.click(
      screen.getByRole('button', { name: /Check for updates/i })
    )
    expect(mocks.checkNow).toHaveBeenCalledTimes(1)
  })

  it('flips the auto-check switch and calls setAutoCheck with the new value', async () => {
    mocks.GetAppVersion.mockResolvedValue('0.4.0')
    mocks.loadSettings.mockResolvedValue(undefined)
    // setAutoCheck flips updateState.autoCheck optimistically; emulate that so
    // the rendered aria-checked reflects the post-click state.
    mocks.setAutoCheck.mockImplementation(async (on: boolean) => {
      mocks.updateState.autoCheck = on
    })
    render(AboutTab)
    await tick()
    const sw = screen.getByRole('switch', {
      name: /Automatically check for updates/i
    })
    expect(sw).toHaveAttribute('aria-checked', 'true')
    await fireEvent.click(sw)
    expect(mocks.setAutoCheck).toHaveBeenCalledWith(false)
    expect(mocks.updateState.autoCheck).toBe(false)
  })

  it('opens the release notes externally via BrowserOpenURL', async () => {
    mocks.GetAppVersion.mockResolvedValue('0.4.0')
    mocks.loadSettings.mockResolvedValue(undefined)
    mocks.updateState.status = 'available'
    mocks.updateState.latestVersion = '0.5.0'
    mocks.updateState.releaseUrl = 'https://example/release-notes'
    mocks.updateState.assetUrl = 'https://example/asset.exe'
    render(AboutTab)
    await tick()
    await fireEvent.click(
      screen.getByRole('button', { name: /View full notes/i })
    )
    expect(mocks.BrowserOpenURL).toHaveBeenCalledWith(
      'https://example/release-notes'
    )
  })
})
