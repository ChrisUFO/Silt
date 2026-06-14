// Runtime theme injector (#46).
//
// Receives a flat token map (CSS custom-property name -> value, e.g.
// { "--bg-void": "#0c0c0e", "--accent-primary-start": "#2dd4bf", ... }) and
// applies it to :root by rewriting a single generated <style> element.
//
// A single textContent rewrite is ONE DOM write -> ONE style recalculation,
// so the whole shell repaints in the same paint frame: no flicker, no reload,
// no component remount. The index.css :root values are retained as startup
// fallbacks only; the first call here overrides them once IPC returns.

const STYLE_ID = 'silt-theme'

/**
 * Inject a token map onto :root as CSS custom properties, in a single DOM
 * write so the repaint is same-tick.
 */
export function injectTokens(tokens: Record<string, string>): void {
  let el = document.getElementById(STYLE_ID) as HTMLStyleElement | null
  if (!el) {
    el = document.createElement('style')
    el.id = STYLE_ID
    document.head.appendChild(el)
  }

  // Build ":root{--name:value;…}". Values are taken verbatim from the
  // validated theme JSON (hex/rgb/rgba), so no escaping beyond the
  // property declaration is required.
  let css = ':root{'
  for (const name in tokens) {
    const value = tokens[name]
    if (value === undefined || value === null || value === '') continue
    css += `${name}:${value};`
  }
  css += '}'

  // textContent replacement is a single atomic DOM write.
  el.textContent = css
}

/**
 * Read the currently-injected value of a token from the live computed style.
 * Used by the theme-swap verification (a representative computed value,
 * e.g. --bg-void, should change without remounting the app).
 */
export function readToken(name: string): string {
  return getComputedStyle(document.documentElement)
    .getPropertyValue(name)
    .trim()
}
