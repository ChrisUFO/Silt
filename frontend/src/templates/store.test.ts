import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

// Mock the Wails-bound functions before importing the store.
vi.mock('../../wailsjs/go/main/App.js', () => ({
  ListTemplates: vi.fn(),
  GetTemplate: vi.fn(),
  RenderTemplate: vi.fn(),
  SaveUserTemplate: vi.fn(),
  DeleteUserTemplate: vi.fn(),
  ReloadTemplates: vi.fn(),
  CreatePageFromTemplate: vi.fn(),
  RenderTemplateBlocks: vi.fn()
}))

vi.mock('../../wailsjs/runtime/runtime.js', () => ({
  EventsOn: vi.fn()
}))

import {
  templatesState,
  loadTemplates,
  initTemplates,
  _resetForTests
} from './store.svelte'

// Import the mocked modules to configure their behavior.
import { ListTemplates } from '../../wailsjs/go/main/App.js'
import { EventsOn } from '../../wailsjs/runtime/runtime.js'

const mockListTemplates = vi.mocked(ListTemplates)
const mockEventsOn = vi.mocked(EventsOn)

describe('templates store', () => {
  beforeEach(() => {
    _resetForTests()
    vi.clearAllMocks()
  })

  afterEach(() => {
    _resetForTests()
  })

  it('loadTemplates populates templatesState.items', async () => {
    mockListTemplates.mockResolvedValue({
      templates: [
        { id: 'daily-note', title: 'Daily Note', category: 'daily', source: 'builtin' },
        { id: 'meeting-notes', title: 'Meeting Notes', category: 'meetings', source: 'builtin' }
      ],
      errors: [],
      warnings: []
    } as any)

    await loadTemplates()

    expect(templatesState.items.length).toBe(2)
    expect(templatesState.items[0].id).toBe('daily-note')
    expect(templatesState.loadError).toBeNull()
    expect(templatesState.loading).toBe(false)
  })

  it('loadTemplates surfaces errors', async () => {
    mockListTemplates.mockRejectedValue(new Error('IPC failed'))

    await loadTemplates()

    expect(templatesState.items.length).toBe(0)
    expect(templatesState.loadError).toBe('IPC failed')
    expect(templatesState.loading).toBe(false)
  })

  it('initTemplates is idempotent', () => {
    const dispose1 = initTemplates()
    const dispose2 = initTemplates()

    // Second call should be a no-op (returns a no-op disposer).
    expect(dispose2()).toBeUndefined()

    dispose1()
  })

  it('initTemplates subscribes to templates:changed', () => {
    const dispose = initTemplates()

    expect(mockEventsOn).toHaveBeenCalledWith('templates:changed', expect.any(Function))

    dispose()
  })

  it('initTemplates dispose unsubscribes', () => {
    const dispose = initTemplates()
    dispose()

    // Re-init should work after dispose.
    const dispose2 = initTemplates()
    expect(mockEventsOn).toHaveBeenCalledTimes(2)
    dispose2()
  })
})
