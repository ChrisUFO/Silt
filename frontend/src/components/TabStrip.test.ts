import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/svelte'
import TabStrip from './TabStrip.svelte'
import type { TabEntry } from '../lib/tabs'

function mkTab(
  ref: { notebook: string; section: string; page: string },
  opts: {
    preview?: boolean
    lastActivatedAt?: number
    id?: string
    dirty?: boolean
    saveError?: string | null
  } = {}
): TabEntry {
  return {
    id: opts.id ?? `tab-${ref.page}`,
    notebook: ref.notebook,
    section: ref.section,
    page: ref.page,
    preview: opts.preview ?? false,
    lastActivatedAt: opts.lastActivatedAt ?? Date.now(),
    viewMode: 'edit',
    dirty: opts.dirty,
    saveError: opts.saveError
  }
}

function defaultProps(
  overrides: {
    tabs?: TabEntry[]
    activeTabId?: string
  } = {}
) {
  return {
    tabs: overrides.tabs ?? [],
    activeTabId: overrides.activeTabId ?? '',
    onSelectTab: vi.fn(),
    onCloseTab: vi.fn(),
    onPromoteTab: vi.fn(),
    onReorderTab: vi.fn(),
    showDirtyIndicators: true
  }
}

describe('TabStrip (#142)', () => {
  beforeEach(() => vi.clearAllMocks())
  afterEach(() => cleanup())

  it('renders nothing when there are no tabs', () => {
    const props = defaultProps()
    render(TabStrip, { props })
    expect(screen.queryByRole('tablist')).toBeNull()
  })

  it('renders a tablist with a tab for each open tab', () => {
    const tabs = [
      mkTab({ notebook: 'Work', section: 'Projects', page: 'Site' }),
      mkTab({ notebook: 'Work', section: '', page: 'Top' })
    ]
    render(TabStrip, {
      props: defaultProps({ tabs, activeTabId: 'tab-Site' })
    })
    const tablist = screen.getByRole('tablist')
    expect(tablist).toBeTruthy()
    expect(tablist.getAttribute('aria-label')).toBe('Open pages')
    const tabButtons = screen.getAllByRole('tab')
    expect(tabButtons).toHaveLength(2)
  })

  it('marks the active tab with aria-selected=true', () => {
    const tabs = [
      mkTab({ notebook: 'Work', section: '', page: 'A' }),
      mkTab({ notebook: 'Work', section: '', page: 'B' })
    ]
    render(TabStrip, {
      props: defaultProps({ tabs, activeTabId: 'tab-B' })
    })
    const tabButtons = screen.getAllByRole('tab')
    const active = tabButtons.find((b) => b.textContent?.includes('B'))!
    const inactive = tabButtons.find((b) => b.textContent?.includes('A'))!
    expect(active.getAttribute('aria-selected')).toBe('true')
    expect(inactive.getAttribute('aria-selected')).toBe('false')
  })

  it('clicking a tab calls onSelectTab', async () => {
    const tabs = [
      mkTab({ notebook: 'Work', section: '', page: 'A' }),
      mkTab({ notebook: 'Work', section: '', page: 'B' })
    ]
    const props = defaultProps({ tabs, activeTabId: 'tab-A' })
    render(TabStrip, { props })
    const tabB = screen
      .getAllByRole('tab')
      .find((b) => b.textContent?.includes('B'))!
    await fireEvent.click(tabB)
    expect(props.onSelectTab).toHaveBeenCalledWith('tab-B')
  })

  it('clicking the close button calls onCloseTab', async () => {
    const tabs = [mkTab({ notebook: 'Work', section: '', page: 'A' })]
    const props = defaultProps({ tabs, activeTabId: 'tab-A' })
    render(TabStrip, { props })
    const closeBtn = screen.getByLabelText('Close tab')
    await fireEvent.click(closeBtn)
    expect(props.onCloseTab).toHaveBeenCalledWith('tab-A')
  })

  it('double-clicking a tab calls onPromoteTab', async () => {
    const tabs = [
      mkTab({ notebook: 'Work', section: '', page: 'A' }, { preview: true })
    ]
    const props = defaultProps({ tabs, activeTabId: 'tab-A' })
    render(TabStrip, { props })
    const tab = screen.getAllByRole('tab')[0]
    await fireEvent.dblClick(tab)
    expect(props.onPromoteTab).toHaveBeenCalledWith('tab-A')
  })

  it('middle-clicking a tab calls onCloseTab', async () => {
    const tabs = [
      mkTab({ notebook: 'Work', section: '', page: 'A' }),
      mkTab({ notebook: 'Work', section: '', page: 'B' })
    ]
    const props = defaultProps({ tabs, activeTabId: 'tab-A' })
    render(TabStrip, { props })
    const tab = screen.getAllByRole('tab')[0]
    tab.dispatchEvent(new MouseEvent('auxclick', { bubbles: true, button: 1 }))
    expect(props.onCloseTab).toHaveBeenCalledWith('tab-A')
  })

  it('ArrowRight moves focus to the next tab', async () => {
    const tabs = [
      mkTab({ notebook: 'Work', section: '', page: 'A' }),
      mkTab({ notebook: 'Work', section: '', page: 'B' }),
      mkTab({ notebook: 'Work', section: '', page: 'C' })
    ]
    const props = defaultProps({ tabs, activeTabId: 'tab-A' })
    render(TabStrip, { props })
    const tablist = screen.getByRole('tablist')
    const tabButtons = screen.getAllByRole('tab')
    // Start on tab A
    tabButtons[0].focus()
    expect(document.activeElement).toBe(tabButtons[0])
    // ArrowRight → tab B
    await fireEvent.keyDown(tablist, { key: 'ArrowRight' })
    expect(document.activeElement).toBe(tabButtons[1])
    // ArrowRight → tab C
    await fireEvent.keyDown(tablist, { key: 'ArrowRight' })
    expect(document.activeElement).toBe(tabButtons[2])
  })

  it('ArrowLeft wraps around to the last tab', async () => {
    const tabs = [
      mkTab({ notebook: 'Work', section: '', page: 'A' }),
      mkTab({ notebook: 'Work', section: '', page: 'B' })
    ]
    const props = defaultProps({ tabs, activeTabId: 'tab-A' })
    render(TabStrip, { props })
    const tablist = screen.getByRole('tablist')
    const tabButtons = screen.getAllByRole('tab')
    tabButtons[0].focus()
    await fireEvent.keyDown(tablist, { key: 'ArrowLeft' })
    expect(document.activeElement).toBe(tabButtons[1]) // wraps
  })

  it('Home/End jump to first/last tab', async () => {
    const tabs = [
      mkTab({ notebook: 'W', section: '', page: 'A' }),
      mkTab({ notebook: 'W', section: '', page: 'B' }),
      mkTab({ notebook: 'W', section: '', page: 'C' })
    ]
    const props = defaultProps({ tabs, activeTabId: 'tab-B' })
    render(TabStrip, { props })
    const tablist = screen.getByRole('tablist')
    const tabButtons = screen.getAllByRole('tab')
    tabButtons[1].focus()
    await fireEvent.keyDown(tablist, { key: 'Home' })
    expect(document.activeElement).toBe(tabButtons[0])
    await fireEvent.keyDown(tablist, { key: 'End' })
    expect(document.activeElement).toBe(tabButtons[2])
  })

  it('Enter/Space activates the focused tab', async () => {
    const tabs = [
      mkTab({ notebook: 'W', section: '', page: 'A' }),
      mkTab({ notebook: 'W', section: '', page: 'B' })
    ]
    const props = defaultProps({ tabs, activeTabId: 'tab-A' })
    render(TabStrip, { props })
    const tablist = screen.getByRole('tablist')
    const tabButtons = screen.getAllByRole('tab')
    tabButtons[1].focus()
    await fireEvent.keyDown(tablist, { key: 'Enter' })
    expect(props.onSelectTab).toHaveBeenCalledWith('tab-B')
  })

  it('Delete closes the focused tab', async () => {
    const tabs = [
      mkTab({ notebook: 'W', section: '', page: 'A' }),
      mkTab({ notebook: 'W', section: '', page: 'B' })
    ]
    const props = defaultProps({ tabs, activeTabId: 'tab-A' })
    render(TabStrip, { props })
    const tablist = screen.getByRole('tablist')
    await fireEvent.keyDown(tablist, { key: 'Delete' })
    expect(props.onCloseTab).toHaveBeenCalledWith('tab-A')
  })

  it('preview tabs have the preview class (italic)', () => {
    const tabs = [
      mkTab({ notebook: 'W', section: '', page: 'A' }, { preview: true })
    ]
    render(TabStrip, {
      props: defaultProps({ tabs, activeTabId: 'tab-A' })
    })
    const tab = screen.getAllByRole('tab')[0]
    expect(tab.className).toContain('preview')
  })

  it('pinned tabs do NOT have the preview class', () => {
    const tabs = [
      mkTab({ notebook: 'W', section: '', page: 'A' }, { preview: false })
    ]
    render(TabStrip, {
      props: defaultProps({ tabs, activeTabId: 'tab-A' })
    })
    const tab = screen.getAllByRole('tab')[0]
    expect(tab.className).not.toContain('preview')
  })

  it('tabs have aria-controls pointing to the tabpanel', () => {
    const tabs = [mkTab({ notebook: 'W', section: '', page: 'A' })]
    render(TabStrip, {
      props: defaultProps({ tabs, activeTabId: 'tab-A' })
    })
    const tab = screen.getAllByRole('tab')[0]
    expect(tab.getAttribute('aria-controls')).toBe('silt-tabpanel')
  })

  it('tabs have unique ids', () => {
    const tabs = [
      mkTab({ notebook: 'W', section: '', page: 'A' }),
      mkTab({ notebook: 'W', section: '', page: 'B' })
    ]
    render(TabStrip, {
      props: defaultProps({ tabs, activeTabId: 'tab-A' })
    })
    const [tab1, tab2] = screen.getAllByRole('tab')
    expect(tab1.id).toBeTruthy()
    expect(tab2.id).toBeTruthy()
    expect(tab1.id).not.toBe(tab2.id)
  })

  it('tab buttons are draggable (#175)', () => {
    const tabs = [mkTab({ notebook: 'W', section: '', page: 'A' })]
    render(TabStrip, {
      props: defaultProps({ tabs, activeTabId: 'tab-A' })
    })
    const tab = screen.getAllByRole('tab')[0]
    expect(tab.getAttribute('draggable')).toBe('true')
  })

  it('drop on another tab calls onReorderTab (#175)', async () => {
    const tabs = [
      mkTab({ notebook: 'W', section: '', page: 'A' }, { id: 'tab-A' }),
      mkTab({ notebook: 'W', section: '', page: 'B' }, { id: 'tab-B' })
    ]
    const props = defaultProps({ tabs, activeTabId: 'tab-A' })
    render(TabStrip, { props })
    const [tabA, tabB] = screen.getAllByRole('tab')

    // Mock getBoundingClientRect so before/after is deterministic: tab B
    // occupies [0,100], clientX=10 → before=true. jsdom lacks DragEvent, so
    // we use a MouseEvent with the dragover type.
    vi.spyOn(tabB, 'getBoundingClientRect').mockReturnValue({
      left: 0,
      top: 0,
      width: 100,
      height: 30,
      right: 100,
      bottom: 30,
      x: 0,
      y: 0,
      toJSON: () => ({})
    } as DOMRect)

    await fireEvent.dragStart(tabA)
    tabB.dispatchEvent(
      new MouseEvent('dragover', {
        bubbles: true,
        cancelable: true,
        clientX: 10
      })
    )
    await fireEvent.drop(tabB)

    expect(props.onReorderTab).toHaveBeenCalledTimes(1)
    // The before/after depends on mouse position (clientX=10 < width/2=50).
    expect(props.onReorderTab).toHaveBeenCalledWith('tab-A', 'tab-B', true)
  })

  it('dragging from the close button does not start a tab drag (#175)', async () => {
    const tabs = [
      mkTab({ notebook: 'W', section: '', page: 'A' }),
      mkTab({ notebook: 'W', section: '', page: 'B' })
    ]
    const props = defaultProps({ tabs, activeTabId: 'tab-A' })
    render(TabStrip, { props })
    // Two tabs → two close buttons; grab the first one.
    const closeSpans = screen.getAllByLabelText('Close tab')
    const tabB = screen.getAllByRole('tab')[1]

    // dragstart from the close span should be cancelled (preventDefault).
    // After that, a dragOver+drop on tab B should NOT trigger onReorderTab
    // because dragTabId was never set.
    await fireEvent.dragStart(closeSpans[0])
    await fireEvent.dragOver(tabB)
    await fireEvent.drop(tabB)

    expect(props.onReorderTab).not.toHaveBeenCalled()
  })

  it('keyboard navigation works after a reorder interaction (regression #175)', async () => {
    const tabs = [
      mkTab({ notebook: 'W', section: '', page: 'A' }),
      mkTab({ notebook: 'W', section: '', page: 'B' }),
      mkTab({ notebook: 'W', section: '', page: 'C' })
    ]
    const props = defaultProps({ tabs, activeTabId: 'tab-A' })
    render(TabStrip, { props })
    const tablist = screen.getByRole('tablist')
    const tabButtons = screen.getAllByRole('tab')

    // Focus tab A, ArrowRight to B, ArrowRight to C — keyboard nav is
    // unbroken by the DnD handlers.
    tabButtons[0].focus()
    await fireEvent.keyDown(tablist, { key: 'ArrowRight' })
    expect(document.activeElement).toBe(tabButtons[1])
    await fireEvent.keyDown(tablist, { key: 'ArrowRight' })
    expect(document.activeElement).toBe(tabButtons[2])

    // Home and Delete still work.
    await fireEvent.keyDown(tablist, { key: 'Home' })
    expect(document.activeElement).toBe(tabButtons[0])
    await fireEvent.keyDown(tablist, { key: 'Delete' })
    expect(props.onCloseTab).toHaveBeenCalledWith('tab-A')
  })

  it('dirty tab shows the dirty glyph (#167)', () => {
    const tabs = [
      mkTab({ notebook: 'W', section: '', page: 'A' }, { dirty: true })
    ]
    render(TabStrip, {
      props: defaultProps({ tabs, activeTabId: 'tab-A' })
    })
    const glyph = screen.getByText('circle')
    expect(glyph).toBeInTheDocument()
    expect(glyph.closest('.tab-save-state')?.classList.contains('dirty')).toBe(
      true
    )
  })

  it('save-failed tab shows the error glyph (#167)', () => {
    const tabs = [
      mkTab(
        { notebook: 'W', section: '', page: 'A' },
        { saveError: 'disk full' }
      )
    ]
    render(TabStrip, {
      props: defaultProps({ tabs, activeTabId: 'tab-A' })
    })
    const glyph = screen.getByText('error')
    expect(glyph).toBeInTheDocument()
    expect(glyph.closest('.tab-save-state')?.classList.contains('error')).toBe(
      true
    )
  })

  it('clean tab shows no save-state glyph (#167)', () => {
    const tabs = [mkTab({ notebook: 'W', section: '', page: 'A' })]
    render(TabStrip, {
      props: defaultProps({ tabs, activeTabId: 'tab-A' })
    })
    expect(screen.queryByText('circle')).toBeNull()
    expect(screen.queryByText('error')).toBeNull()
  })

  it('dirty glyph hidden when showDirtyIndicators is false (#167)', () => {
    const tabs = [
      mkTab({ notebook: 'W', section: '', page: 'A' }, { dirty: true })
    ]
    const props = defaultProps({ tabs, activeTabId: 'tab-A' })
    props.showDirtyIndicators = false
    render(TabStrip, { props })
    expect(screen.queryByText('circle')).toBeNull()
  })

  it('dirty tab tooltip includes unsaved edits hint (#167)', () => {
    const tabs = [
      mkTab({ notebook: 'W', section: '', page: 'A' }, { dirty: true })
    ]
    render(TabStrip, {
      props: defaultProps({ tabs, activeTabId: 'tab-A' })
    })
    const tab = screen.getAllByRole('tab')[0]
    expect(tab.getAttribute('title')).toContain('unsaved edits')
  })

  it('error tab tooltip includes save failed hint (#167)', () => {
    const tabs = [
      mkTab(
        { notebook: 'W', section: '', page: 'A' },
        { saveError: 'disk full' }
      )
    ]
    render(TabStrip, {
      props: defaultProps({ tabs, activeTabId: 'tab-A' })
    })
    const tab = screen.getAllByRole('tab')[0]
    expect(tab.getAttribute('title')).toContain('save failed')
  })
})
