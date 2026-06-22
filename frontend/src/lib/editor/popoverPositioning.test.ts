import { describe, it, expect } from 'vitest'
import { clampToViewport, POPOVER_MARGIN } from './popoverPositioning'

describe('clampToViewport', () => {
  it('returns the input position when fully inside the viewport', () => {
    const result = clampToViewport(
      { x: 100, y: 100, width: 200, height: 100 },
      { width: 1000, height: 800 }
    )
    expect(result).toEqual({ left: 100, top: 100 })
  })

  it('shifts left when the popover would overflow the right edge', () => {
    const result = clampToViewport(
      { x: 900, y: 100, width: 200, height: 100 },
      { width: 1000, height: 800 }
    )
    // 900 + 200 = 1100 > 1000 → left = 1000 - 200 - 8 = 792
    expect(result.left).toBe(1000 - 200 - POPOVER_MARGIN)
    expect(result.top).toBe(100)
  })

  it('shifts up when the popover would overflow the bottom edge', () => {
    const result = clampToViewport(
      { x: 100, y: 750, width: 200, height: 100 },
      { width: 1000, height: 800 }
    )
    // 750 + 100 = 850 > 800 → top = 800 - 100 - 8 = 692
    expect(result.left).toBe(100)
    expect(result.top).toBe(800 - 100 - POPOVER_MARGIN)
  })

  it('enforces the 8px minimum on both axes', () => {
    const result = clampToViewport(
      { x: 0, y: 0, width: 100, height: 50 },
      { width: 1000, height: 800 }
    )
    expect(result).toEqual({ left: POPOVER_MARGIN, top: POPOVER_MARGIN })
  })

  it('clamps negative coordinates to the margin', () => {
    const result = clampToViewport(
      { x: -50, y: -30, width: 100, height: 50 },
      { width: 1000, height: 800 }
    )
    expect(result).toEqual({ left: POPOVER_MARGIN, top: POPOVER_MARGIN })
  })

  it('handles a popover larger than the viewport by pinning to top-left', () => {
    // Width 1200 > viewport 1000 → left would go negative; the Math.max
    // floor keeps it at the margin.
    const result = clampToViewport(
      { x: 500, y: 500, width: 1200, height: 900 },
      { width: 1000, height: 800 }
    )
    expect(result).toEqual({ left: POPOVER_MARGIN, top: POPOVER_MARGIN })
  })

  it('handles both axes overflowing simultaneously', () => {
    const result = clampToViewport(
      { x: 900, y: 750, width: 200, height: 100 },
      { width: 1000, height: 800 }
    )
    expect(result.left).toBe(1000 - 200 - POPOVER_MARGIN)
    expect(result.top).toBe(800 - 100 - POPOVER_MARGIN)
  })
})
