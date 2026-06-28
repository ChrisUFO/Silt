import { describe, it, expect } from 'vitest'
import { VIEW_CYCLE, nextView } from './viewCycle'

describe('VIEW_CYCLE (#322 — Agenda merged into Calendar)', () => {
  it('contains the 4 standard views in canonical order, no standalone agenda', () => {
    expect([...VIEW_CYCLE]).toEqual(['notes', 'tags', 'calendar', 'kanban'])
  })
})

describe('nextView', () => {
  it('cycles notes → tags → calendar → kanban → notes (#322)', () => {
    expect(nextView('notes')).toBe('tags')
    expect(nextView('tags')).toBe('calendar')
    expect(nextView('calendar')).toBe('kanban')
    expect(nextView('kanban')).toBe('notes')
  })

  it("'agenda' is no longer in the cycle — falls back to notes (#322)", () => {
    // The Agenda view was merged into Calendar; routing an activeView of
    // 'agenda' through the cycle should reset to 'notes' rather than skip
    // through Calendar. The unified Calendar already exposes the Agenda
    // layout via its mode toggle.
    expect(nextView('agenda')).toBe('notes')
  })

  it('wraps from the last view back to the first', () => {
    expect(nextView('kanban')).toBe('notes')
  })

  it("returns 'notes' when current is not in the cycle (e.g. a plugin view)", () => {
    expect(nextView('silt-custom-view')).toBe('notes')
  })

  it("returns 'notes' when current is empty", () => {
    expect(nextView('')).toBe('notes')
  })
})
