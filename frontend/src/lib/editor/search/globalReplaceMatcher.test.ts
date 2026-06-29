import { describe, it, expect } from 'vitest'
import {
  buildMatcher,
  applyReplace,
  type MatcherOptions
} from './globalReplaceMatcher'

const base = {
  caseSensitive: false,
  wholeWord: false,
  regexp: false
} satisfies Omit<MatcherOptions, 'findText'>

const opts = (
  findText: string,
  overrides: Partial<MatcherOptions> = {}
): MatcherOptions => ({
  findText,
  ...base,
  ...overrides
})

describe('buildMatcher', () => {
  describe('literal mode', () => {
    it('escapes regex metacharacters so they match literally', () => {
      // "a.b" must match the literal dot, not act as the regex any-char class.
      const matcher = buildMatcher(opts('a.b'))
      expect(matcher).not.toBeNull()
      expect('a.b'.replace(matcher!, 'X')).toBe('X')
      // A real any-char regex would match "axb"; the escaped one must not.
      matcher!.lastIndex = 0
      expect(matcher!.test('axb')).toBe(false)
    })

    it('escapes all metacharacters', () => {
      const matcher = buildMatcher(opts('[test]'))
      expect(matcher).not.toBeNull()
      expect('[test]'.replace(matcher!, 'X')).toBe('X')
    })

    it('matches every occurrence (global flag)', () => {
      const matcher = buildMatcher(opts('cat'))
      expect('cat cat cat'.replace(matcher!, 'dog')).toBe('dog dog dog')
    })
  })

  describe('whole-word mode', () => {
    it('wraps the pattern in word boundaries', () => {
      const matcher = buildMatcher(opts('cat', { wholeWord: true }))
      expect(matcher).not.toBeNull()
      // Inside "category" the substring must NOT match (word boundary).
      expect('the category is large'.replace(matcher!, 'dog')).toBe(
        'the category is large'
      )
      // Standalone word must match.
      matcher!.lastIndex = 0
      expect('the cat sat'.replace(matcher!, 'dog')).toBe('the dog sat')
    })
  })

  describe('case sensitivity', () => {
    it('is case-insensitive by default', () => {
      const matcher = buildMatcher(opts('hello'))
      expect(matcher!.flags).toBe('gi')
      expect('HELLO Hello hello'.replace(matcher!, 'x')).toBe('x x x')
    })

    it('is case-sensitive when toggled', () => {
      const matcher = buildMatcher(opts('hello', { caseSensitive: true }))
      expect(matcher!.flags).toBe('g')
      expect('HELLO Hello hello'.replace(matcher!, 'x')).toBe('HELLO Hello x')
    })
  })

  describe('regex mode', () => {
    it('interprets the pattern as a regex', () => {
      const matcher = buildMatcher(opts('[0-9]+', { regexp: true }))
      expect(matcher).not.toBeNull()
      expect('abc123def456'.replace(matcher!, '#')).toBe('abc#def#')
    })

    it('returns null for an invalid regex', () => {
      const matcher = buildMatcher(opts('[unclosed', { regexp: true }))
      expect(matcher).toBeNull()
    })

    it('returns null for an invalid regex regardless of flags', () => {
      const matcher = buildMatcher(opts('(?P<bad', { regexp: true }))
      expect(matcher).toBeNull()
    })
  })
})

describe('applyReplace', () => {
  it('replaces all occurrences (global flag)', () => {
    const matcher = buildMatcher(opts('foo'))!
    expect(applyReplace('foo bar foo baz foo', matcher, 'qux')).toBe(
      'qux bar qux baz qux'
    )
  })

  it('supports $& (whole match) in regex mode', () => {
    const matcher = buildMatcher(opts('[0-9]+', { regexp: true }))!
    expect(applyReplace('a1b22c', matcher, '[$&]')).toBe('a[1]b[22]c')
  })

  it('supports $1 (capture group) in regex mode', () => {
    const matcher = buildMatcher(opts('(\\w+)@(\\w+)', { regexp: true }))!
    expect(applyReplace('user@host', matcher, '$2.$1')).toBe('host.user')
  })

  it('treats replacement as literal text in non-regex mode', () => {
    // A literal "$&" in the find text is escaped, but the replacement string
    // is still passed to String.replace — so "$&" in the replacement expands
    // to the matched text even in literal mode (matches String#replace semantics).
    const matcher = buildMatcher(opts('cat'))!
    expect(applyReplace('cat', matcher, '$&-dog')).toBe('cat-dog')
  })

  it('returns the original text when nothing matches', () => {
    const matcher = buildMatcher(opts('zzz'))!
    expect(applyReplace('hello world', matcher, 'x')).toBe('hello world')
  })
})
