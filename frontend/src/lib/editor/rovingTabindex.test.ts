import { describe, it, expect } from 'vitest'
import { nearestEnabledIndex } from './rovingTabindex'

describe('nearestEnabledIndex', () => {
  const none = [false, false, false, false, false, false]
  const del4 = [false, false, false, false, true, false] // index 4 disabled

  it('moves forward to the next enabled button', () => {
    expect(nearestEnabledIndex(none, 0, 1)).toBe(1)
    expect(nearestEnabledIndex(none, 2, 1)).toBe(3)
  })

  it('skips a disabled button going forward', () => {
    expect(nearestEnabledIndex(del4, 3, 1)).toBe(5)
  })

  it('wraps around going forward', () => {
    expect(nearestEnabledIndex(none, 5, 1)).toBe(0)
    expect(nearestEnabledIndex(del4, 5, 1)).toBe(0)
  })

  it('moves backward to the previous enabled button', () => {
    expect(nearestEnabledIndex(none, 3, -1)).toBe(2)
  })

  it('skips a disabled button going backward', () => {
    expect(nearestEnabledIndex(del4, 5, -1)).toBe(3)
  })

  it('wraps around going backward', () => {
    expect(nearestEnabledIndex(none, 0, -1)).toBe(5)
    expect(nearestEnabledIndex(del4, 0, -1)).toBe(5)
  })

  it('Home (from -1 forward) lands on first enabled', () => {
    expect(nearestEnabledIndex(none, -1, 1)).toBe(0)
    expect(nearestEnabledIndex(del4, -1, 1)).toBe(0)
  })

  it('End (from length backward) lands on last enabled', () => {
    expect(nearestEnabledIndex(none, 6, -1)).toBe(5)
    expect(nearestEnabledIndex(del4, 6, -1)).toBe(5)
  })

  it('Home with first button disabled skips to next', () => {
    expect(nearestEnabledIndex([true, false, false], -1, 1)).toBe(1)
  })

  it('End with last button disabled skips backward', () => {
    expect(nearestEnabledIndex([false, false, true], 3, -1)).toBe(1)
  })

  it('returns from unchanged when all buttons are disabled', () => {
    expect(nearestEnabledIndex([true, true, true], 1, 1)).toBe(1)
    expect(nearestEnabledIndex([true, true, true], 1, -1)).toBe(1)
  })

  it('returns from unchanged on an empty array', () => {
    expect(nearestEnabledIndex([], 0, 1)).toBe(0)
  })
})
