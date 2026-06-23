import { vi } from 'vitest'
import '@testing-library/jest-dom/vitest'

// jsdom omits or stubs layout-dependent DOM APIs that TipTap v3's Placeholder
// viewport tracker touches during editor construction. Force-override them
// (not conditionally) because some jsdom versions define elementFromPoint as
// a non-functional stub that still throws.
if (typeof document !== 'undefined') {
  document.elementFromPoint = vi.fn(() => document.body)
}
