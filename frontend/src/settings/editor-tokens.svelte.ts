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

type EditorConfig = config.EditorConfig

const STYLE_ID = 'silt-editor'

/**
 * Inject editor typography config as CSS custom properties on :root, in a
 * single DOM write so the repaint is same-tick. Guards against empty/zero
 * values so the index.css fallbacks remain in effect until a valid config
 * arrives.
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
  if (editor.font_family) tokens['--editor-font-family'] = editor.font_family
  if (editor.mono_font_family) tokens['--editor-mono-font-family'] = editor.mono_font_family
  if (editor.font_size_px > 0) tokens['--editor-font-size'] = `${editor.font_size_px}px`
  if (editor.line_height > 0) tokens['--editor-line-height'] = String(editor.line_height)

  let css = ':root{'
  for (const [name, value] of Object.entries(tokens)) {
    css += `${name}:${value};`
  }
  css += '}'

  el.textContent = css
}

/**
 * Initialize the editor-token injection pipeline. Uses $effect.root to watch
 * the reactive settings store and re-inject whenever the config changes
 * (initial load + config:changed hot-reload). Should be called once from
 * App.svelte onMount.
 */
export function initEditorTokens(): void {
  $effect.root(() => {
    $effect(() => {
      injectEditorTokens(settings.config?.editor)
    })
  })
}
