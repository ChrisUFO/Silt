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
// Without normalization, a binding like "Ctrl+Slash" would parse to key
// "slash" and never match e.key "/". Map the common named tokens to their
// KeyboardEvent.key value (lower-cased) so either spelling works.
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
  const parts = s
    .toLowerCase()
    .split('+')
    .map((p) => p.trim())
    .filter(Boolean)
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
export function matchHotkey(
  e: KeyboardEvent,
  binding: string | undefined | null
): boolean {
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

// ---- Config → ProseMirror keymap converter (#311) -------------------------
// TipTap's addKeyboardShortcuts returns a static { 'Mod-Shift-9': handler }
// map registered at editor-creation time. The config entries in config.yaml
// use "Ctrl+Shift+9" notation. This converter bridges the two formats so the
// editor honors user-remapped hotkeys at creation time.
//
// Per prosemirror-keymap source (verified from node_modules):
// - Separator is '-', NOT '+'.
// - 'Mod' = Cmd on Mac, Ctrl everywhere else.
// - Modifier order is normalized (input order doesn't matter).
// - Special keys use KeyboardEvent.key names: ArrowUp, ArrowDown, etc.
// - Letters must be lowercase (uppercase implies Shift).
// - Punctuation keys (., /, ,) are single-character key names.

// Map arrow/direction names from config notation to KeyboardEvent.key names.
const PM_KEY_NORMALIZE: Record<string, string> = {
  up: 'ArrowUp',
  down: 'ArrowDown',
  left: 'ArrowLeft',
  right: 'ArrowRight',
  space: ' ',
  esc: 'Escape',
  del: 'Delete'
}

/**
 * Convert a config-style binding ("Ctrl+Shift+9") to a ProseMirror keymap
 * binding string ("Mod-Shift-9"). Returns '' for empty/invalid input.
 */
export function configKeyToProseMirrorKey(
  binding: string | undefined | null
): string {
  if (!binding) return ''
  const parsed = parseHotkey(binding)
  if (!parsed || !parsed.key) return ''

  const mods: string[] = []
  // Mod first (matches the codebase convention: Mod-Alt-1, Mod-Shift-9).
  // PM normalizes modifier order on its end anyway, so this is cosmetic.
  if (parsed.ctrl || parsed.meta) mods.push('Mod')
  if (parsed.alt) mods.push('Alt')
  if (parsed.shift) mods.push('Shift')

  const key = PM_KEY_NORMALIZE[parsed.key] ?? parsed.key
  return [...mods, key].join('-')
}

/**
 * Resolve a keyboard shortcut from config, falling back to a default
 * ProseMirror key string. Reads hotkeys[configKey], converts via
 * configKeyToProseMirrorKey, and returns the result. Falls back to
 * defaultPmKey if the config entry is absent, empty, or unparseable.
 */
export function resolveShortcut(
  configKey: string,
  defaultPmKey: string,
  hotkeys: Record<string, string>
): string {
  const configBinding = hotkeys[configKey]
  const converted = configKeyToProseMirrorKey(configBinding)
  return converted || defaultPmKey
}

/**
 * Resolve a hotkey's DISPLAY binding (e.g. "Ctrl+Shift+9") for tooltips,
 * slash-menu hints, and aria-keyshortcuts. Returns the configured binding
 * straight from the hotkeys map — no ProseMirror conversion — so the display
 * always matches what the user sees in config.yaml. Returns '' when the
 * action is absent or explicitly disabled (set to ""), so callers can omit
 * the hint / aria attribute entirely for unbound actions. Unlike
 * {@link resolveShortcut} (which returns a ProseMirror keystring for
 * keymaps), this returns the human-readable config binding.
 */
export function resolveHotkeyDisplay(
  action: string,
  hotkeys: Record<string, string>
): string {
  const binding = hotkeys[action]
  return binding ?? ''
}
