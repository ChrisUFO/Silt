// Component-level test for BlockReferenceChip (#127). The chip renders a
// clickable ((uuid)) reference that resolves the target block via IPC and
// dispatches navigate-to-block on click. This test exercises the standalone
// component: resolution, navigation dispatch, unresolved state, and the
// hover popover.

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import {
  render,
  screen,
  cleanup,
  fireEvent,
  waitFor
} from '@testing-library/svelte'
import { tick } from 'svelte'
import BlockReferenceChip from './BlockReferenceChip.svelte'

const mocks = vi.hoisted(() => ({
  resolveBlockReference: vi.fn()
}))

vi.mock('../../wailsjs/go/main/App.js', () => ({
  ResolveBlockReference: mocks.resolveBlockReference
}))

const FIXTURE_UUID = 'bbbbbbbb-cccc-4ddd-8eee-ffffffffffff'
const FIXTURE_UUID_SHORT = 'bbbbbbbb'

describe('BlockReferenceChip (#127)', () => {
  beforeEach(() => vi.clearAllMocks())
  afterEach(() => cleanup())

  it('shows a loading state while resolving', () => {
    mocks.resolveBlockReference.mockReturnValue(new Promise(() => {}))
    render(BlockReferenceChip, { props: { uuid: FIXTURE_UUID } })
    expect(screen.getByText(/\(\(/)).toBeTruthy()
  })

  it('renders the referenced block text on the happy path', async () => {
    mocks.resolveBlockReference.mockResolvedValue({
      exists: true,
      id: FIXTURE_UUID,
      notebook: 'Work',
      section: 'Projects',
      page: 'Site',
      file_date: '2026-06-15',
      clean_text: 'Important reference text'
    })
    render(BlockReferenceChip, { props: { uuid: FIXTURE_UUID } })
    await waitFor(() => {
      expect(screen.getByText('Important reference text')).toBeTruthy()
    })
  })

  it('dispatches navigate-to-block on click', async () => {
    mocks.resolveBlockReference.mockResolvedValue({
      exists: true,
      id: FIXTURE_UUID,
      notebook: 'Work',
      section: '',
      page: 'Top',
      file_date: '2026-06-15',
      clean_text: 'clickable'
    })
    const handler = vi.fn()
    window.addEventListener('navigate-to-block', handler)

    render(BlockReferenceChip, { props: { uuid: FIXTURE_UUID } })
    await waitFor(() => {
      expect(screen.getByText('clickable')).toBeTruthy()
    })

    const link = screen.getByRole('link')
    await fireEvent.click(link)
    expect(handler).toHaveBeenCalledTimes(1)
    const detail = handler.mock.calls[0][0].detail
    expect(detail.notebook).toBe('Work')
    expect(detail.page).toBe('Top')
    expect(detail.blockId).toBe(FIXTURE_UUID)

    window.removeEventListener('navigate-to-block', handler)
  })

  it('dispatches navigate-to-block on Enter key', async () => {
    mocks.resolveBlockReference.mockResolvedValue({
      exists: true,
      id: FIXTURE_UUID,
      notebook: 'Work',
      section: '',
      page: 'Top',
      file_date: '2026-06-15',
      clean_text: 'enterable'
    })
    const handler = vi.fn()
    window.addEventListener('navigate-to-block', handler)

    render(BlockReferenceChip, { props: { uuid: FIXTURE_UUID } })
    await waitFor(() => {
      expect(screen.getByText('enterable')).toBeTruthy()
    })

    const link = screen.getByRole('link')
    link.focus()
    await fireEvent.keyDown(link, { key: 'Enter' })
    expect(handler).toHaveBeenCalledTimes(1)

    window.removeEventListener('navigate-to-block', handler)
  })

  it('shows the unresolved state for a missing block', async () => {
    mocks.resolveBlockReference.mockResolvedValue({ exists: false })
    render(BlockReferenceChip, { props: { uuid: FIXTURE_UUID } })
    await waitFor(() => {
      const unresolved = screen.getByTitle('Unresolved block reference')
      expect(unresolved).toBeTruthy()
    })
  })

  it('has a tooltip with the breadcrumb path', async () => {
    mocks.resolveBlockReference.mockResolvedValue({
      exists: true,
      id: FIXTURE_UUID,
      notebook: 'Personal',
      section: 'Journal',
      page: 'Daily',
      file_date: '2026-06-15',
      clean_text: 'tooltip test'
    })
    render(BlockReferenceChip, { props: { uuid: FIXTURE_UUID } })
    await waitFor(() => {
      expect(screen.getByText('tooltip test')).toBeTruthy()
    })
    const link = screen.getByRole('link')
    expect(link.getAttribute('title')).toBe('Personal › Journal › Daily')
  })
})
