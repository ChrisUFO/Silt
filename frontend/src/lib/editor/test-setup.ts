import { vi } from 'vitest'

// jsdom omits a handful of DOM APIs that layout-dependent browser code (here:
// TipTap v3's Placeholder viewport tracker, which calls elementFromPoint)
// touches during editor construction. These are well-known jsdom gaps, not
// editor bugs. Polyfill them once here so every editor test gets a complete
// enough DOM to mount a real ProseMirror view.
if (typeof document !== 'undefined') {
  // elementFromPoint: jsdom has no layout engine, so return the document body
  // for any coordinate. This is sufficient for editor-mount smoke tests; tests
  // that genuinely need hit-testing assert on doc positions, not coordinates.
  if (typeof document.elementFromPoint !== 'function') {
    document.elementFromPoint = vi.fn(() => document.body)
  }
}
