// Unit coverage for updates/store.svelte (#312): the quit-after-install
// contract (T1) and the startup↔About state sharing (T3). The wailsjs binding
// + runtime modules are mocked via vi.hoisted + vi.mock so no real IPC or
// process-quit fires in a test.

import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest'
import {
  updateState,
  initStartupUpdateCheck,
  setAutoCheck,
  loadSettings,
  startupCheck,
  downloadAndInstall,
  _resetForTests
} from './store.svelte'
import {
  notificationsState,
  clearAllNotifications
} from '../notifications/store.svelte'

const mocks = vi.hoisted(() => ({
  CheckForUpdates: vi.fn(),
  DownloadUpdate: vi.fn(),
  InstallUpdate: vi.fn(),
  GetUpdateSettings: vi.fn(),
  SetUpdateSettings: vi.fn(),
  EventsOn: vi.fn(() => () => {}),
  BrowserOpenURL: vi.fn(),
  Quit: vi.fn()
}))

vi.mock('../../wailsjs/go/main/App.js', () => ({
  CheckForUpdates: mocks.CheckForUpdates,
  DownloadUpdate: mocks.DownloadUpdate,
  InstallUpdate: mocks.InstallUpdate,
  GetUpdateSettings: mocks.GetUpdateSettings,
  SetUpdateSettings: mocks.SetUpdateSettings
}))
vi.mock('../../wailsjs/runtime/runtime.js', () => ({
  EventsOn: mocks.EventsOn,
  BrowserOpenURL: mocks.BrowserOpenURL,
  Quit: mocks.Quit
}))

describe('updates/store.svelte (#312)', () => {
  beforeEach(() => {
    _resetForTests()
    clearAllNotifications()
    mocks.CheckForUpdates.mockReset()
    mocks.DownloadUpdate.mockReset()
    mocks.InstallUpdate.mockReset()
    mocks.GetUpdateSettings.mockReset()
    mocks.SetUpdateSettings.mockReset()
    mocks.BrowserOpenURL.mockReset()
    mocks.Quit.mockReset()
    mocks.EventsOn.mockReset()
    mocks.EventsOn.mockImplementation(() => () => {})
  })

  afterEach(() => {
    _resetForTests()
    clearAllNotifications()
  })

  describe('initStartupUpdateCheck (backend-decided 24h throttle)', () => {
    // The 24h truth table itself lives in Go (throttle_test.go); here we only
    // assert the frontend honors the backend's shouldAutoCheck decision and
    // does not duplicate the threshold locally.
    it('runs the check when the backend says shouldAutoCheck=true', async () => {
      mocks.GetUpdateSettings.mockResolvedValue({
        autoCheck: true,
        lastCheck: '',
        shouldAutoCheck: true
      })
      mocks.CheckForUpdates.mockResolvedValue({ hasUpdate: false })
      await initStartupUpdateCheck()
      expect(mocks.CheckForUpdates).toHaveBeenCalledTimes(1)
    })

    it('skips the check when the backend says shouldAutoCheck=false', async () => {
      mocks.GetUpdateSettings.mockResolvedValue({
        autoCheck: true,
        lastCheck: new Date().toISOString(),
        shouldAutoCheck: false
      })
      mocks.CheckForUpdates.mockResolvedValue({ hasUpdate: false })
      await initStartupUpdateCheck()
      expect(mocks.CheckForUpdates).not.toHaveBeenCalled()
    })

    it('still proceeds on a GetUpdateSettings failure (default-true)', async () => {
      mocks.GetUpdateSettings.mockRejectedValue(new Error('unreadable'))
      mocks.CheckForUpdates.mockResolvedValue({ hasUpdate: false })
      await initStartupUpdateCheck()
      expect(mocks.CheckForUpdates).toHaveBeenCalledTimes(1)
    })
  })

  describe('startupCheck (T3: shares state with the About panel)', () => {
    it('records a found update in updateState AND raises a toast', async () => {
      mocks.CheckForUpdates.mockResolvedValue({
        hasUpdate: true,
        latestVersion: '0.5.0',
        releaseUrl: 'https://example/release',
        releaseNotes: 'notes',
        asset: { browserDownloadUrl: 'https://example/asset.exe' }
      })
      await startupCheck()
      expect(updateState.status).toBe('available')
      expect(updateState.latestVersion).toBe('0.5.0')
      expect(updateState.assetUrl).toBe('https://example/asset.exe')
      expect(notificationsState.items.length).toBe(1)
      expect(notificationsState.items[0].message).toContain('0.5.0')
    })

    it('records up-to-date in updateState but raises NO toast', async () => {
      mocks.CheckForUpdates.mockResolvedValue({
        hasUpdate: false,
        latestVersion: '0.4.0',
        releaseUrl: 'https://example/release',
        releaseNotes: ''
      })
      await startupCheck()
      expect(updateState.status).toBe('up-to-date')
      expect(notificationsState.items.length).toBe(0)
    })

    it('stays silent and leaves state untouched on failure (AC5)', async () => {
      mocks.CheckForUpdates.mockRejectedValue(new Error('offline'))
      const before = updateState.status
      await startupCheck()
      expect(updateState.status).toBe(before) // idle
      expect(notificationsState.items.length).toBe(0)
    })

    it('falls back to opening the release URL when no platform asset exists', async () => {
      mocks.CheckForUpdates.mockResolvedValue({
        hasUpdate: true,
        latestVersion: '0.5.0',
        releaseUrl: 'https://example/release',
        releaseNotes: ''
      })
      await startupCheck()
      const action = notificationsState.items[0].action
      expect(action).toBeDefined()
      expect(action!.label).toBe('View')
      action!.run()
      expect(mocks.BrowserOpenURL).toHaveBeenCalledWith(
        'https://example/release'
      )
    })

    it('offers an Install action that runs the install flow when a platform asset exists', async () => {
      mocks.DownloadUpdate.mockResolvedValue('/tmp/asset.exe')
      mocks.InstallUpdate.mockResolvedValue({ willQuit: true })
      mocks.CheckForUpdates.mockResolvedValue({
        hasUpdate: true,
        latestVersion: '0.5.0',
        releaseUrl: 'https://example/release',
        releaseNotes: '',
        asset: { browserDownloadUrl: 'https://example/asset.exe' }
      })
      await startupCheck()
      const action = notificationsState.items[0].action
      expect(action).toBeDefined()
      expect(action!.label).toBe('Install')
      action!.run()
      // downloadAndInstall is async; wait for the first IPC call to land.
      await vi.waitFor(() =>
        expect(mocks.DownloadUpdate).toHaveBeenCalledWith(
          'https://example/asset.exe'
        )
      )
    })
  })

  describe('downloadAndInstall (T1: quit vs hand-off)', () => {
    it('calls JS Quit when the installer signals willQuit (self-replace)', async () => {
      mocks.DownloadUpdate.mockResolvedValue('/tmp/asset.exe')
      mocks.InstallUpdate.mockResolvedValue({ willQuit: true })
      // Seed an asset URL + available status so the flow has a URL.
      updateState.status = 'available'
      updateState.assetUrl = 'https://example/asset.exe'
      await downloadAndInstall('https://example/asset.exe')
      expect(mocks.InstallUpdate).toHaveBeenCalledWith('/tmp/asset.exe')
      expect(mocks.Quit).toHaveBeenCalledTimes(1)
      // status stays 'installing' (the app is exiting)
      expect(updateState.status).toBe('installing')
    })

    it('surfaces a hand-off toast and returns to available when willQuit is false (xdg-open)', async () => {
      mocks.DownloadUpdate.mockResolvedValue('/tmp/asset.deb')
      mocks.InstallUpdate.mockResolvedValue({ willQuit: false })
      updateState.status = 'available'
      updateState.assetUrl = 'https://example/asset.deb'
      await downloadAndInstall('https://example/asset.deb')
      expect(mocks.Quit).not.toHaveBeenCalled()
      expect(updateState.status).toBe('available')
      expect(notificationsState.items.length).toBe(1)
      expect(notificationsState.items[0].message).toMatch(
        /install it to finish/i
      )
    })

    it('enters the error state on a download/verify failure', async () => {
      mocks.DownloadUpdate.mockRejectedValue(
        new Error('downloaded asset failed SHA256 verification')
      )
      updateState.status = 'available'
      updateState.assetUrl = 'https://example/asset.exe'
      await downloadAndInstall('https://example/asset.exe')
      expect(updateState.status).toBe('error')
      expect(updateState.error).toMatch(/integrity check/i)
      expect(mocks.Quit).not.toHaveBeenCalled()
    })

    it('surfaces "restart manually" when the swap succeeded but relaunch failed (A4)', async () => {
      mocks.DownloadUpdate.mockResolvedValue('/tmp/asset.AppImage')
      mocks.InstallUpdate.mockRejectedValue(
        new Error('appimage updated but relaunch failed; restart manually')
      )
      updateState.status = 'available'
      updateState.assetUrl = 'https://example/asset.AppImage'
      await downloadAndInstall('https://example/asset.AppImage')
      // willQuit was false for this path → no Quit, app stays alive to show
      // the actionable message.
      expect(mocks.Quit).not.toHaveBeenCalled()
      expect(updateState.status).toBe('error')
      expect(updateState.error).toMatch(/restart silt to finish/i)
    })
  })

  describe('setAutoCheck (in-flight re-entrancy guard)', () => {
    it('ignores a second flip while a save is in flight', async () => {
      let resolveSave: () => void
      mocks.SetUpdateSettings.mockImplementation(
        () => new Promise<void>((r) => (resolveSave = r))
      )
      updateState.autoCheck = true
      const first = setAutoCheck(false) // begins the save, inflight=true
      const second = setAutoCheck(true) // should be ignored while first runs
      expect(updateState.autoCheckInflight).toBe(true)
      expect(mocks.SetUpdateSettings).toHaveBeenCalledTimes(1) // second skipped
      resolveSave!()
      await first
      await second // resolves immediately (it was a no-op)
      expect(updateState.autoCheckInflight).toBe(false)
      expect(updateState.autoCheck).toBe(false) // only the first flip landed
    })

    it('reverts the optimistic flip and toasts on save failure', async () => {
      mocks.SetUpdateSettings.mockRejectedValue(new Error('disk full'))
      updateState.autoCheck = true
      await setAutoCheck(false)
      expect(updateState.autoCheck).toBe(true) // reverted
      expect(updateState.autoCheckInflight).toBe(false)
      expect(notificationsState.items.length).toBe(1)
      expect(notificationsState.items[0].kind).toBe('error')
    })
  })
})
