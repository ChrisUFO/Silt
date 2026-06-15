// Template store (#55): holds the listing of available templates (built-in +
// user), subscribes to the backend ListTemplates IPC method, and re-lists on
// the backend's `templates:changed` event so an added/edited/deleted custom
// template appears immediately (mirrors the theme listing store). Svelte 5
// $state runes in a .svelte.ts module (matches theme/store.svelte.ts and
// settings/store.svelte.ts).
import { ListTemplates } from '../../wailsjs/go/main/App.js'
import { EventsOn } from '../../wailsjs/runtime/runtime.js'
import type { templates } from '../../wailsjs/go/models'

export interface TemplatesListingState {
  items: templates.TemplateSummary[]
  loadError: string | null
  loading: boolean
}

export const templatesState: TemplatesListingState = $state({
  items: [],
  loadError: null,
  loading: false
})

export type TemplateStatusKind = 'info' | 'success' | 'error'

export interface TemplateStatus {
  kind: TemplateStatusKind
  message: string
}

export const templateStatus: TemplateStatus = $state({
  kind: 'info',
  message: ''
})

let statusTimeout: ReturnType<typeof setTimeout> | null = null

export function setTemplateStatus(s: TemplateStatus): void {
  templateStatus.kind = s.kind
  templateStatus.message = s.message
  if (statusTimeout !== null) {
    clearTimeout(statusTimeout)
    statusTimeout = null
  }
  if (s.kind !== 'error' && s.message) {
    statusTimeout = setTimeout(() => {
      clearTemplateStatus()
      statusTimeout = null
    }, 5000)
  }
}

export function clearTemplateStatus(): void {
  if (statusTimeout !== null) {
    clearTimeout(statusTimeout)
    statusTimeout = null
  }
  templateStatus.kind = 'info'
  templateStatus.message = ''
}

let templatesStarted = false
let offTemplatesChanged: (() => void) | null = null

/**
 * Test-only: reset module-level state. Exported for vitest coverage; not used
 * in app code.
 */
export function _resetForTests(): void {
  templatesStarted = false
  offTemplatesChanged?.()
  offTemplatesChanged = null
  templatesState.items = []
  templatesState.loadError = null
  templatesState.loading = false
  clearTemplateStatus()
}

/**
 * Load the listing of available templates. Safe to call repeatedly; subsequent
 * calls overwrite the previous result (used as the `templates:changed` event
 * handler).
 */
export async function loadTemplates(): Promise<void> {
  templatesState.loading = true
  templatesState.loadError = null
  try {
    const res = await ListTemplates()
    templatesState.items = res?.templates ?? []
  } catch (err) {
    console.error('templates: ListTemplates failed:', err)
    templatesState.loadError = err instanceof Error ? err.message : String(err)
  } finally {
    templatesState.loading = false
  }
}

/**
 * Wire the template-listing store: one initial load plus a subscription to the
 * backend's `templates:changed` event so a custom template added/edited/deleted
 * externally appears immediately. Idempotent; safe to call once from
 * `App.svelte onMount`. Returns a disposer (mirrors initThemes).
 */
export function initTemplates(): () => void {
  if (templatesStarted) return () => {}
  templatesStarted = true
  void loadTemplates()
  // Debounce so a burst of changes coalesces into one ListTemplates call.
  let reloadTimer: ReturnType<typeof setTimeout> | null = null
  offTemplatesChanged = EventsOn('templates:changed', () => {
    if (reloadTimer !== null) clearTimeout(reloadTimer)
    reloadTimer = setTimeout(() => {
      reloadTimer = null
      void loadTemplates()
    }, 100)
  })
  return () => {
    if (reloadTimer !== null) {
      clearTimeout(reloadTimer)
      reloadTimer = null
    }
    offTemplatesChanged?.()
    offTemplatesChanged = null
    templatesStarted = false
  }
}
