// Shared reactive OS dark-mode preference. Both TipTapEditor and
// VirtualScrollContainer independently tracked prefers-color-scheme and
// recomputed the same isDark value. This module centralizes the single
// matchMedia listener and exposes isSystemDark() for $derived consumption.
import { themeState } from '../theme/store.svelte'

let osPrefersDark = $state(
  window.matchMedia?.('(prefers-color-scheme: dark)').matches ?? false
)

if (typeof window !== 'undefined' && window.matchMedia) {
  const mql = window.matchMedia('(prefers-color-scheme: dark)')
  mql.addEventListener('change', (e: MediaQueryListEvent) => {
    osPrefersDark = e.matches
  })
}

export function isSystemDark(): boolean {
  return (
    themeState.mode === 'dark' ||
    (themeState.mode === 'system' && osPrefersDark)
  )
}
