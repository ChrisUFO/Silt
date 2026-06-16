// Component-level coverage for the template picker (#55). Mirrors the
// AppearanceTab.test.ts pattern: hoisted mock state + vi.mock for the store
// and Wails IPC, then render + screen assertions.
import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/svelte'

const mocks = vi.hoisted(() => ({
  templatesState: {
    items: [
      {
        id: 'daily-note',
        title: 'Daily Note',
        description: 'A daily log.',
        category: 'daily',
        icon: 'today',
        source: 'builtin',
        placeholders: []
      },
      {
        id: 'meeting-notes',
        title: 'Meeting Notes',
        description: 'Structured meetings.',
        category: 'meetings',
        icon: 'group',
        source: 'builtin',
        placeholders: [{ name: 'meeting_title', description: 'Title', required: true }]
      }
    ],
    loadError: null as string | null,
    loading: false
  },
  templateStatus: { kind: 'info' as const, message: '' },
  loadTemplates: vi.fn(),
  clearTemplateStatus: vi.fn()
}))

vi.mock('../../wailsjs/go/main/App.js', () => ({
  ListTemplates: vi.fn(),
  GetTemplate: vi.fn(),
  RenderTemplate: vi.fn().mockResolvedValue('# Preview content'),
  SaveUserTemplate: vi.fn(),
  DeleteUserTemplate: vi.fn(),
  ReloadTemplates: vi.fn(),
  CreatePageFromTemplate: vi.fn().mockResolvedValue('2026-06-15'),
  RenderTemplateBlocks: vi.fn().mockResolvedValue([])
}))

vi.mock('../../wailsjs/runtime/runtime.js', () => ({
  EventsOn: vi.fn()
}))

vi.mock('./store.svelte', () => ({
  templatesState: mocks.templatesState,
  templateStatus: mocks.templateStatus,
  loadTemplates: mocks.loadTemplates,
  initTemplates: vi.fn(() => () => {}),
  setTemplateStatus: vi.fn(),
  clearTemplateStatus: mocks.clearTemplateStatus
}))

import TemplatePicker from './TemplatePicker.svelte'

describe('TemplatePicker (#55)', () => {
  beforeEach(() => {
    mocks.loadTemplates.mockReset()
    mocks.clearTemplateStatus.mockReset()
  })

  afterEach(() => {
    cleanup()
  })

  it('renders a dialog with template options grouped by category', () => {
    render(TemplatePicker, {
      props: { mode: 'insert', onClose: vi.fn(), onInsertBlocks: vi.fn() }
    })

    expect(screen.getByRole('dialog', { name: 'Template picker' })).toBeInTheDocument()

    const options = screen.getAllByRole('option')
    expect(options).toHaveLength(2)
    expect(screen.getByText('Daily Note')).toBeInTheDocument()
    expect(screen.getByText('Meeting Notes')).toBeInTheDocument()
  })

  it('shows the Insert button in insert mode', () => {
    render(TemplatePicker, {
      props: { mode: 'insert', onClose: vi.fn(), onInsertBlocks: vi.fn() }
    })

    expect(screen.getByText('Insert')).toBeInTheDocument()
  })

  it('shows the Create Page button + page-name field in new-page mode', () => {
    render(TemplatePicker, {
      props: {
        mode: 'new-page',
        notebook: 'Work',
        section: '',
        onClose: vi.fn(),
        onCreatedPage: vi.fn()
      }
    })

    expect(screen.getByText('Create Page')).toBeInTheDocument()
    expect(screen.getByLabelText('Page name')).toBeInTheDocument()
  })

  it('filters the list when searching', async () => {
    render(TemplatePicker, {
      props: { mode: 'insert', onClose: vi.fn(), onInsertBlocks: vi.fn() }
    })

    const search = screen.getByLabelText('Search templates')
    await fireEvent.input(search, { target: { value: 'meeting' } })

    expect(screen.getByText('Meeting Notes')).toBeInTheDocument()
    expect(screen.queryByText('Daily Note')).not.toBeInTheDocument()
  })

  it('renders the placeholder form when a template with placeholders is focused', () => {
    render(TemplatePicker, {
      props: { mode: 'insert', onClose: vi.fn(), onInsertBlocks: vi.fn() }
    })

    // Click the Meeting Notes option (it has a meeting_title placeholder).
    const meetingOption = screen.getByText('Meeting Notes')
    fireEvent.click(meetingOption)

    expect(screen.getByText('Placeholders')).toBeInTheDocument()
    expect(screen.getByText(/meeting_title/)).toBeInTheDocument()
  })

  it('shows the empty state when no templates match the search', async () => {
    render(TemplatePicker, {
      props: { mode: 'insert', onClose: vi.fn(), onInsertBlocks: vi.fn() }
    })

    const search = screen.getByLabelText('Search templates')
    await fireEvent.input(search, { target: { value: 'zzz-no-match' } })

    expect(screen.getByText('No templates match your search.')).toBeInTheDocument()
  })

  it('pre-fills the page-name field in new-page mode (#95)', () => {
    render(TemplatePicker, {
      props: {
        mode: 'new-page',
        notebook: 'Work',
        section: '',
        onClose: vi.fn(),
        onCreatedPage: vi.fn()
      }
    })

    const input = screen.getByLabelText('Page name') as HTMLInputElement
    expect(input.value).toMatch(/^Page \d{4}-\d{2}-\d{2}$/)
  })

  it('dispatches focus-page-title on successful CreatePageFromTemplate (#95)', async () => {
    const { CreatePageFromTemplate } = await import('../../wailsjs/go/main/App.js')
    ;(CreatePageFromTemplate as ReturnType<typeof vi.fn>).mockResolvedValue('2026-06-15')

    const onCreatedPage = vi.fn()
    const dispatchSpy = vi.spyOn(window, 'dispatchEvent')

    render(TemplatePicker, {
      props: {
        mode: 'new-page',
        notebook: 'Work',
        section: '',
        onClose: vi.fn(),
        onCreatedPage
      }
    })

    const input = screen.getByLabelText('Page name') as HTMLInputElement
    await fireEvent.input(input, { target: { value: 'Sprint Day' } })
    const createBtn = screen.getByText('Create Page')
    await fireEvent.click(createBtn)

    await vi.waitFor(() => {
      expect(CreatePageFromTemplate).toHaveBeenCalledWith(
        'Work',
        '',
        'Sprint Day',
        '',
        'daily-note',
        expect.any(Object)
      )
    })
    expect(dispatchSpy).toHaveBeenCalled()
    const event = dispatchSpy.mock.calls
      .map((c) => c[0] as Event)
      .find((e) => e.type === 'focus-page-title')
    expect(event).toBeDefined()
    expect(onCreatedPage).toHaveBeenCalledWith('Sprint Day')
    dispatchSpy.mockRestore()
  })
})
