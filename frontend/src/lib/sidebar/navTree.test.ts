import { describe, it, expect } from 'vitest'
import { reconcileActive, generateUniquePageName } from './navTree'
import type { NavigationTree } from './types'

const emptyTree: NavigationTree = { notebooks: [] }

function treeWith(
  notebooks: {
    name: string
    sections?: { name: string; pages?: { name: string }[] }[]
  }[]
): NavigationTree {
  return {
    notebooks: notebooks.map((nb) => ({
      name: nb.name,
      sections: (nb.sections ?? []).map((sec) => ({
        name: sec.name,
        pages: (sec.pages ?? []).map((p) => ({ name: p.name, count: 0 }))
      }))
    }))
  }
}

describe('reconcileActive', () => {
  it('returns current unchanged when tree is empty', () => {
    const next = reconcileActive(emptyTree, {
      notebook: 'Work',
      section: 'Journal',
      page: 'Today'
    })
    expect(next).toEqual({
      notebook: 'Work',
      section: 'Journal',
      page: 'Today'
    })
  })

  it('falls back to the first notebook when active notebook is missing', () => {
    const tree = treeWith([{ name: 'Work' }, { name: 'Personal' }])
    const next = reconcileActive(tree, {
      notebook: 'Gone',
      section: 'Whatever',
      page: 'X'
    })
    expect(next.notebook).toBe('Work')
    expect(next.section).toBe('') // section was for the gone notebook
    expect(next.page).toBe('X') // page is preserved (existing behaviour)
  })

  it('picks the first notebook when activeNotebook is empty', () => {
    const tree = treeWith([{ name: 'Work' }])
    const next = reconcileActive(tree, { notebook: '', section: '', page: '' })
    expect(next.notebook).toBe('Work')
  })

  it('clears section when active notebook exists but section does not', () => {
    const tree = treeWith([{ name: 'Work', sections: [{ name: 'Active' }] }])
    const next = reconcileActive(tree, {
      notebook: 'Work',
      section: 'Deleted',
      page: 'P'
    })
    expect(next.notebook).toBe('Work')
    expect(next.section).toBe('')
    expect(next.page).toBe('P')
  })

  it('preserves section when notebook + section both still exist', () => {
    const tree = treeWith([
      { name: 'Work', sections: [{ name: 'Journal' }, { name: 'Active' }] }
    ])
    const next = reconcileActive(tree, {
      notebook: 'Work',
      section: 'Journal',
      page: 'Today'
    })
    expect(next).toEqual({
      notebook: 'Work',
      section: 'Journal',
      page: 'Today'
    })
  })

  it('clears section when notebook just changed (section belonged to old notebook)', () => {
    // Active was SomeOldNotebook/OldSection; SomeOldNotebook is gone → fall
    // back to first notebook (Work), whose sections don't include OldSection
    // → section cleared.
    const tree = treeWith([
      { name: 'Work', sections: [{ name: 'Journal' }] },
      { name: 'Personal', sections: [{ name: 'Home' }] }
    ])
    const next = reconcileActive(tree, {
      notebook: 'SomeOldNotebook',
      section: 'OldSection',
      page: ''
    })
    expect(next.notebook).toBe('Work')
    expect(next.section).toBe('')
  })

  it('preserves empty section (no false clear)', () => {
    const tree = treeWith([{ name: 'Work', sections: [{ name: 'Journal' }] }])
    const next = reconcileActive(tree, {
      notebook: 'Work',
      section: '',
      page: ''
    })
    expect(next.section).toBe('')
  })

  it('does not mutate the input current', () => {
    const tree = treeWith([{ name: 'Work' }])
    const current = { notebook: 'Gone', section: 'X', page: 'Y' }
    const next = reconcileActive(tree, current)
    expect(current).toEqual({ notebook: 'Gone', section: 'X', page: 'Y' })
    expect(next).not.toBe(current)
  })
})

describe('generateUniquePageName', () => {
  it('returns "Untitled" when notebook is missing', () => {
    expect(generateUniquePageName(emptyTree, 'Work', 'Journal')).toBe(
      'Untitled'
    )
  })

  it('returns "Untitled" when section is missing', () => {
    const tree = treeWith([{ name: 'Work', sections: [] }])
    expect(generateUniquePageName(tree, 'Work', 'Journal')).toBe('Untitled')
  })

  it('returns "Untitled" when section has no pages', () => {
    const tree = treeWith([
      { name: 'Work', sections: [{ name: 'Journal', pages: [] }] }
    ])
    expect(generateUniquePageName(tree, 'Work', 'Journal')).toBe('Untitled')
  })

  it('returns "Untitled" when "Untitled" is not taken', () => {
    const tree = treeWith([
      {
        name: 'Work',
        sections: [{ name: 'Journal', pages: [{ name: 'Other' }] }]
      }
    ])
    expect(generateUniquePageName(tree, 'Work', 'Journal')).toBe('Untitled')
  })

  it('returns "Untitled 2" when "Untitled" exists', () => {
    const tree = treeWith([
      {
        name: 'Work',
        sections: [{ name: 'Journal', pages: [{ name: 'Untitled' }] }]
      }
    ])
    expect(generateUniquePageName(tree, 'Work', 'Journal')).toBe('Untitled 2')
  })

  it('skips to "Untitled 3" when "Untitled" and "Untitled 2" both exist', () => {
    const tree = treeWith([
      {
        name: 'Work',
        sections: [
          {
            name: 'Journal',
            pages: [{ name: 'Untitled' }, { name: 'Untitled 2' }]
          }
        ]
      }
    ])
    expect(generateUniquePageName(tree, 'Work', 'Journal')).toBe('Untitled 3')
  })
})
