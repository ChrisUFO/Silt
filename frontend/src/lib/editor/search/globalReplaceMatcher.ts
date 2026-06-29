export interface MatcherOptions {
  findText: string
  caseSensitive: boolean
  wholeWord: boolean
  regexp: boolean
}

/**
 * Build a global RegExp matcher from find text + toggles. Returns null for
 * invalid regex. Escapes regex metacharacters in non-regex mode; wraps in
 * word boundaries when wholeWord is set.
 */
export function buildMatcher(opts: MatcherOptions): RegExp | null {
  const { findText, caseSensitive, wholeWord, regexp } = opts
  const flags = caseSensitive ? 'g' : 'gi'
  try {
    if (regexp) return new RegExp(findText, flags)
    const esc = findText.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
    return new RegExp(wholeWord ? `\\b${esc}\\b` : esc, flags)
  } catch {
    return null
  }
}

/**
 * Apply a matcher to text, replacing all matches with the replacement string.
 */
export function applyReplace(
  text: string,
  matcher: RegExp,
  replacement: string
): string {
  return text.replace(matcher, replacement)
}
