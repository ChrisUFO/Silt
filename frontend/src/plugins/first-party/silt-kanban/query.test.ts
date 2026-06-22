import { describe, it, expect } from 'vitest'
import { buildQuery, type QueryCtxLike } from './query'
import type { KanbanFilters, Scope } from './types'

const ctx: QueryCtxLike = {
  activeNotebook: 'Work',
  activeSection: 'Journal',
  activePage: 'Today',
  today: '2026-06-22'
}

const emptyFilters: KanbanFilters = {
  owners: [],
  priorities: [],
  dueDate: '',
  tags: []
}

describe('buildQuery — scope branches', () => {
  it('vault scope adds no WHERE for scope', () => {
    const { sql, params } = buildQuery('vault', emptyFilters, ctx)
    expect(sql).toContain('WHERE 1=1')
    expect(params).toEqual([])
  })

  it('notebook scope filters by activeNotebook only', () => {
    const { sql, params } = buildQuery('notebook', emptyFilters, ctx)
    expect(sql).toContain('b.notebook = ?')
    expect(sql).not.toContain('b.section = ?')
    expect(sql).not.toContain('b.page = ?')
    expect(params).toEqual(['Work'])
  })

  it('section scope filters by notebook + section', () => {
    const { sql, params } = buildQuery('section', emptyFilters, ctx)
    expect(sql).toContain('b.notebook = ?')
    expect(sql).toContain('b.section = ?')
    expect(sql).not.toContain('b.page = ?')
    expect(params).toEqual(['Work', 'Journal'])
  })

  it('page scope filters by notebook + section + page', () => {
    const { sql, params } = buildQuery('page', emptyFilters, ctx)
    expect(sql).toContain('b.notebook = ?')
    expect(sql).toContain('b.section = ?')
    expect(sql).toContain('b.page = ?')
    expect(params).toEqual(['Work', 'Journal', 'Today'])
  })
})

describe('buildQuery — filter branches', () => {
  it('owners filter adds parameterised IN clause', () => {
    const filters: KanbanFilters = {
      ...emptyFilters,
      owners: ['Alice', 'Bob']
    }
    const { sql, params } = buildQuery('vault', filters, ctx)
    expect(sql).toContain('t.owner IN (?, ?)')
    expect(params).toEqual(['Alice', 'Bob'])
  })

  it('priorities filter adds parameterised IN clause', () => {
    const filters: KanbanFilters = {
      ...emptyFilters,
      priorities: [1, 3]
    }
    const { sql, params } = buildQuery('vault', filters, ctx)
    expect(sql).toContain('t.priority IN (?, ?)')
    expect(params).toEqual([1, 3])
  })

  it('empty owners filter is a no-op (no IN clause)', () => {
    const { sql, params } = buildQuery('vault', emptyFilters, ctx)
    expect(sql).not.toContain('t.owner IN')
    expect(params).toEqual([])
  })

  it('dueDate=overdue uses lexicographic less-than today', () => {
    const filters: KanbanFilters = {
      ...emptyFilters,
      dueDate: 'overdue'
    }
    const { sql, params } = buildQuery('vault', filters, ctx)
    expect(sql).toContain('t.due_date < ?')
    expect(params).toEqual(['2026-06-22'])
  })

  it('dueDate=today uses equality against today', () => {
    const filters: KanbanFilters = {
      ...emptyFilters,
      dueDate: 'today'
    }
    const { sql, params } = buildQuery('vault', filters, ctx)
    expect(sql).toContain('t.due_date = ?')
    expect(params).toEqual(['2026-06-22'])
  })

  it('dueDate=week uses BETWEEN today and today+7', () => {
    const filters: KanbanFilters = {
      ...emptyFilters,
      dueDate: 'week'
    }
    const { sql, params } = buildQuery('vault', filters, ctx)
    expect(sql).toContain('t.due_date BETWEEN ? AND ?')
    expect(params).toEqual(['2026-06-22', '2026-06-29'])
  })

  it('dueDate=none matches NULL or empty string', () => {
    const filters: KanbanFilters = {
      ...emptyFilters,
      dueDate: 'none'
    }
    const { sql, params } = buildQuery('vault', filters, ctx)
    expect(sql).toContain("(t.due_date IS NULL OR t.due_date = '')")
    expect(params).toEqual([])
  })

  it('tags filter adds subquery on tags table', () => {
    const filters: KanbanFilters = {
      ...emptyFilters,
      tags: ['work/project', 'personal']
    }
    const { sql, params } = buildQuery('vault', filters, ctx)
    expect(sql).toContain(
      'b.id IN (SELECT block_id FROM tags WHERE raw_path IN (?, ?))'
    )
    expect(params).toEqual(['work/project', 'personal'])
  })
})

describe('buildQuery — combined scope + filters', () => {
  it('vault-scope + no filters produces WHERE 1=1 with no params', () => {
    const { sql, params } = buildQuery('vault' as Scope, emptyFilters, ctx)
    expect(sql).toContain('WHERE 1=1')
    expect(params).toEqual([])
  })

  it('notebook + owners + priorities + dueToday combine via AND', () => {
    const filters: KanbanFilters = {
      owners: ['Alice'],
      priorities: [2],
      dueDate: 'today',
      tags: []
    }
    const { sql, params } = buildQuery('notebook', filters, ctx)
    expect(sql).toContain('b.notebook = ?')
    expect(sql).toContain('t.owner IN (?)')
    expect(sql).toContain('t.priority IN (?)')
    expect(sql).toContain('t.due_date = ?')
    expect(params).toEqual(['Work', 'Alice', 2, '2026-06-22'])
  })

  it('always includes the priority + due_date ORDER BY', () => {
    const { sql } = buildQuery('vault', emptyFilters, ctx)
    expect(sql).toContain(
      "ORDER BY t.priority ASC, COALESCE(t.due_date, '9999-12-31') ASC"
    )
  })
})
