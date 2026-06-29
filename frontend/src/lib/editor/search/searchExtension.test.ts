import { describe, it, expect } from 'vitest'
import { Editor } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import Link from '@tiptap/extension-link'
import {
  Search,
  getMatchCount,
  getActiveMatchIndex,
  clearSearch
} from './searchExtension'

function mount(content: string) {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const editor = new Editor({
    element: container,
    extensions: [StarterKit, Link, Search],
    content
  })
  return {
    editor,
    cleanup: () => {
      editor.destroy()
      container.remove()
    }
  }
}

describe('Search extension (in-page find, #186)', () => {
  it('highlights and counts matches', () => {
    const { editor, cleanup } = mount('<p>hello hello hello</p>')
    try {
      editor.commands.setSearchQuery({ search: 'hello' })
      expect(getMatchCount(editor)).toBe(3)
    } finally {
      cleanup()
    }
  })

  it('is case-insensitive by default and case-sensitive when toggled', () => {
    const { editor, cleanup } = mount('<p>Hello hello HELLO</p>')
    try {
      editor.commands.setSearchQuery({ search: 'hello' })
      expect(getMatchCount(editor)).toBe(3) // case-insensitive: all three
      editor.commands.setSearchQuery({ search: 'hello', caseSensitive: true })
      expect(getMatchCount(editor)).toBe(1) // only lowercase
    } finally {
      cleanup()
    }
  })

  it('whole-word matching', () => {
    const { editor, cleanup } = mount('<p>hello helloworld sayhello</p>')
    try {
      editor.commands.setSearchQuery({ search: 'hello' })
      expect(getMatchCount(editor)).toBe(3) // matches inside words too
      editor.commands.setSearchQuery({ search: 'hello', wholeWord: true })
      expect(getMatchCount(editor)).toBe(1) // only standalone "hello"
    } finally {
      cleanup()
    }
  })

  it('scope filter skips fenced code blocks', () => {
    const { editor, cleanup } = mount(
      '<p>hello</p><pre><code>hello hello</code></pre>'
    )
    try {
      editor.commands.setSearchQuery({ search: 'hello' })
      // Only the prose match counts; the two inside the code block are skipped.
      expect(getMatchCount(editor)).toBe(1)
    } finally {
      cleanup()
    }
  })

  it('scope filter skips inline code', () => {
    const { editor, cleanup } = mount('<p>hello <code>hello</code> hello</p>')
    try {
      editor.commands.setSearchQuery({ search: 'hello' })
      // Two prose matches; the one inside inline code is skipped.
      expect(getMatchCount(editor)).toBe(2)
    } finally {
      cleanup()
    }
  })

  it('scope filter skips links (URLs)', () => {
    const { editor, cleanup } = mount(
      '<p><a href="http://hello.example">hello</a> hello</p>'
    )
    try {
      editor.commands.setSearchQuery({ search: 'hello' })
      // Only the standalone prose match; the link text is skipped.
      expect(getMatchCount(editor)).toBe(1)
    } finally {
      cleanup()
    }
  })

  it('findNext moves the selection onto a match', () => {
    const { editor, cleanup } = mount('<p>alpha beta gamma</p>')
    try {
      editor.commands.setSearchQuery({ search: 'beta' })
      editor.commands.findNextInPage()
      // Selection should now span "beta" (4..8 in "alpha beta gamma").
      expect(getActiveMatchIndex(editor)).toBe(0)
      const { from, to } = editor.state.selection
      const word = editor.state.doc.textBetween(from, to, ' ')
      expect(word).toBe('beta')
    } finally {
      cleanup()
    }
  })

  it('findNext/findPrev navigate all matches and wrap', () => {
    const { editor, cleanup } = mount('<p>one two one two one</p>')
    try {
      editor.commands.setSearchQuery({ search: 'one' })
      expect(getMatchCount(editor)).toBe(3)
      editor.commands.findNextInPage()
      expect(getActiveMatchIndex(editor)).toBe(0)
      editor.commands.findNextInPage()
      expect(getActiveMatchIndex(editor)).toBe(1)
      editor.commands.findNextInPage()
      expect(getActiveMatchIndex(editor)).toBe(2)
      // Wraps back to the first.
      editor.commands.findNextInPage()
      expect(getActiveMatchIndex(editor)).toBe(0)
      // Prev reverses.
      editor.commands.findPrevInPage()
      expect(getActiveMatchIndex(editor)).toBe(2)
    } finally {
      cleanup()
    }
  })

  it('regex search', () => {
    const { editor, cleanup } = mount('<p>cat cot cut cet</p>')
    try {
      editor.commands.setSearchQuery({ search: 'c[au]t', regexp: true })
      expect(getMatchCount(editor)).toBe(2) // cat + cut
    } finally {
      cleanup()
    }
  })

  it('empty query highlights nothing', () => {
    const { editor, cleanup } = mount('<p>hello world</p>')
    try {
      editor.commands.setSearchQuery({ search: '' })
      expect(getMatchCount(editor)).toBe(0)
    } finally {
      cleanup()
    }
  })

  it('clearSearch removes all decorations', () => {
    const { editor, cleanup } = mount('<p>hello hello</p>')
    try {
      editor.commands.setSearchQuery({ search: 'hello' })
      expect(getMatchCount(editor)).toBe(2)
      clearSearch(editor)
      expect(getMatchCount(editor)).toBe(0)
    } finally {
      cleanup()
    }
  })
})
