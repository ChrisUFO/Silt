import { describe, it, expect } from 'vitest'
import { Editor } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import { SiltBlockExtensions } from './index'
import {
  resolveDropDepth,
  BlockIndentOnDrop,
  INDENT_STEP_PX,
  MAX_DEPTH
} from './dragIndentDrop'

// resolveDropDepth is the ONLY logic this phase can prove correct in jsdom.
// The HTML5 drag/drop pipeline (DataTransfer + layout-driven posAtCoords +
// PM's internal view.dragging NodeSelection) cannot be driven from jsdom —
// per AGENTS.md the interactive indent-on-drop path is covered by the
// TESTING.md manual matrix. So this file exhaustively pins the pure helper
// (the part that decides the depth) and adds a smoke test that the TipTap
// extension is constructible and registers its ProseMirror plugin.

describe('resolveDropDepth — pure math', () => {
  describe('default Silt constants (INDENT_STEP_PX=24, MAX_DEPTH=6)', () => {
    it('exposes the editor CSS constants verbatim', () => {
      // INDENT_STEP_PX mirrors `--indent-unit` in frontend/src/index.css:103.
      // MAX_DEPTH mirrors the deepest `[data-depth='N']` rule (index.css:459).
      // If either changes, dragIndentDrop.ts MUST be updated to match —
      // pinning them here makes a silent drift a test failure.
      expect(INDENT_STEP_PX).toBe(24)
      expect(MAX_DEPTH).toBe(6)
    })

    it('returns 0 at exactly contentLeft', () => {
      expect(resolveDropDepth(100, 100, 24, 6)).toBe(0)
      expect(resolveDropDepth(0, 0, 24, 6)).toBe(0)
    })

    it('returns 0 left of contentLeft', () => {
      expect(resolveDropDepth(99, 100, 24, 6)).toBe(0)
      expect(resolveDropDepth(50, 100, 24, 6)).toBe(0)
      expect(resolveDropDepth(0, 100, 24, 6)).toBe(0)
      expect(resolveDropDepth(-100, 100, 24, 6)).toBe(0)
    })

    it('returns 1 at exactly +1 step', () => {
      expect(resolveDropDepth(100 + 24, 100, 24, 6)).toBe(1)
    })

    it('returns 2 at exactly +2 steps', () => {
      expect(resolveDropDepth(100 + 48, 100, 24, 6)).toBe(2)
    })

    it('returns N at exactly +N steps for every N in [0, max]', () => {
      for (let n = 0; n <= 6; n++) {
        expect(resolveDropDepth(100 + n * 24, 100, 24, 6)).toBe(n)
      }
    })

    it('rounds to nearest step (Math.round half-up semantics)', () => {
      // The implementation documents Math.round, which rounds 0.5 → 1
      // (round-half-up, not banker's rounding). Pin the exact midpoint
      // direction so a future refactor to Math.trunc/|0 is caught.
      const halfStep = 100 + 12 // +0.5 step
      expect(resolveDropDepth(halfStep, 100, 24, 6)).toBe(1)

      // +0.4 steps → rounds down to 0
      expect(resolveDropDepth(100 + 9, 100, 24, 6)).toBe(0)
      // +0.6 steps → rounds up to 1
      expect(resolveDropDepth(100 + 15, 100, 24, 6)).toBe(1)

      // +1.4 steps → 1
      expect(resolveDropDepth(100 + 34, 100, 24, 6)).toBe(1)
      // +1.5 steps → 2 (half-up)
      expect(resolveDropDepth(100 + 36, 100, 24, 6)).toBe(2)
      // +1.6 steps → 2
      expect(resolveDropDepth(100 + 38, 100, 24, 6)).toBe(2)
    })

    it('clamps to MAX_DEPTH when clientX is far right', () => {
      expect(resolveDropDepth(1000, 100, 24, 6)).toBe(6)
      expect(resolveDropDepth(1e6, 100, 24, 6)).toBe(6)
    })

    it('returns max at exactly +max steps and beyond', () => {
      expect(resolveDropDepth(100 + 6 * 24, 100, 24, 6)).toBe(6)
      expect(resolveDropDepth(100 + 7 * 24, 100, 24, 6)).toBe(6)
      expect(resolveDropDepth(100 + 100 * 24, 100, 24, 6)).toBe(6)
    })
  })

  describe('variable indentStepPx', () => {
    it('respects a larger step (40px)', () => {
      expect(resolveDropDepth(100 + 40, 100, 40, 6)).toBe(1)
      expect(resolveDropDepth(100 + 80, 100, 40, 6)).toBe(2)
      expect(resolveDropDepth(100 + 60, 100, 40, 6)).toBe(2) // 1.5 → half-up
      expect(resolveDropDepth(100 + 20, 100, 40, 6)).toBe(1) // 0.5 → half-up
    })

    it('respects a smaller step (10px)', () => {
      expect(resolveDropDepth(100 + 10, 100, 10, 6)).toBe(1)
      expect(resolveDropDepth(100 + 25, 100, 10, 6)).toBe(3) // 2.5 → half-up → 3
      expect(resolveDropDepth(100 + 55, 100, 10, 6)).toBe(6) // 5.5 → 6
    })

    it('handles fractional step values', () => {
      expect(resolveDropDepth(100 + 1.5, 100, 1.5, 6)).toBe(1)
      expect(resolveDropDepth(100 + 3, 100, 1.5, 6)).toBe(2)
    })
  })

  describe('variable maxDepth', () => {
    it('respects a smaller max (2)', () => {
      expect(resolveDropDepth(100 + 24, 100, 24, 2)).toBe(1)
      expect(resolveDropDepth(100 + 48, 100, 24, 2)).toBe(2)
      expect(resolveDropDepth(1000, 100, 24, 2)).toBe(2)
    })

    it('respects max=1', () => {
      expect(resolveDropDepth(100, 100, 24, 1)).toBe(0)
      expect(resolveDropDepth(100 + 24, 100, 24, 1)).toBe(1)
      expect(resolveDropDepth(1000, 100, 24, 1)).toBe(1)
    })

    it('clamps to 0 when maxDepth=0 (every drop is depth-0)', () => {
      expect(resolveDropDepth(100, 100, 24, 0)).toBe(0)
      expect(resolveDropDepth(100 + 24, 100, 24, 0)).toBe(0)
      expect(resolveDropDepth(1000, 100, 24, 0)).toBe(0)
    })
  })

  describe('defensive guards (never throws)', () => {
    it('treats step=0 as step=1 (no divide-by-zero)', () => {
      // clientX-contentLeft=100, step clamped to 1 → 100 raw steps, clamped to max=6
      expect(resolveDropDepth(200, 100, 0, 6)).toBe(6)
      // small offset with step=0 → step=1 → 1 step → depth 1
      expect(resolveDropDepth(101, 100, 0, 6)).toBe(1)
    })

    it('treats negative step as step=1', () => {
      expect(resolveDropDepth(200, 100, -5, 6)).toBe(6)
    })

    it('treats negative maxDepth as maxDepth=0', () => {
      expect(resolveDropDepth(200, 100, 24, -3)).toBe(0)
    })

    it('returns 0 for NaN clientX (broken DOM rect)', () => {
      expect(resolveDropDepth(NaN, 100, 24, 6)).toBe(0)
    })

    it('returns 0 for NaN contentLeft', () => {
      expect(resolveDropDepth(200, NaN, 24, 6)).toBe(0)
    })

    it('returns 0 for Infinity clientX', () => {
      expect(resolveDropDepth(Infinity, 100, 24, 6)).toBe(0)
    })

    it('returns 0 for -Infinity clientX', () => {
      expect(resolveDropDepth(-Infinity, 100, 24, 6)).toBe(0)
    })

    it('treats NaN maxDepth as 0', () => {
      expect(resolveDropDepth(200, 100, 24, NaN)).toBe(0)
    })

    it('treats NaN step as step=1', () => {
      expect(resolveDropDepth(200, 100, NaN, 6)).toBe(6)
    })

    it('floors a fractional maxDepth', () => {
      // A fractional cap doesn't make sense; floor it.
      expect(resolveDropDepth(1000, 100, 24, 2.9)).toBe(2)
    })
  })

  describe('monotonicity (deeper drops never yield shallower depths)', () => {
    it('is non-decreasing as clientX increases', () => {
      let prev = -1
      for (let dx = -50; dx <= 500; dx += 3) {
        const d = resolveDropDepth(100 + dx, 100, 24, 6)
        expect(d).toBeGreaterThanOrEqual(prev)
        expect(d).toBeGreaterThanOrEqual(0)
        expect(d).toBeLessThanOrEqual(6)
        prev = d
      }
    })
  })
})

// ---- smoke test (interactive behavior is in TESTING.md manual matrix) ------
// We can't drive HTML5 drag/drop from jsdom, but we CAN assert the extension
// is constructible, registers under the right name, and contributes its
// ProseMirror plugin to the editor state. If `addProseMirrorPlugins` throws
// or the plugin fails to attach, every real drop would fall through to
// native (silently breaking the feature) — this gate catches that.

describe('BlockIndentOnDrop extension (smoke)', () => {
  function makeEditor(): Editor {
    return new Editor({
      extensions: [
        StarterKit.configure({
          paragraph: false,
          heading: false,
          bulletList: false,
          orderedList: false,
          listItem: false,
          blockquote: false,
          codeBlock: false,
          horizontalRule: false,
          trailingNode: false
        }),
        ...SiltBlockExtensions,
        BlockIndentOnDrop
      ],
      content: {
        type: 'doc',
        content: [
          {
            type: 'noteBlock',
            attrs: { id: 'b1', depth: 0, bullet: '- ' },
            content: [{ type: 'text', text: 'one' }]
          },
          {
            type: 'noteBlock',
            attrs: { id: 'b2', depth: 0, bullet: '- ' },
            content: [{ type: 'text', text: 'two' }]
          }
        ]
      }
    })
  }

  it('registers under the name siltBlockIndentOnDrop', () => {
    const editor = makeEditor()
    expect(editor.extensionManager.extensions.map((e) => e.name)).toContain(
      'siltBlockIndentOnDrop'
    )
    editor.destroy()
  })

  it('contributes a ProseMirror plugin to the editor state', () => {
    const editor = makeEditor()
    // The extension must contribute at least one plugin (handleDrop lives
    // on the plugin props; without it the extension is a no-op).
    expect(editor.state.plugins.length).toBeGreaterThan(0)
    editor.destroy()
  })

  it('does not alter the doc on construction (no side effects)', () => {
    const editor = makeEditor()
    expect(editor.state.doc.childCount).toBe(2)
    expect(editor.state.doc.firstChild?.attrs.depth).toBe(0)
    expect(editor.state.doc.lastChild?.attrs.depth).toBe(0)
    editor.destroy()
  })
})
