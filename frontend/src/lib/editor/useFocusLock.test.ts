import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { FocusLockManager } from './useFocusLock'
import type { FocusLockDeps } from './useFocusLock'

// Mock the Wails IPC bindings.
vi.mock('../../../wailsjs/go/main/App.js', () => ({
  AcquireFocusLock: vi.fn().mockResolvedValue(undefined),
  ReleaseFocusLock: vi.fn().mockResolvedValue(undefined),
  RefreshFocusLock: vi.fn().mockResolvedValue(undefined)
}))

function makeDeps(overrides: Partial<FocusLockDeps> = {}): FocusLockDeps {
  return {
    notebook: 'Work',
    section: 'Journal',
    page: '2026-06-22',
    getEditor: () => null,
    onBlockFocus: vi.fn(),
    ...overrides
  }
}

describe('FocusLockManager', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('acquires and releases the lock', async () => {
    const { AcquireFocusLock, ReleaseFocusLock } = await import(
      '../../../wailsjs/go/main/App.js'
    )
    const lock = new FocusLockManager(makeDeps())

    await lock.acquire()
    expect(lock.locked).toBe(true)
    expect(AcquireFocusLock).toHaveBeenCalledWith('Work', 'Journal', '2026-06-22')

    await lock.release()
    expect(lock.locked).toBe(false)
    expect(ReleaseFocusLock).toHaveBeenCalledWith('Work', 'Journal', '2026-06-22')
  })

  it('release() is a no-op when not locked', async () => {
    const { ReleaseFocusLock } = await import('../../../wailsjs/go/main/App.js')
    const lock = new FocusLockManager(makeDeps())

    await lock.release()
    expect(ReleaseFocusLock).not.toHaveBeenCalled()
  })

  it('heartbeat refreshes the lock periodically', async () => {
    const { RefreshFocusLock } = await import('../../../wailsjs/go/main/App.js')
    const lock = new FocusLockManager(makeDeps())

    lock.startHeartbeat()

    // After 20 seconds, one refresh should have fired.
    await vi.advanceTimersByTimeAsync(20000)
    expect(RefreshFocusLock).toHaveBeenCalledTimes(1)

    // After another 20 seconds, two total.
    await vi.advanceTimersByTimeAsync(20000)
    expect(RefreshFocusLock).toHaveBeenCalledTimes(2)

    lock.stopHeartbeat()
  })

  it('stopHeartbeat stops the refresh cycle', async () => {
    const { RefreshFocusLock } = await import('../../../wailsjs/go/main/App.js')
    const lock = new FocusLockManager(makeDeps())

    lock.startHeartbeat()
    lock.stopHeartbeat()

    await vi.advanceTimersByTimeAsync(60000)
    expect(RefreshFocusLock).not.toHaveBeenCalled()
  })

  it('startHeartbeat restarts (does not stack)', async () => {
    const { RefreshFocusLock } = await import('../../../wailsjs/go/main/App.js')
    const lock = new FocusLockManager(makeDeps())

    lock.startHeartbeat()
    lock.startHeartbeat() // should not create two intervals

    await vi.advanceTimersByTimeAsync(20000)
    expect(RefreshFocusLock).toHaveBeenCalledTimes(1)

    lock.stopHeartbeat()
  })

  it('acquire logs error but does not throw on IPC failure', async () => {
    const { AcquireFocusLock } = await import('../../../wailsjs/go/main/App.js')
    vi.mocked(AcquireFocusLock).mockRejectedValueOnce(new Error('IPC fail'))
    const lock = new FocusLockManager(makeDeps())

    // Should not throw.
    await lock.acquire()
    expect(lock.locked).toBe(false)
  })

  it('notifyFocus is a no-op when editor is null', () => {
    const deps = makeDeps({ getEditor: () => null })
    const lock = new FocusLockManager(deps)

    // Should not throw.
    lock.notifyFocus()
    expect(deps.onBlockFocus).not.toHaveBeenCalled()
  })
})
