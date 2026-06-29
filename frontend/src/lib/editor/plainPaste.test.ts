import { describe, it, expect } from 'vitest'
import { Editor } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import {
  PlainPaste,
  _setShiftHeldForTests,
  _getShiftHeldForTests
} from './plainPaste'

// PlainPaste reads the clipboard's text/plain in handlePaste when shift was
// held (Ctrl+Shift+V) and inserts it as plain text, suppressing ProseMirror's
// default rich-HTML parse. The shift flag is set by the keydown handler and
// reset by handlePaste; jsdom's ClipboardEvent doesn't populate clipboardData
// reliably, so these tests invoke the merged handlePaste prop directly with a
// stub DataTransfer and set the shift precondition via the test accessor.

function mountEditor() {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const editor = new Editor({
    element: container,
    extensions: [StarterKit, PlainPaste],
    content: '<p></p>'
  })
  return {
    editor,
    cleanup: () => {
      editor.destroy()
      container.remove()
    }
  }
}

/** A minimal ClipboardEvent stand-in with a DataTransfer.getData('text/plain'). */
function pasteEvent(plainText: string): any {
  return {
    clipboardData: {
      getData: (type: string) => (type === 'text/plain' ? plainText : '')
    }
  }
}

/** Invoke PlainPaste's specific handlePaste (via its plugin spec, NOT someProp
 *  — the Link extension's handlePasteLink comes earlier in the chain, so
 *  someProp would return that one). In the real app ProseMirror walks the
 *  handlePaste chain in order; Link defers (returns false), then PlainPaste
 *  runs and wins when shift is held. */
function callHandlePaste(editor: Editor, event: any): boolean {
  // The Link extension's handlePasteLink comes earlier in the chain, so
  // someProp would return that one. Find PlainPaste's plugin by key (the
  // Plugin type hides runtime `.key`, hence the cast) and read its spec's
  // handlePaste directly. In the real app ProseMirror walks the handlePaste
  // chain in order; Link defers (returns false), then PlainPaste runs and
  // wins when shift is held.
  const plugin = (editor.view.state.plugins as any[]).find(
    (p) => typeof p.key === 'string' && p.key.startsWith('plainPaste')
  )
  const handler = plugin?.spec?.props?.handlePaste as
    | ((v: any, e: any, s: any) => boolean | void)
    | undefined
  if (!handler) return false
  return handler(editor.view, event, editor.state.selection.content()) === true
}

describe('PlainPaste', () => {
  it('inserts text/plain and suppresses default when shift held', () => {
    const { editor, cleanup } = mountEditor()
    try {
      _setShiftHeldForTests(true)
      const handled = callHandlePaste(editor, pasteEvent('hello plain'))
      expect(handled).toBe(true)
      expect(editor.getText()).toBe('hello plain')
    } finally {
      cleanup()
    }
  })

  it('falls through (returns false) when shift NOT held — rich paste proceeds', () => {
    const { editor, cleanup } = mountEditor()
    try {
      _setShiftHeldForTests(false)
      const handled = callHandlePaste(editor, pasteEvent('ignored'))
      expect(handled).toBe(false)
      expect(editor.getText()).toBe('')
    } finally {
      cleanup()
    }
  })

  it('returns false for an empty text/plain payload (nothing to insert)', () => {
    const { editor, cleanup } = mountEditor()
    try {
      _setShiftHeldForTests(true)
      const handled = callHandlePaste(editor, pasteEvent(''))
      expect(handled).toBe(false)
    } finally {
      cleanup()
    }
  })

  it('resets the shift flag after a handled paste so a following paste is not plain', () => {
    const { editor, cleanup } = mountEditor()
    try {
      _setShiftHeldForTests(true)
      expect(callHandlePaste(editor, pasteEvent('first'))).toBe(true)
      // Flag reset by handlePaste — a second paste with no intervening keydown
      // (e.g. a context-menu paste) must NOT be treated as plain.
      expect(_getShiftHeldForTests()).toBe(false)
      expect(callHandlePaste(editor, pasteEvent('second'))).toBe(false)
      expect(editor.getText()).toBe('first')
    } finally {
      cleanup()
    }
  })

  it('keydown handler sets the shift flag from the event modifier', () => {
    // Confirms the keydown→flag glue is wired (the diagnostic the earlier
    // dispatch-based tests couldn't isolate). Real user input flows through
    // this same handler; a context-menu paste has no preceding keydown.
    const { editor, cleanup } = mountEditor()
    try {
      _setShiftHeldForTests(false)
      editor.view.dom.dispatchEvent(
        new KeyboardEvent('keydown', { shiftKey: true, bubbles: true })
      )
      // Whether or not the DOM-event handler fires in jsdom (ProseMirror's
      // event pipeline is part-internal), the feature's correctness rests on
      // handlePaste honoring the flag — covered above. This assertion pins the
      // intended wiring; if jsdom doesn't fire handleDOMEvents, this is the
      // canary that surfaces it rather than silently passing.
      expect(_getShiftHeldForTests()).toBe(true)
    } finally {
      cleanup()
    }
  })
})
