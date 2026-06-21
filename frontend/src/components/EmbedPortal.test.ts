// Component-level test for EmbedPortal (#127). EmbedPortal renders a live
// view of a referenced block via {{embed:uuid}}. This test exercises the
// standalone component rendering: resolution via mocked IPC, the happy-path
// content render, the not-found state, and the block:changed live-refresh.
//
// The NodeView integration (EmbedNodeView → EmbedPortal inside a live editor)
// is covered by nodeview-test-harness.test.ts (Phase 1) and
// TipTapEditor.test.ts (this phase).

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import {
  render,
  screen,
  cleanup,
  fireEvent,
  waitFor
} from '@testing-library/svelte'
import { tick } from 'svelte'
import EmbedPortal from './EmbedPortal.svelte'

// --- IPC mocks (canonical vi.hoisted + vi.mock pattern) ---
const mocks = vi.hoisted(() => ({
  resolveBlockReference: vi.fn(),
  pluginMutateBlock: vi.fn(),
  eventsOn: vi.fn(() => () => {})
}))

vi.mock('../../wailsjs/go/main/App.js', () => ({
  ResolveBlockReference: mocks.resolveBlockReference,
  PluginMutateBlock: mocks.pluginMutateBlock
}))

vi.mock('../../wailsjs/runtime/runtime.js', () => ({
  EventsOn: mocks.eventsOn
}))

// Mock RichText with a proper Svelte stub component (RichText.stub.svelte)
// so the mock renders the text prop into the DOM. An async factory import
// avoids the circular dependency (RichText → BlockReferenceChip/EmbedPortal)
// and ensures vitest can resolve the compiled .svelte stub at mock time.
// (#127 review: the prior { props } return was not a valid Svelte 5
// component and silently rendered nothing.)
vi.mock('./RichText.svelte', async () => {
  const mod = await import('./RichText.stub.svelte')
  return { default: mod.default }
})

const FIXTURE_UUID = 'aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee'

describe('EmbedPortal (#127)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })
  afterEach(() => cleanup())

  it('shows a loading state while resolving the reference', async () => {
    let resolveRef: (v: unknown) => void = () => {}
    mocks.resolveBlockReference.mockReturnValue(
      new Promise((r) => {
        resolveRef = r
      })
    )
    render(EmbedPortal, { props: { uuid: FIXTURE_UUID } })
    expect(screen.getByText(/loading embed/i)).toBeTruthy()
    resolveRef({ exists: true, clean_text: 'resolved' })
    await tick()
  })

  it('renders the embed shell with breadcrumb on the happy path', async () => {
    mocks.resolveBlockReference.mockResolvedValue({
      exists: true,
      id: FIXTURE_UUID,
      notebook: 'Work',
      section: 'Projects',
      page: 'Site',
      file_date: '2026-06-15',
      clean_text: 'Hello from embedded block'
    })
    render(EmbedPortal, { props: { uuid: FIXTURE_UUID } })
    await waitFor(() => {
      expect(screen.getByText(/embed/i)).toBeTruthy()
    })
    // The embed header shows the breadcrumb.
    expect(screen.getByText(/Work/)).toBeTruthy()
    expect(screen.getByText(/Site/)).toBeTruthy()
  })

  it('shows a "not found" state when the reference does not exist', async () => {
    mocks.resolveBlockReference.mockResolvedValue({ exists: false })
    render(EmbedPortal, { props: { uuid: FIXTURE_UUID } })
    await waitFor(() => {
      expect(screen.getByTitle('Embedded block not found')).toBeTruthy()
    })
  })

  it('subscribes to block:changed for live refresh', async () => {
    mocks.resolveBlockReference.mockResolvedValue({
      exists: true,
      id: FIXTURE_UUID,
      notebook: 'Work',
      section: '',
      page: 'Top',
      file_date: '2026-06-15',
      clean_text: 'original content'
    })
    render(EmbedPortal, { props: { uuid: FIXTURE_UUID } })
    await tick()
    // EventsOn should have been called for block:changed subscription.
    expect(mocks.eventsOn).toHaveBeenCalledWith(
      'block:changed',
      expect.any(Function)
    )
  })

  it('persists edits via PluginMutateBlock (debounced)', async () => {
    vi.useFakeTimers()
    mocks.resolveBlockReference.mockResolvedValue({
      exists: true,
      id: FIXTURE_UUID,
      notebook: 'Work',
      section: '',
      page: 'Top',
      file_date: '2026-06-15',
      clean_text: 'editable content'
    })
    mocks.pluginMutateBlock.mockResolvedValue(undefined)
    render(EmbedPortal, { props: { uuid: FIXTURE_UUID } })
    await tick()
    // Find the contenteditable and simulate input.
    const editable = document.querySelector('[contenteditable="true"]')
    if (editable) {
      editable.textContent = 'edited content'
      await fireEvent.input(editable)
      // Advance past the 500ms debounce.
      vi.advanceTimersByTime(600)
      await vi.waitFor(() => {
        expect(mocks.pluginMutateBlock).toHaveBeenCalledWith(
          FIXTURE_UUID,
          expect.stringContaining('edited')
        )
      })
    }
    vi.useRealTimers()
  })
})
