import { describe, it, expect } from 'vitest'
import { VIEW_CYCLE, nextView } from './viewCycle'

describe('VIEW_CYCLE', () => {
  it('contains the 5 standard views in canonical order', () => {
    expect([...VIEW_CYCLE]).toEqual([
      'notes',
      'tags',
      'agenda',
      'calendar',
      'kanban'
    ])
  })
})

describe('nextView', () => {
  it('cycles notes → tags → agenda → calendar → kanban → notes', () => {
    expect(nextView('notes')).toBe('tags')
    expect(nextView('tags')).toBe('agenda')
    expect(nextView('agenda')).toBe('calendar')
    expect(nextView('calendar')).toBe('kanban')
    expect(nextView('kanban')).toBe('notes')
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
