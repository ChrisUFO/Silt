// Theme store (#46): holds the active theme/mode and the dark/light token
// maps, subscribes to the backend GetActiveTheme / ApplyTheme IPC methods,
// re-resolves "system" mode locally via prefers-color-scheme, and drives
// the runtime injector. Svelte 5 $state runes in a .svelte.ts module
// (matches plugins/store.svelte.ts).
import { ApplyTheme, GetActiveTheme } from '../../wailsjs/go/main/App.js'
import { EventsOn } from '../../wailsjs/runtime/runtime.js'
import { injectTokens } from './inject'

export type ThemeMode = 'dark' | 'light' | 'system'

export interface ThemeState {
  id: string
  name: string
  mode: ThemeMode
  darkTokens: Record<string, string>
  lightTokens: Record<string, string>
  /** True until the first IPC-driven injection completes. */
  loading: boolean
  /** Last error from a theme IPC call (surfaced so the UI can show it). */
  error: string | null
}

export const themeState: ThemeState = $state({
  id: '',
  name: '',
  mode: 'dark',
  darkTokens: {},
  lightTokens: {},
  loading: true,
  error: null
})

let darkMedia: MediaQueryList | null = null
let started = false

/** Returns true when the OS prefers light mode (used to resolve "system"). */
function osPrefersLight(): boolean {
  if (typeof window === 'undefined' || !window.matchMedia) return false
  return window.matchMedia('(prefers-color-scheme: light)').matches
}

/** Pick the concrete token map for the active mode, resolving "system". */
function effectiveTokens(s: ThemeState): Record<string, string> {
  if (s.mode === 'light') return s.lightTokens
  if (s.mode === 'dark') return s.darkTokens
  return osPrefersLight() ? s.lightTokens : s.darkTokens
}

/** Re-inject the effective tokens for the current state (same-tick). */
function repaint(): void {
  injectTokens(effectiveTokens(themeState))
}

/**
 * Initialize the theme engine on startup. Loads the active theme over IPC,
 * injects it before/with the first meaningful paint, and wires up the
 * "system" mode listener + theme:changed event. Safe to call once.
 */
export async function initTheme(): Promise<void> {
  if (started) return
  started = true

  // Watch prefers-color-scheme so "system" mode follows the OS live, with
  // no second IPC round-trip (both token maps are already in hand).
  if (typeof window !== 'undefined' && window.matchMedia) {
    darkMedia = window.matchMedia('(prefers-color-scheme: dark)')
    darkMedia.addEventListener('change', () => {
      if (themeState.mode === 'system') repaint()
    })
  }

  // Re-paint when the backend reports a theme change from elsewhere.
  EventsOn('theme:changed', async () => {
    try {
      const res = await GetActiveTheme()
      applyResult(res)
    } catch (err) {
      themeState.error = err instanceof Error ? err.message : String(err)
    }
  })

  try {
    const res = await GetActiveTheme()
    applyResult(res)
  } catch (err) {
    themeState.error = err instanceof Error ? err.message : String(err)
    // Even on error, drop the loading flag so the UI can render with the
    // index.css :root fallbacks rather than hang on a loader.
    themeState.loading = false
  }
}

/** Apply an IPC result to the store and inject the effective tokens. */
function applyResult(res: {
  id: string
  name: string
  mode: string
  dark_tokens: Record<string, string>
  light_tokens: Record<string, string>
}): void {
  themeState.id = res.id
  themeState.name = res.name
  themeState.mode = (res.mode as ThemeMode) || 'dark'
  themeState.darkTokens = res.dark_tokens || {}
  themeState.lightTokens = res.light_tokens || {}
  themeState.loading = false
  themeState.error = null
  repaint()
}

/**
 * Switch to a theme/mode, persist it via the backend, and inject the result.
 * Returns true on success.
 */
export async function applyTheme(
  id: string,
  mode: ThemeMode
): Promise<boolean> {
  try {
    const res = await ApplyTheme(id, mode)
    applyResult(res)
    return true
  } catch (err) {
    themeState.error = err instanceof Error ? err.message : String(err)
    return false
  }
}
