import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/svelte'
import TabStrip from './TabStrip.svelte'
import type { TabEntry } from '../lib/tabs'

function mkTab(
  ref: { notebook: string; section: string; page: string },
  opts: { preview?: boolean; lastActivatedAt?: number; id?: string } = {}
): TabEntry {
  return {
    id: opts.id ?? `tab-${ref.page}`,
    notebook: ref.notebook,
    section: ref.section,
    page: ref.page,
    preview: opts.preview ?? false,
    lastActivatedAt: opts.lastActivatedAt ?? Date.now()
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
    onPromoteTab: vi.fn()
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
})
