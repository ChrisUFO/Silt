import { describe, it, expect, vi, beforeEach } from 'vitest'
import { sortByName, NavOrderManager } from './navOrder'
import type { NavOrderState } from './navOrder'

// Mock the Wails IPC bindings.
vi.mock('../../../wailsjs/go/main/App.js', () => ({
  GetNavOrder: vi.fn().mockResolvedValue({
    notebooks: ['Work', 'Personal'],
    sections: { Work: ['Journal', 'Projects'] },
    pages: { 'Work/Journal': ['2026-06-22', '2026-06-21'] }
  }),
  SetNavOrder: vi.fn().mockResolvedValue(undefined)
}))

describe('sortByName', () => {
  it('returns items in alphabetical order when no custom order', () => {
    const items = [{ name: 'C' }, { name: 'A' }, { name: 'B' }]
    const result = sortByName(items, undefined)
    expect(result.map((i) => i.name)).toEqual(['A', 'B', 'C'])
  })

  it('returns items in alphabetical order when order is empty', () => {
    const items = [{ name: 'C' }, { name: 'A' }, { name: 'B' }]
    const result = sortByName(items, [])
    expect(result.map((i) => i.name)).toEqual(['A', 'B', 'C'])
  })

  it('sorts by custom order, then alphabetical for unlisted items', () => {
    const items = [
      { name: 'C' },
      { name: 'A' },
      { name: 'B' },
      { name: 'D' }
    ]
    const result = sortByName(items, ['B', 'A'])
    expect(result.map((i) => i.name)).toEqual(['B', 'A', 'C', 'D'])
  })

  it('preserves original array (does not mutate)', () => {
    const items = [{ name: 'B' }, { name: 'A' }]
    const original = [...items]
    sortByName(items, ['A', 'B'])
    expect(items.map((i) => i.name)).toEqual(original.map((i) => i.name))
  })

  it('handles items not in the order list', () => {
    const items = [{ name: 'X' }, { name: 'A' }, { name: 'B' }]
    const result = sortByName(items, ['B'])
    expect(result.map((i) => i.name)).toEqual(['B', 'A', 'X'])
  })
})

describe('NavOrderManager', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('loads nav order from IPC', async () => {
    let state: NavOrderState | null = null
    const manager = new NavOrderManager({
      onStateChange: (s) => {
        state = s
      }
    })

    await manager.load()

    expect(state).not.toBeNull()
    expect(state!.notebooks).toEqual(['Work', 'Personal'])
    expect(state!.sections.Work).toEqual(['Journal', 'Projects'])
    expect(state!.pages['Work/Journal']).toEqual(['2026-06-22', '2026-06-21'])
  })

  it('persists section order', async () => {
    const { SetNavOrder } = await import('../../../wailsjs/go/main/App.js')
    let state: NavOrderState | null = null
    const manager = new NavOrderManager({
      onStateChange: (s) => {
        state = s
      }
    })

    // Load first to initialize state.
    await manager.load()
    await manager.persistSectionOrder('Work', ['Projects', 'Journal'])

    expect(state!.sections.Work).toEqual(['Projects', 'Journal'])
    expect(SetNavOrder).toHaveBeenCalledWith(
      expect.objectContaining({
        sections: { Work: ['Projects', 'Journal'] }
      })
    )
  })

  it('persists page order', async () => {
    const { SetNavOrder } = await import('../../../wailsjs/go/main/App.js')
    const manager = new NavOrderManager({ onStateChange: () => {} })

    await manager.load()
    await manager.persistPageOrder('Work/Journal', ['2026-06-21', '2026-06-22'])

    expect(manager.current.pages['Work/Journal']).toEqual([
      '2026-06-21',
      '2026-06-22'
    ])
    expect(SetNavOrder).toHaveBeenCalled()
  })

  it('load() is a no-op on IPC failure', async () => {
    const { GetNavOrder } = await import('../../../wailsjs/go/main/App.js')
    vi.mocked(GetNavOrder).mockRejectedValueOnce(new Error('no vault'))
    const manager = new NavOrderManager({ onStateChange: () => {} })

    // Should not throw.
    await manager.load()

    // State should remain defaults.
    expect(manager.current.notebooks).toEqual([])
  })
})
