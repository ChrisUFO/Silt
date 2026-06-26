// Update store (#312): the reactive bridge between the About-tab update UI
// and the backend/updates Go package. Mirrors the pattern of
// notifications/store.svelte.ts and theme/store.svelte.ts (Svelte 5 $state
// runes in a .svelte.ts module).
//
// Two check paths share one backend binding (CheckForUpdates):
//   - checkNow(): manual, surfaces errors in updateState so the About tab can
//     show "couldn't check".
//   - startupCheck(): fires from App.svelte onMount under the 24h throttle;
//     on any failure it stays silent (AC5: quiet on startup) and only raises
//     a toast when an update exists.

import {
  CheckForUpdates,
  DownloadUpdate,
  InstallUpdate,
  GetUpdateSettings,
  SetUpdateSettings
} from '../../wailsjs/go/main/App.js'
import {
  EventsOn,
  BrowserOpenURL,
  Quit
} from '../../wailsjs/runtime/runtime.js'
import { pushNotification } from '../notifications/store.svelte'

export type UpdateStatus =
  | 'idle'
  | 'checking'
  | 'up-to-date'
  | 'available'
  | 'downloading'
  | 'installing'
  | 'error'
export interface UpdateState {
  status: UpdateStatus
  latestVersion: string
  releaseUrl: string
  releaseNotes: string
  /** Platform-matching asset download URL ('' when none for this platform). */
  assetUrl: string
  /** 0–100 during 'downloading', else null. */
  downloadProgress: number | null
  /** ISO timestamp of the last check, '' when never checked. */
  lastChecked: string
  autoCheck: boolean
  /** True while SetUpdateSettings is in flight; disables the toggle button. */
  autoCheckInflight: boolean
  /** User-facing error message for the 'error' status. */
  error: string
}

export const updateState: UpdateState = $state({
  status: 'idle',
  latestVersion: '',
  releaseUrl: '',
  releaseNotes: '',
  assetUrl: '',
  downloadProgress: null,
  lastChecked: '',
  autoCheck: true,
  autoCheckInflight: false,
  error: ''
})

let progressUnsub: (() => void) | null = null

/** loadSettings hydrates autoCheck + lastChecked from settings.json and caches
 * the backend's throttled startup decision (single source of truth for the 24h
 * rule — the frontend never duplicates the constant). */
export async function loadSettings(): Promise<void> {
  try {
    const s = await GetUpdateSettings()
    updateState.autoCheck = s.autoCheck
    updateState.lastChecked = s.lastCheck ?? ''
    cachedShouldAutoCheck = !!s.shouldAutoCheck
  } catch {
    // Non-fatal: defaults (autoCheck true, never checked) keep the startup
    // check working even if settings.json is momentarily unreadable.
  }
}

// cachedShouldAutoCheck holds the backend's throttled startup decision from
// the last loadSettings() call. Default true so a first-run (never checked)
// always fires the startup check.
let cachedShouldAutoCheck = true

/** setAutoCheck persists the toggle and updates the local state. Guarded
 * against re-entrancy: a second click while a save is in flight is ignored so
 * two writes cannot race and leave the toggle out of sync with disk. */
export async function setAutoCheck(on: boolean): Promise<void> {
  if (updateState.autoCheckInflight) return
  updateState.autoCheckInflight = true
  updateState.autoCheck = on
  try {
    await SetUpdateSettings(on)
  } catch (e) {
    // Revert the optimistic flip so the toggle reflects on-disk truth.
    updateState.autoCheck = !on
    pushNotification({
      kind: 'error',
      message: 'Could not save the update-check preference.'
    })
    console.error('SetUpdateSettings failed:', e)
  } finally {
    updateState.autoCheckInflight = false
  }
}

interface CheckResult {
  hasUpdate: boolean
  latestVersion: string
  releaseUrl: string
  releaseNotes: string
  asset?: { browserDownloadUrl?: string; name?: string; size?: number } | null
}

function applyCheckResult(r: CheckResult): void {
  updateState.latestVersion = r.latestVersion ?? ''
  updateState.releaseUrl = r.releaseUrl ?? ''
  updateState.releaseNotes = r.releaseNotes ?? ''
  updateState.assetUrl = r.asset?.browserDownloadUrl ?? ''
  updateState.status = r.hasUpdate ? 'available' : 'up-to-date'
  updateState.error = ''
}

/** checkNow runs a manual check (always immediate, errors surfaced). */
export async function checkNow(): Promise<void> {
  updateState.status = 'checking'
  updateState.error = ''
  try {
    const r = (await CheckForUpdates()) as CheckResult
    applyCheckResult(r)
    await loadSettings()
  } catch (e) {
    updateState.status = 'error'
    updateState.error = friendlyCheckError(e)
  }
}

/**
 * startupCheck runs from App.svelte onMount. Quiet on failure (AC5): no error
 * status, no toast. On a successful check it BOTH records the result in
 * updateState (so the About panel reflects a startup-discovered update instead
 * of showing idle) AND raises a non-blocking toast with a View action (AC2).
 * Safe to call when no vault is open — the check is user-global, not
 * vault-scoped.
 */
export async function startupCheck(): Promise<void> {
  let r: CheckResult
  try {
    r = (await CheckForUpdates()) as CheckResult
  } catch {
    // Offline / rate-limited / parse error: stay silent on startup.
    return
  }
  // Reflect the discovery in the shared state so the About panel (if opened
  // after the toast) shows the available update without a manual re-check.
  applyCheckResult(r)
  if (!r.hasUpdate) return
  const url = r.releaseUrl
  pushNotification({
    kind: 'info',
    message: `Silt ${r.latestVersion} is available.`,
    action: {
      label: 'View',
      run: () => {
        if (url) BrowserOpenURL(url)
      }
    },
    autoDismissMs: 15_000
  })
}

/** downloadAndInstall runs the download → verify → install flow. */
export async function downloadAndInstall(assetUrl: string): Promise<void> {
  updateState.status = 'downloading'
  updateState.downloadProgress = 0
  updateState.error = ''
  // Subscribe to progress events for this download; unsubscribe on completion.
  subscribeProgress()
  try {
    const localPath = await DownloadUpdate(assetUrl)
    updateState.status = 'installing'
    // Install launches the installer/relaunch. If willQuit, a self-replacing
    // installer was launched and the app must exit (via the graceful JS Quit,
    // which runs OnShutdown → vault/WAL flush) so it can replace the locked
    // binary. If not (Linux xdg-open hand-off), surface guidance and return to
    // 'available' so the user can retry or place the file manually.
    const res = (await InstallUpdate(localPath)) as { willQuit?: boolean }
    if (res?.willQuit) {
      // status stays 'installing' ("Silt will restart"); the window is exiting.
      Quit()
    } else {
      updateState.status = 'available'
      pushNotification({
        kind: 'info',
        message:
          'Opened the downloaded package — install it to finish the upgrade.'
      })
    }
  } catch (e) {
    updateState.status = 'error'
    updateState.downloadProgress = null
    updateState.error = friendlyInstallError(e)
  } finally {
    unsubscribeProgress()
  }
}

function subscribeProgress(): void {
  unsubscribeProgress()
  progressUnsub = EventsOn(
    'update:download:progress',
    (p: { received: number; total: number }) => {
      if (!p) return
      if (p.total > 0) {
        updateState.downloadProgress = Math.min(
          100,
          Math.round((p.received / p.total) * 100)
        )
      } else {
        // Unknown total: show an indeterminate hint via a negative sentinel
        // the UI can render as a busy bar.
        updateState.downloadProgress = -1
      }
    }
  )
}

function unsubscribeProgress(): void {
  if (progressUnsub) {
    progressUnsub()
    progressUnsub = null
  }
}

function friendlyCheckError(e: unknown): string {
  const msg = String(e instanceof Error ? e.message : e)
  if (/403|rate limit/i.test(msg))
    return 'GitHub rate-limited the check. Try again later.'
  if (/offline|network|connection|dial|timeout|no such host/i.test(msg))
    return "Couldn't reach GitHub — check your connection."
  return "Couldn't check for updates."
}

function friendlyInstallError(e: unknown): string {
  const msg = String(e instanceof Error ? e.message : e)
  if (/appimage updated but relaunch failed|restart manually/i.test(msg))
    return 'The update was installed. Restart Silt to finish the upgrade.'
  if (/SHA256|checksum|verification/i.test(msg))
    return 'The download failed its integrity check and was discarded.'
  if (/not in the latest release/i.test(msg))
    return 'The update is no longer the latest release. Re-check for updates.'
  return 'The update could not be installed.'
}

/**
 * initStartupUpdateCheck runs the throttled startup auto-check (AC2). Called
 * from App.svelte onMount. The 24h throttle decision comes from the backend
 * (GetUpdateSettings.shouldAutoCheck) — the frontend does not duplicate the
 * threshold. On a found update it raises a non-blocking toast. All failures
 * are silent on startup (AC5: quiet state). Safe before a vault is open.
 */
export async function initStartupUpdateCheck(): Promise<void> {
  await loadSettings()
  if (!cachedShouldAutoCheck) return
  await startupCheck()
}

/**
 * disposeUpdateStore tears down the update store's subscriptions (the
 * download-progress event listener). Called from App.svelte's onMount
 * cleanup. Naming reflects that this disposes general store state, not just
 * startup — a download in flight when the app tears down would otherwise leak
 * its progress subscription (though the process exit reclaims it anyway).
 */
export function disposeUpdateStore(): void {
  unsubscribeProgress()
}

/** Test-only: reset module state. */
export function _resetForTests(): void {
  unsubscribeProgress()
  updateState.status = 'idle'
  updateState.latestVersion = ''
  updateState.releaseUrl = ''
  updateState.releaseNotes = ''
  updateState.assetUrl = ''
  updateState.downloadProgress = null
  updateState.lastChecked = ''
  updateState.autoCheck = true
  updateState.autoCheckInflight = false
  updateState.error = ''
  cachedShouldAutoCheck = true
}
