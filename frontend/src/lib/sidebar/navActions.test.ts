import { describe, it, expect } from 'vitest'
import {
  linkedNotebookId,
  isLinkedNotebook,
  deleteTargetLabel,
  reconcileActiveAfterDelete,
  findNotebook,
  type DeleteTarget
} from './navActions'
import type { NavigationTree } from './types'

describe('linkedNotebookId', () => {
  it('returns null when notebook is undefined', () => {
    expect(linkedNotebookId(undefined)).toBeNull()
  })

  it('returns null for a vault notebook (no source)', () => {
    expect(linkedNotebookId({ name: 'Work', sections: [] })).toBeNull()
  })

  it('returns null when source is "vault"', () => {
    expect(
      linkedNotebookId({ name: 'Work', sections: [], source: 'vault' })
    ).toBeNull()
  })

  it('extracts the id from a linked:<id> source', () => {
    expect(
      linkedNotebookId({
        name: 'Synced',
        sections: [],
        source: 'linked:abc-123'
      })
    ).toBe('abc-123')
  })

  it('returns null when the linked: prefix is present but id is empty', () => {
    expect(
      linkedNotebookId({ name: 'Synced', sections: [], source: 'linked:' })
    ).toBeNull()
  })
})

describe('isLinkedNotebook', () => {
  it('returns true for a linked notebook', () => {
    expect(
      isLinkedNotebook({
        name: 'Synced',
        sections: [],
        source: 'linked:abc'
      })
    ).toBe(true)
  })

  it('returns false for a vault notebook', () => {
    expect(isLinkedNotebook({ name: 'Work', sections: [] })).toBe(false)
  })
})

describe('deleteTargetLabel', () => {
  it('builds a page label', () => {
    const target: DeleteTarget = {
      level: 'page',
      notebook: 'Work',
      section: 'Journal',
      page: 'Today'
    }
    expect(deleteTargetLabel(target)).toBe('page "Today"')
  })

  it('builds a section label including "and all its pages"', () => {
    const target: DeleteTarget = {
      level: 'section',
      notebook: 'Work',
      section: 'Journal'
    }
    expect(deleteTargetLabel(target)).toBe(
      'section "Journal" and all its pages'
    )
  })

  it('builds a notebook label including "and all its content"', () => {
    const target: DeleteTarget = {
      level: 'notebook',
      notebook: 'Work'
    }
    expect(deleteTargetLabel(target)).toBe(
      'notebook "Work" and all its content'
    )
  })
})

describe('reconcileActiveAfterDelete', () => {
  const current = { notebook: 'Work', section: 'Journal', page: 'Today' }

  it('clears all three when the active notebook is deleted', () => {
    const next = reconcileActiveAfterDelete(
      { level: 'notebook', notebook: 'Work' },
      current
    )
    expect(next).toEqual({ notebook: '', section: '', page: '' })
  })

  it('clears section + page when the active section is deleted', () => {
    const next = reconcileActiveAfterDelete(
      { level: 'section', notebook: 'Work', section: 'Journal' },
      current
    )
    expect(next).toEqual({ notebook: 'Work', section: '', page: '' })
  })

  it('clears only page when the active page is deleted', () => {
    const next = reconcileActiveAfterDelete(
      { level: 'page', notebook: 'Work', section: 'Journal', page: 'Today' },
      current
    )
    expect(next).toEqual({ notebook: 'Work', section: 'Journal', page: '' })
  })

  it('leaves current unchanged when an unrelated notebook is deleted', () => {
    const next = reconcileActiveAfterDelete(
      { level: 'notebook', notebook: 'Other' },
      current
    )
    expect(next).toEqual(current)
    expect(next).not.toBe(current) // returns a new object, never mutates
  })

  it('leaves current unchanged when an unrelated section is deleted', () => {
    const next = reconcileActiveAfterDelete(
      { level: 'section', notebook: 'Work', section: 'Other' },
      current
    )
    expect(next).toEqual(current)
  })

  it('leaves current unchanged when an unrelated page is deleted', () => {
    const next = reconcileActiveAfterDelete(
      {
        level: 'page',
        notebook: 'Work',
        section: 'Journal',
        page: 'Other'
      },
      current
    )
    expect(next).toEqual(current)
  })

  it('handles notebook delete when current notebook was already empty', () => {
    const next = reconcileActiveAfterDelete(
      { level: 'notebook', notebook: 'Work' },
      { notebook: '', section: '', page: '' }
    )
    expect(next).toEqual({ notebook: '', section: '', page: '' })
  })
})

describe('findNotebook', () => {
  const tree: NavigationTree = {
    notebooks: [
      { name: 'Work', sections: [] },
      { name: 'Personal', sections: [] }
    ]
  }

  it('returns the matching notebook', () => {
    expect(findNotebook(tree, 'Personal')?.name).toBe('Personal')
  })

  it('returns undefined when no match', () => {
    expect(findNotebook(tree, 'Missing')).toBeUndefined()
  })
})
