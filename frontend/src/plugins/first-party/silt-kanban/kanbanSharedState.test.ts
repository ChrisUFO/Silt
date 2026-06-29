// Direct unit tests for kanbanSharedState (#323 hardening). These pin the
// setter semantics + the scope-user-override invariant so future refactors
// can't silently regress the bidirectional contract between Kanban.svelte
// and KanbanSidebar.svelte.
import { describe, expect, it, beforeEach } from 'vitest'
import type { KanbanFilters } from './types'
import {
  getKanbanState,
  setScope,
  setFilters,
  clearFilters,
  narrowScopeTo,
  clearScopeOverride,
  applySavedBoard,
  initFromConfig,
  resetKanbanState
} from './kanbanSharedState.svelte'

describe('kanbanSharedState (#323)', () => {
  beforeEach(() => {
    resetKanbanState()
  })

  describe('getKanbanState()', () => {
    it('returns the default state after reset', () => {
      const s = getKanbanState()
      expect(s.scope).toBe('vault')
      expect(s.scopeUserOverride).toBe(false)
      expect(s.filters).toEqual({
        owners: [],
        priorities: [],
        dueDate: '',
        tags: []
      })
    })
  })

  describe('setScope() — user-initiated scope change', () => {
    it('mutates scope AND flips scopeUserOverride', () => {
      setScope('notebook')
      const s = getKanbanState()
      expect(s.scope).toBe('notebook')
      expect(s.scopeUserOverride).toBe(true)
    })

    it('a second setScope re-flips the override (no-op semantics)', () => {
      setScope('notebook')
      setScope('section')
      const s = getKanbanState()
      expect(s.scope).toBe('section')
      expect(s.scopeUserOverride).toBe(true)
    })
  })

  describe('narrowScopeTo() — navigation auto-narrow', () => {
    it('mutates scope WITHOUT flipping scopeUserOverride', () => {
      narrowScopeTo('page')
      const s = getKanbanState()
      expect(s.scope).toBe('page')
      expect(s.scopeUserOverride).toBe(false)
    })

    it('is a no-op when scopeUserOverride is true (#124 invariant)', () => {
      setScope('notebook') // flips override
      narrowScopeTo('vault') // nav would normally narrow to vault
      const s = getKanbanState()
      // Scope stays 'notebook' (the user's pick), override stays true.
      expect(s.scope).toBe('notebook')
      expect(s.scopeUserOverride).toBe(true)
    })
  })

  describe('clearScopeOverride() — Follow affordance', () => {
    it('clears scopeUserOverride so nav can re-narrow', () => {
      setScope('section')
      clearScopeOverride()
      const s = getKanbanState()
      expect(s.scope).toBe('section') // scope unchanged
      expect(s.scopeUserOverride).toBe(false)
    })

    it('a subsequent narrowScopeTo actually mutates again', () => {
      setScope('section')
      clearScopeOverride()
      narrowScopeTo('page')
      expect(getKanbanState().scope).toBe('page')
    })
  })

  describe('setFilters() / clearFilters()', () => {
    it('setFilters replaces the filters object entirely', () => {
      setFilters({
        owners: ['alice'],
        priorities: [1, 2],
        dueDate: 'overdue',
        tags: ['backend']
      })
      const s = getKanbanState()
      expect(s.filters.owners).toEqual(['alice'])
      expect(s.filters.priorities).toEqual([1, 2])
      expect(s.filters.dueDate).toBe('overdue')
      expect(s.filters.tags).toEqual(['backend'])
    })

    it('clearFilters resets to empty defaults', () => {
      setFilters({
        owners: ['alice'],
        priorities: [1],
        dueDate: 'today',
        tags: ['x']
      })
      clearFilters()
      expect(getKanbanState().filters).toEqual({
        owners: [],
        priorities: [],
        dueDate: '',
        tags: []
      })
    })
  })

  describe('applySavedBoard() — clicking a saved board', () => {
    it('restores scope + filters and flips the override', () => {
      setScope('section') // user already has override
      applySavedBoard({
        scope: 'page',
        filters: {
          owners: ['bob'],
          priorities: [1],
          dueDate: '',
          tags: []
        }
      })
      const s = getKanbanState()
      expect(s.scope).toBe('page')
      expect(s.filters.owners).toEqual(['bob'])
      expect(s.scopeUserOverride).toBe(true)
    })

    it('replaces the filter object (caller mutations to nested arrays still alias)', () => {
      // The shallow spread `{ ...b.filters }` clones the top-level
      // KanbanFilters but the inner arrays alias the caller's data.
      // That's the documented contract — inner arrays come from the
      // caller's own UI state and are not deep-frozen. The top-level
      // replacement IS what prevents re-renders from re-reading stale
      // data via the reactivity contract.
      // The explicit : KanbanFilters annotation narrows the literal so
      // dueDate: '' is typed as the literal '' (not the wider string) —
      // matches KanbanFilters' declared type. Without the annotation TS
      // widens to string and the applySavedBoard call below fails to
      // typecheck (TS2345).
      const filters: KanbanFilters = {
        owners: ['alice'],
        priorities: [2],
        dueDate: '',
        tags: []
      }
      applySavedBoard({ scope: 'notebook', filters })
      expect(getKanbanState().filters).not.toBe(filters)
      expect(getKanbanState().filters.owners).toEqual(['alice'])
    })
  })

  describe('initFromConfig() — mount-time hydration', () => {
    it('seeds scope + filters + override flag', () => {
      initFromConfig(
        'section',
        {
          owners: ['a'],
          priorities: [3],
          dueDate: 'week',
          tags: ['x']
        },
        true
      )
      const s = getKanbanState()
      expect(s.scope).toBe('section')
      expect(s.filters.owners).toEqual(['a'])
      expect(s.scopeUserOverride).toBe(true)
    })
  })
})
