// Editor typography token injector (#72).
//
// Mirrors the theme engine's injectTokens pattern (theme/inject.ts): writes
// editor.* config values (font_family, mono_font_family, font_size_px,
// line_height) as CSS custom properties on :root via a single generated
// <style> element. One DOM write -> one style recalculation -> same-tick
// repaint, so config changes apply live without a reload or remount.
//
// A dedicated <style id="silt-editor"> element carries these properties,
// separate from the theme injector's <style id="silt-theme"> (colors). The
// index.css :root values are startup fallbacks only; the first call here
// overrides them once the config IPC returns.

import { settings } from './store.svelte'
import type { config } from '../../wailsjs/go/models.js'
import { sanitizeFontFamilyCSS } from '../theme/sanitize'

type EditorConfig = config.EditorConfig

const STYLE_ID = 'silt-editor'

/**
 * Strip characters that could break out of the CSS declaration context when a
 * font-family value is interpolated into `:root{--editor-font-family:value;}`.
 * Delegates to the shared theme/sanitize util (single source of truth — the
 * same defense is applied in the FontSelect combobox preview). Idempotent.
 */
function sanitizeCSSValue(v: string): string {
  return sanitizeFontFamilyCSS(v)
}

/**
 * Inject editor typography config as CSS custom properties on :root, in a
 * single DOM write so the repaint is same-tick. Guards against empty/zero
 * values so the index.css fallbacks remain in effect until a valid config
 * arrives. Font-family values are sanitized to prevent CSS injection.
 */
export function injectEditorTokens(editor: EditorConfig | null | undefined): void {
  if (!editor) return
  let el = document.getElementById(STYLE_ID) as HTMLStyleElement | null
  if (!el) {
    el = document.createElement('style')
    el.id = STYLE_ID
    document.head.appendChild(el)
  }

  const tokens: Record<string, string> = {}
  if (editor.font_family) tokens['--editor-font-family'] = sanitizeCSSValue(editor.font_family)
  if (editor.mono_font_family) tokens['--editor-mono-font-family'] = sanitizeCSSValue(editor.mono_font_family)
  if (editor.font_size_px > 0) tokens['--editor-font-size'] = `${editor.font_size_px}px`
  if (editor.line_height > 0) tokens['--editor-line-height'] = String(editor.line_height)

  let css = ':root{'
  for (const [name, value] of Object.entries(tokens)) {
    css += `${name}:${value};`
  }
  css += '}'

  el.textContent = css
}

let initialized = false

/**
 * Initialize the editor-token injection pipeline. Uses $effect.root to watch
 * the reactive settings store and re-inject whenever the config changes
 * (initial load + config:changed hot-reload). Returns a disposer that the
 * caller must invoke on unmount to prevent duplicate root effects during
 * dev hot-reload. Idempotent: subsequent calls return a no-op disposer,
 * matching the initConfigHotReload guard pattern (store.svelte.ts).
 * Should be called once from App.svelte onMount.
 */
export function initEditorTokens(): () => void {
  if (initialized) return () => {}
  initialized = true
  return $effect.root(() => {
    $effect(() => {
      injectEditorTokens(settings.config?.editor)
    })
  })
}
