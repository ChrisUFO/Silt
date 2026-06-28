// Direct unit tests for focusState (#322 hardening). Pins the setter
// semantics + the window-event side-effect so future refactors can't
// silently regress the bidirectional contract between Calendar.svelte
// and CalendarSidebar.svelte.
import { describe, expect, it, beforeEach, vi } from 'vitest'
import {
  getFocusState,
  setActiveFilter,
  clearActiveFilter,
  setFocusDate,
  clearFocusDate,
  resetFocusState,
  resetFocusStateForTests
} from './focusState.svelte'

describe('focusState (#322)', () => {
  beforeEach(() => {
    resetFocusStateForTests()
  })

  describe('getFocusState()', () => {
    it('returns the default state after reset', () => {
      const s = getFocusState()
      expect(s.focusDate).toBe('')
      expect(s.activeFilter).toBe('all')
    })
  })

  describe('setActiveFilter() / clearActiveFilter()', () => {
    it('setActiveFilter writes the filter value', () => {
      setActiveFilter('today')
      expect(getFocusState().activeFilter).toBe('today')
    })

    it('clearActiveFilter resets to "all"', () => {
      setActiveFilter('overdue')
      clearActiveFilter()
      expect(getFocusState().activeFilter).toBe('all')
    })
  })

  describe('setFocusDate() / clearFocusDate()', () => {
    it('setFocusDate writes the YYYY-MM-DD value', () => {
      setFocusDate('2026-06-16')
      expect(getFocusState().focusDate).toBe('2026-06-16')
    })

    it('clearFocusDate resets to empty string', () => {
      setFocusDate('2026-06-16')
      clearFocusDate()
      expect(getFocusState().focusDate).toBe('')
    })

    it('setFocusDate dispatches "calendar:focus-date" on window', () => {
      const handler = vi.fn()
      window.addEventListener('calendar:focus-date', handler)
      setFocusDate('2026-06-20')
      expect(handler).toHaveBeenCalledTimes(1)
      const detail = (handler.mock.calls[0][0] as CustomEvent).detail
      expect(detail.date).toBe('2026-06-20')
      window.removeEventListener('calendar:focus-date', handler)
    })

    it('clearFocusDate also dispatches the event with empty detail', () => {
      const handler = vi.fn()
      setFocusDate('2026-06-16')
      window.addEventListener('calendar:focus-date', handler)
      clearFocusDate()
      expect(handler).toHaveBeenCalledTimes(1)
      const detail = (handler.mock.calls[0][0] as CustomEvent).detail
      expect(detail.date).toBe('')
      window.removeEventListener('calendar:focus-date', handler)
    })
  })

  describe('resetFocusState() (production path)', () => {
    // The production-mode reset is called by CalendarSidebar's
    // refresh-navigation handler to drop stale focusDate / activeFilter
    // state on vault switch (#141 SwitchVault, #323 hardening). It must
    // be identical to the test reset — the only difference is the
    // intended caller. We test both reset points to make sure the
    // production reset stays exported and doesn't drift.
    it('clears focusDate AND activeFilter', () => {
      setFocusDate('2026-06-16')
      setActiveFilter('overdue')
      resetFocusState()
      expect(getFocusState().focusDate).toBe('')
      expect(getFocusState().activeFilter).toBe('all')
    })
  })
})
