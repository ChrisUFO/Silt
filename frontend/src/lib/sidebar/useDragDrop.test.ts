import { describe, it, expect, vi, beforeEach } from 'vitest'
import { DragDropManager } from './useDragDrop'
import type { DragDropDeps } from './useDragDrop'
import type { NavSection } from './types'
import { NavOrderManager } from './navOrder'

// Mock the Wails IPC bindings.
vi.mock('../../../wailsjs/go/main/App.js', () => ({
  MovePage: vi.fn().mockResolvedValue(undefined),
  GetNavOrder: vi.fn().mockResolvedValue({}),
  SetNavOrder: vi.fn().mockResolvedValue(undefined)
}))

function makeSections(): NavSection[] {
  return [
    {
      name: 'Journal',
      pages: [
        { name: '2026-06-22', count: 0 },
        { name: '2026-06-21', count: 0 }
      ]
    },
    {
      name: 'Projects',
      pages: [
        { name: 'Roadmap', count: 0 },
        { name: 'Backlog', count: 0 }
      ]
    }
  ]
}

function makeDeps(overrides: Partial<DragDropDeps> = {}): DragDropDeps {
  const navOrder = new NavOrderManager({ onStateChange: () => {} })
  return {
    getActiveNotebook: () => 'Work',
    getActiveNotebookSections: () => makeSections(),
    navOrder,
    onDragItemChange: vi.fn(),
    onDropTargetChange: vi.fn(),
    onError: vi.fn(),
    onMoved: vi.fn().mockResolvedValue(undefined),
    onPageMoved: vi.fn(),
    ...overrides
  }
}

function makeDragEvent(overrides: Partial<DragEvent> = {}): DragEvent {
  return {
    preventDefault: vi.fn(),
    stopPropagation: vi.fn(),
    clientY: 100,
    currentTarget: {
      getBoundingClientRect: () => ({ top: 50, height: 100 })
    },
    dataTransfer: {
      effectAllowed: '',
      setData: vi.fn(),
      dropEffect: ''
    },
    ...overrides
  } as unknown as DragEvent
}

describe('DragDropManager', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('handleDragStart sets dragItem and dataTransfer', () => {
    const deps = makeDeps()
    const dnd = new DragDropManager(deps)
    const e = makeDragEvent()

    dnd.handleDragStart(e, 'section', 'Journal')

    expect(deps.onDragItemChange).toHaveBeenCalledWith({
      level: 'section',
      name: 'Journal',
      section: undefined
    })
    expect(e.dataTransfer!.effectAllowed).toBe('move')
    expect(e.dataTransfer!.setData).toHaveBeenCalledWith(
      'text/plain',
      'Journal'
    )
  })

  it('handleDragOver sets dropTarget for same-level', () => {
    const deps = makeDeps()
    const dnd = new DragDropManager(deps)
    const e = makeDragEvent()

    dnd.handleDragStart(e, 'section', 'Journal')
    dnd.handleDragOver(e, 'section', 'Projects')

    expect(deps.onDropTargetChange).toHaveBeenCalledWith(
      expect.objectContaining({ level: 'section', name: 'Projects' })
    )
  })

  it('handleDragOver rejects cross-level (except page→section)', () => {
    const deps = makeDeps()
    const dnd = new DragDropManager(deps)
    const e = makeDragEvent()

    dnd.handleDragStart(e, 'section', 'Journal')
    dnd.handleDragOver(e, 'page', '2026-06-22')

    // Should not call preventDefault for invalid cross-level.
    expect(e.preventDefault).not.toHaveBeenCalled()
  })

  it('handleDragOver allows page→section', () => {
    const deps = makeDeps()
    const dnd = new DragDropManager(deps)
    const e = makeDragEvent()

    dnd.handleDragStart(e, 'page', '2026-06-22', 'Journal')
    dnd.handleDragOver(e, 'section', 'Projects')

    expect(e.preventDefault).toHaveBeenCalled()
  })

  it('handleDrop reorders sections via navOrder', async () => {
    const { SetNavOrder } = await import('../../../wailsjs/go/main/App.js')
    const deps = makeDeps()
    const dnd = new DragDropManager(deps)
    const e = makeDragEvent()

    dnd.handleDragStart(e, 'section', 'Journal')
    // Set dropTarget before handleDrop.
    dnd.handleDragOver(e, 'section', 'Projects')
    await dnd.handleDrop(e, 'section', 'Projects', 'Work')

    // Section reorder persists via navOrder, not onMoved.
    expect(SetNavOrder).toHaveBeenCalled()
  })

  it('handleDrop page→section calls MovePage', async () => {
    const { MovePage } = await import('../../../wailsjs/go/main/App.js')
    const deps = makeDeps()
    const dnd = new DragDropManager(deps)
    const e = makeDragEvent()

    dnd.handleDragStart(e, 'page', '2026-06-22', 'Journal')
    dnd.handleDragOver(e, 'section', 'Projects')
    await dnd.handleDrop(e, 'section', 'Projects', 'Work', 'Projects')

    expect(MovePage).toHaveBeenCalledWith(
      'Work',
      'Journal',
      'Projects',
      '2026-06-22'
    )
    expect(deps.onMoved).toHaveBeenCalled()
    expect(deps.onPageMoved).toHaveBeenCalledWith(
      'Work',
      'Journal',
      'Projects',
      '2026-06-22'
    )
  })

  it('handleDrop same-section page→section is a no-op', async () => {
    const { MovePage } = await import('../../../wailsjs/go/main/App.js')
    const deps = makeDeps()
    const dnd = new DragDropManager(deps)
    const e = makeDragEvent()

    dnd.handleDragStart(e, 'page', '2026-06-22', 'Journal')
    dnd.handleDragOver(e, 'section', 'Journal')
    await dnd.handleDrop(e, 'section', 'Journal', 'Work', 'Journal')

    expect(MovePage).not.toHaveBeenCalled()
  })

  it('handleDrop page→__root__ moves the page out of its section', async () => {
    const { MovePage } = await import('../../../wailsjs/go/main/App.js')
    const deps = makeDeps()
    const dnd = new DragDropManager(deps)
    const e = makeDragEvent()

    dnd.handleDragStart(e, 'page', '2026-06-22', 'Journal')
    // Root dropzone calls handleDrop with targetName='__root__' + section='' (#177).
    await dnd.handleDrop(e, 'section', '__root__', 'Work', '')

    expect(MovePage).toHaveBeenCalledWith('Work', 'Journal', '', '2026-06-22')
    expect(deps.onPageMoved).toHaveBeenCalledWith(
      'Work',
      'Journal',
      '',
      '2026-06-22'
    )
  })

  it('handleDrop page→__root__ is a no-op when the page is already at root', async () => {
    const { MovePage } = await import('../../../wailsjs/go/main/App.js')
    const deps = makeDeps()
    const dnd = new DragDropManager(deps)
    const e = makeDragEvent()

    // dragItem.section === '' → page already at root.
    dnd.handleDragStart(e, 'page', '2026-06-22', '')
    await dnd.handleDrop(e, 'section', '__root__', 'Work', '')

    expect(MovePage).not.toHaveBeenCalled()
  })

  it('handleDragEnd clears state', () => {
    const deps = makeDeps()
    const dnd = new DragDropManager(deps)
    const e = makeDragEvent()

    dnd.handleDragStart(e, 'section', 'Journal')
    dnd.handleDragEnd()

    expect(deps.onDragItemChange).toHaveBeenCalledWith(null)
    expect(deps.onDropTargetChange).toHaveBeenCalledWith(null)
  })

  it('handleDrop no-op when no dragItem', async () => {
    const deps = makeDeps()
    const dnd = new DragDropManager(deps)
    const e = makeDragEvent()

    await dnd.handleDrop(e, 'section', 'Projects', 'Work')

    expect(deps.onMoved).not.toHaveBeenCalled()
  })
})
