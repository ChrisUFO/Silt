// Unit coverage for updates/store.svelte (#312): the quit-after-install
// contract (T1) and the startup↔About state sharing (T3). The wailsjs binding
// + runtime modules are mocked via vi.hoisted + vi.mock so no real IPC or
// process-quit fires in a test.

import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest'
import {
  updateState,
  shouldAutoCheck,
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
  EventsOn: vi.fn(() => () => {}),
  BrowserOpenURL: vi.fn(),
  Quit: vi.fn()
}))

vi.mock('../../wailsjs/go/main/App.js', () => ({
  CheckForUpdates: mocks.CheckForUpdates,
  DownloadUpdate: mocks.DownloadUpdate,
  InstallUpdate: mocks.InstallUpdate,
  GetUpdateSettings: vi.fn(async () => ({ autoCheck: true, lastCheck: '' })),
  SetUpdateSettings: vi.fn(async () => undefined)
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
    mocks.BrowserOpenURL.mockReset()
    mocks.Quit.mockReset()
    mocks.EventsOn.mockReset()
    mocks.EventsOn.mockImplementation(() => () => {})
  })

  afterEach(() => {
    _resetForTests()
    clearAllNotifications()
  })

  describe('shouldAutoCheck (24h throttle)', () => {
    it('never fires when autoCheck is off', () => {
      expect(shouldAutoCheck('', false)).toBe(false)
      expect(shouldAutoCheck(new Date(0).toISOString(), false)).toBe(false)
    })

    it('fires when never checked and auto on', () => {
      expect(shouldAutoCheck('', true)).toBe(true)
    })

    it('does not fire within 24h', () => {
      const oneHourAgo = new Date(Date.now() - 1 * 60 * 60 * 1000).toISOString()
      expect(shouldAutoCheck(oneHourAgo, true)).toBe(false)
    })

    it('fires at/after 24h', () => {
      const twoDaysAgo = new Date(
        Date.now() - 48 * 60 * 60 * 1000
      ).toISOString()
      expect(shouldAutoCheck(twoDaysAgo, true)).toBe(true)
    })

    it('treats an unparseable timestamp as never-checked', () => {
      expect(shouldAutoCheck('not-a-date', true)).toBe(true)
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

    it('opens the release URL via BrowserOpenURL when the toast action runs', async () => {
      mocks.CheckForUpdates.mockResolvedValue({
        hasUpdate: true,
        latestVersion: '0.5.0',
        releaseUrl: 'https://example/release',
        releaseNotes: ''
      })
      await startupCheck()
      const action = notificationsState.items[0].action
      expect(action).toBeDefined()
      action!.run()
      expect(mocks.BrowserOpenURL).toHaveBeenCalledWith(
        'https://example/release'
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
  })
})
