import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest'
import {
  notificationsState,
  pushNotification,
  dismissNotification,
  clearAllNotifications,
  _resetForTests
} from './store.svelte'

describe('notifications/store.svelte (#86)', () => {
  beforeEach(() => {
    _resetForTests()
  })

  afterEach(() => {
    _resetForTests()
  })

  it('pushNotification appends to the live list', () => {
    pushNotification({ kind: 'info', message: 'hello' })
    expect(notificationsState.items).toHaveLength(1)
    expect(notificationsState.items[0].kind).toBe('info')
    expect(notificationsState.items[0].message).toBe('hello')
    expect(notificationsState.items[0].id).toBeGreaterThan(0)
  })

  it('pushNotification returns the new id', () => {
    const id1 = pushNotification({ kind: 'info', message: 'a' })
    const id2 = pushNotification({ kind: 'info', message: 'b' })
    expect(id2).toBeGreaterThan(id1)
  })

  it('errors are sticky (no auto-dismiss) by default', () => {
    pushNotification({ kind: 'error', message: 'boom' })
    const n = notificationsState.items[0]
    expect(n.autoDismissMs).toBe(0)
  })

  it('info and success auto-dismiss by default', () => {
    pushNotification({ kind: 'info', message: 'i' })
    pushNotification({ kind: 'success', message: 's' })
    expect(notificationsState.items[0].autoDismissMs).toBe(5000)
    expect(notificationsState.items[1].autoDismissMs).toBe(5000)
  })

  it('dismissNotification removes from the list', () => {
    const id = pushNotification({ kind: 'info', message: 'x' })
    expect(notificationsState.items).toHaveLength(1)
    dismissNotification(id)
    expect(notificationsState.items).toHaveLength(0)
  })

  it('dismissNotification is a no-op for unknown ids', () => {
    pushNotification({ kind: 'info', message: 'x' })
    dismissNotification(99999)
    expect(notificationsState.items).toHaveLength(1)
  })

  it('action callback can be attached', () => {
    const action = vi.fn()
    pushNotification({ kind: 'error', message: 'try again', action: { label: 'Retry', run: action } })
    const n = notificationsState.items[0]
    expect(n.action?.label).toBe('Retry')
    expect(n.action?.run).toBe(action)
  })

  it('clearAllNotifications empties the list', () => {
    pushNotification({ kind: 'info', message: 'a' })
    pushNotification({ kind: 'error', message: 'b' })
    clearAllNotifications()
    expect(notificationsState.items).toHaveLength(0)
  })

  it('auto-dismiss fires after the configured delay', async () => {
    vi.useFakeTimers()
    pushNotification({ kind: 'info', message: 'auto', autoDismissMs: 1000 })
    expect(notificationsState.items).toHaveLength(1)
    vi.advanceTimersByTime(1100)
    expect(notificationsState.items).toHaveLength(0)
    vi.useRealTimers()
  })
})
