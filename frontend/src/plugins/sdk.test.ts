// Unit tests for the SDK date helpers introduced for #118: localToday() and
// plusDaysISO(). These power the Kanban due-date quick-picks so they compare
// against the LOCAL day instead of SQLite's UTC date('now'). Pure / deterministic
// (localToday is pinned via fake timers), no IPC, no Svelte runtime needed.

import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest'
import { localToday, plusDaysISO } from './sdk'

describe('localToday', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('returns YYYY-MM-DD in the local timezone', () => {
    vi.setSystemTime(new Date(2026, 5, 16, 23, 45)) // local 16 Jun 2026
    expect(localToday()).toBe('2026-06-16')
  })

  it('reflects the LOCAL day just after local midnight (not UTC)', () => {
    // The #118 bug: date('now') is UTC. localToday must return the local day.
    vi.setSystemTime(new Date(2026, 5, 17, 0, 15)) // local 17 Jun 2026 00:15
    expect(localToday()).toBe('2026-06-17')
  })

  it('matches the YYYY-MM-DD shape on a single-digit day/month', () => {
    vi.setSystemTime(new Date(2026, 0, 5, 9, 0)) // local 5 Jan 2026
    expect(localToday()).toBe('2026-01-05')
    expect(localToday()).toMatch(/^\d{4}-\d{2}-\d{2}$/)
  })
})

describe('plusDaysISO', () => {
  it('adds days within a month', () => {
    expect(plusDaysISO('2026-06-01', 5)).toBe('2026-06-06')
  })
  it('rolls over a month boundary', () => {
    expect(plusDaysISO('2026-06-30', 1)).toBe('2026-07-01')
  })
  it('rolls over a year boundary', () => {
    expect(plusDaysISO('2026-12-30', 3)).toBe('2027-01-02')
  })
  it('computes the Kanban "this week" bound (today + 7)', () => {
    expect(plusDaysISO('2026-06-16', 7)).toBe('2026-06-23')
  })
  it('supports negative offsets', () => {
    expect(plusDaysISO('2026-03-01', -1)).toBe('2026-02-28')
  })
  it('handles a leap day (2024)', () => {
    expect(plusDaysISO('2024-02-28', 1)).toBe('2024-02-29')
    expect(plusDaysISO('2024-02-29', 1)).toBe('2024-03-01')
  })
  it('skips Feb 29 on a non-leap year (2100)', () => {
    expect(plusDaysISO('2100-02-28', 1)).toBe('2100-03-01')
  })
})
