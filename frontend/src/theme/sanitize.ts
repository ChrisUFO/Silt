// Shared CSS sanitization for font-family values that end up rendered in a CSS
// context (the editor-token `:root{--editor-font-family:value;}` injection and
// the FontSelect combobox's inline `style="font-family:value"` preview).
//
// Sandbox-by-validation is the established Silt principle: the Go side
// (themes.isValidFontFamily) rejects theme font strings containing these chars
// outright. This is the display-side mirror for config/picker values: strip the
// characters that could break out of the CSS declaration context (`;` starts a
// new declaration; `{`/`}` open/close a block). Real font-family names never
// contain these, so the strip is lossless for legitimate values. Idempotent.
//
// Kept as one source of truth (previously a private copy in editor-tokens).
export function sanitizeFontFamilyCSS(v: string): string {
  return v.replace(/[;{}]/g, '')
}
