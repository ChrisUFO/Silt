import { Extension } from '@tiptap/core'
import { Plugin, PluginKey } from '@tiptap/pm/state'

const plainPasteKey = new PluginKey('plainPaste')

// Module-level so every plugin instance TipTap may (re)generate shares one
// flag. Only the focused editor receives keydown/paste, so the flag always
// reflects the editor about to paste — no cross-editor cross-talk.
let shiftHeld = false

/**
 * Paste-without-formatting: when the user pastes with Shift held (Ctrl+Shift+V
 * on Windows/Linux — the universal "paste plain" binding), insert the
 * clipboard's `text/plain` payload as unformatted text and suppress
 * ProseMirror's default rich-HTML parse. Mirrors the toolbar Paste button's
 * plain-text insertion so the two stay consistent.
 *
 * Modifier state is tracked from the keydown that precedes the paste event —
 * the ClipboardEvent itself carries no shift/ctrl flags. A paste follows its
 * triggering keydown with no intervening input, so the captured flag is fresh;
 * handlePaste resets it after reading so a later context-menu paste (no
 * preceding keydown) is never falsely treated as plain.
 */
export const PlainPaste = Extension.create({
  name: 'plainPaste',
  addProseMirrorPlugins() {
    return [
      new Plugin({
        key: plainPasteKey,
        props: {
          handleDOMEvents: {
            keydown: (_view, event) => {
              shiftHeld = (event as KeyboardEvent).shiftKey
              return false
            }
          },
          handlePaste: (view, event) => {
            const wasShift = shiftHeld
            shiftHeld = false
            if (!wasShift) return false
            const text = event.clipboardData?.getData('text/plain') ?? ''
            if (!text) return false
            this.editor.commands.insertContent({ type: 'text', text })
            return true
          }
        }
      })
    ]
  }
})

// Test-only accessors. The shift flag is module-private by design; these let
// tests (a) confirm the keydown handler wired up by setting it directly, and
// (b) assert the flag was reset after a handled paste.
export function _setShiftHeldForTests(v: boolean): void {
  shiftHeld = v
}
export function _getShiftHeldForTests(): boolean {
  return shiftHeld
}
