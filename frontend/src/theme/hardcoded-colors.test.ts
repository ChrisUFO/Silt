// Regression guard against hardcoded dark colors that break light mode (#260).
// The theme engine swaps CSS custom properties on :root; any hardcoded
// rgba/hex that doesn't reference a --color-* token is invisible to theme
// switching and produces a mixed dark/light appearance. This test scans the
// source files for known-offensive patterns so they never creep back.

import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

const frontendSrc = resolve(__dirname, '..')

const FILES_TO_CHECK = [
  'index.css',
  'components/Sidebar.svelte',
  'components/TipTapEditor.svelte',
  'components/settings/SettingsShell.svelte',
  'components/settings/VaultActionModal.svelte',
  'components/settings/VaultArchiveModal.svelte'
]

// Hardcoded dark colors that should never appear outside of CSS custom
// property fallback values (var(--token, <fallback>) is acceptable; a
// bare rgba(22, 22, 25, ...) or #131a18 is not).
const FORBIDDEN_PATTERNS: RegExp[] = [
  /rgba?\(\s*22\s*,\s*22\s*,\s*25/,
  /#131a18/i
]

// A line is exempt if the forbidden color appears as a CSS var() fallback
// (e.g. `var(--color-surface, #131a18)`) — these are safe because the token
// always wins when the theme engine injects it.
const FALLBACK_PATTERN = /var\(--[a-z-]+,\s*$/

function readLines(relPath: string): string[] {
  const abs = resolve(frontendSrc, relPath)
  return readFileSync(abs, 'utf-8').split('\n')
}

describe('hardcoded dark color guard (#260)', () => {
  for (const file of FILES_TO_CHECK) {
    it(`${file} has no hardcoded dark rgba/hex colors`, () => {
      const lines = readLines(file)
      const violations: string[] = []

      for (let i = 0; i < lines.length; i++) {
        const line = lines[i]
        for (const pattern of FORBIDDEN_PATTERNS) {
          if (!pattern.test(line)) continue
          // Skip if this is a CSS var() fallback value.
          const beforeMatch = line.slice(0, line.search(pattern))
          if (FALLBACK_PATTERN.test(beforeMatch)) continue
          violations.push(`  line ${i + 1}: ${line.trim()}`)
        }
      }

      expect(
        violations,
        `Hardcoded dark colors found:\n${violations.join('\n')}`
      ).toEqual([])
    })
  }
})
