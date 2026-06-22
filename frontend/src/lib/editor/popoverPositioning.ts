/**
 * Clamp a popover's intended top-left position so the popover stays inside the
 * viewport with an 8px margin. Used by every selection-anchored popover
 * (slash menu, context menu, link input, color picker, meta-suggest) — the
 * previous inline copies had subtly hard-coded widths/heights that drifted
 * from the actual popover size.
 *
 * Pure: reads viewport dimensions from the args so it is unit-testable.
 */
export interface Viewport {
  width: number
  height: number
}

export interface BoundedRect {
  /** Intended top-left X (e.g. the cursor's `clientX`). */
  x: number
  /** Intended top-left Y (e.g. the cursor's `clientY`). */
  y: number
  /** Width of the popover that will hang off (x, y). */
  width: number
  /** Height of the popover that will hang off (x, y). */
  height: number
}

export const POPOVER_MARGIN = 8

/**
 * Returns the clamped `{ left, top }` so the popover is fully on-screen.
 * If the popover is larger than the viewport, both axes fall back to the
 * margin so it stays anchored at the top-left rather than going negative.
 */
export function clampToViewport(
  rect: BoundedRect,
  viewport: Viewport
): { left: number; top: number } {
  let { x: left, y: top, width, height } = rect
  if (left + width > viewport.width) {
    left = viewport.width - width - POPOVER_MARGIN
  }
  if (top + height > viewport.height) {
    top = viewport.height - height - POPOVER_MARGIN
  }
  left = Math.max(POPOVER_MARGIN, left)
  top = Math.max(POPOVER_MARGIN, top)
  return { left, top }
}
