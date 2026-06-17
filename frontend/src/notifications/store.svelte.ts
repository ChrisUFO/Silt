// Notification store (#86): a global, app-wide channel for non-blocking
// status messages (toasts). Mirrors the pattern of theme/store.svelte.ts
// and settings/store.svelte.ts (Svelte 5 $state runes in a .svelte.ts
// module). Consumers (TipTapEditor, TemplatePicker, etc.) call
// pushNotification; ToastContainer.svelte renders the live stack.

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

const MAX_NOTIFICATIONS = 5

export function pushNotification(opts: PushOptions): number {
  const kind: NotificationKind = opts.kind ?? 'info'
  const message = opts.message

  // Dedup: if an active notification with the same kind + message exists,
  // don't stack a duplicate. This prevents repeated save-failure toasts from
  // piling up during continuous typing against a persistent error (#86).
  const existing = notificationsState.items.find(
    (n) => n.kind === kind && n.message === message
  )
  if (existing) {
    return existing.id
  }

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
  while (notificationsState.items.length > MAX_NOTIFICATIONS) {
    const oldest = notificationsState.items[0]
    dismissNotification(oldest.id)
  }
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
