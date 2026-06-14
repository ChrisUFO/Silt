// Hotkey parsing/matching for config-driven key bindings. Parses the
// "Ctrl+Shift+X" notation stored in config.hotkeys into a modifier+key tuple
// and matches it against a KeyboardEvent. Supports single-character keys and
// digit keys with modifier combos (the global shortcuts Silt binds today).

export interface ParsedHotkey {
  ctrl: boolean
  shift: boolean
  alt: boolean
  meta: boolean
  key: string // lower-cased logical key, e.g. "p", "b", "/"
}

// Config bindings may use KeyboardEvent.code-style names ("Slash", "Period")
// whose corresponding KeyboardEvent.key is a different character ("/", ".").
// Without normalization, "Ctrl+Slash" parses to key "slash" and never matches
// e.key "/" (the shipped open_command_palette default would silently fail).
// Map the common named tokens to their KeyboardEvent.key value (lower-cased).
const KEY_ALIASES: Record<string, string> = {
  slash: '/',
  backslash: '\\',
  period: '.',
  comma: ',',
  semicolon: ';',
  quote: "'",
  backquote: '`',
  minus: '-',
  equal: '=',
  bracketleft: '[',
  bracketright: ']',
  space: ' ',
  escape: 'escape',
  esc: 'escape',
  enter: 'enter',
  return: 'enter',
  tab: 'tab',
  delete: 'delete',
  del: 'delete',
  backspace: 'backspace',
  insert: 'insert',
  home: 'home',
  end: 'end',
  pageup: 'pageup',
  pagedown: 'pagedown',
  arrowup: 'arrowup',
  arrowdown: 'arrowdown',
  arrowleft: 'arrowleft',
  arrowright: 'arrowright'
}

/** Parse a "Ctrl+Shift+P"-style binding. Returns null for empty/invalid input. */
export function parseHotkey(s: string | undefined | null): ParsedHotkey | null {
  if (!s) return null
  const parts = s.toLowerCase().split('+').map((p) => p.trim()).filter(Boolean)
  if (parts.length === 0) return null

  let ctrl = false
  let shift = false
  let alt = false
  let meta = false
  let key = ''

  for (const p of parts) {
    switch (p) {
      case 'ctrl':
      case 'control':
        ctrl = true
        break
      case 'shift':
        shift = true
        break
      case 'alt':
      case 'option':
        alt = true
        break
      case 'meta':
      case 'cmd':
      case 'command':
      case 'win':
        meta = true
        break
      default:
        key = p
    }
  }
  if (!key) return null
  // Normalize named keys (slash → /) so matching works against KeyboardEvent.key.
  key = KEY_ALIASES[key] ?? key
  return { ctrl, shift, alt, meta, key }
}

/** True if a KeyboardEvent matches the given binding string. */
export function matchHotkey(e: KeyboardEvent, binding: string | undefined | null): boolean {
  const h = parseHotkey(binding)
  if (!h) return false
  return (
    e.ctrlKey === h.ctrl &&
    e.shiftKey === h.shift &&
    e.altKey === h.alt &&
    e.metaKey === h.meta &&
    e.key.toLowerCase() === h.key
  )
}
