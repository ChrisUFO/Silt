// Notification store (#86): a global, app-wide channel for non-blocking
// status messages (toasts). Mirrors the pattern of theme/store.svelte.ts
// and settings/store.svelte.ts (Svelte 5 $state runes in a .svelte.ts
// module). Consumers (TipTapEditor, TemplatePicker, etc.) call
// pushNotification; ToastContainer.svelte renders the live stack.
import type { templates } from '../../wailsjs/go/models'

export type NotificationKind = 'info' | 'success' | 'error'

export interface NotificationAction {
  label: string
  run: () => void | Promise<void>
}

export interface Notification {
  id: number
  kind: NotificationKind
  message: string
  action?: NotificationAction
  /** Set to a non-zero value (ms) to auto-dismiss; 0 means sticky. */
  autoDismissMs: number
  createdAt: number
}

export interface NotificationsState {
  items: Notification[]
}

export const notificationsState: NotificationsState = $state({
  items: []
})

const DEFAULT_AUTO_DISMISS_MS: Record<NotificationKind, number> = {
  info: 5000,
  success: 5000,
  error: 0
}

let nextId = 1
let dismissTimers = new Map<number, ReturnType<typeof setTimeout>>()

export interface PushOptions {
  kind?: NotificationKind
  message: string
  action?: NotificationAction
  /** Override the default auto-dismiss for this kind. 0 = sticky. */
  autoDismissMs?: number
}

export function pushNotification(opts: PushOptions): number {
  const kind: NotificationKind = opts.kind ?? 'info'
  const id = nextId++
  const n: Notification = {
    id,
    kind,
    message: opts.message,
    action: opts.action,
    autoDismissMs: opts.autoDismissMs ?? DEFAULT_AUTO_DISMISS_MS[kind],
    createdAt: Date.now()
  }
  notificationsState.items.push(n)
  if (n.autoDismissMs > 0) {
    const timer = setTimeout(() => dismissNotification(id), n.autoDismissMs)
    dismissTimers.set(id, timer)
  }
  return id
}

export function dismissNotification(id: number): void {
  const idx = notificationsState.items.findIndex((n) => n.id === id)
  if (idx === -1) return
  notificationsState.items.splice(idx, 1)
  const timer = dismissTimers.get(id)
  if (timer !== undefined) {
    clearTimeout(timer)
    dismissTimers.delete(id)
  }
}

export function clearAllNotifications(): void {
  for (const t of dismissTimers.values()) clearTimeout(t)
  dismissTimers.clear()
  notificationsState.items.splice(0, notificationsState.items.length)
}

/**
 * Test-only: reset module-level state. Exported for vitest; not used in app.
 */
export function _resetForTests(): void {
  clearAllNotifications()
  nextId = 1
}

// Re-export the templates namespace so consumers can build template
// notifications without a second import (forward-compat helper).
export type { templates }
