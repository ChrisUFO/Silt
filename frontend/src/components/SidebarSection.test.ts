import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/svelte'
import SidebarSection from './SidebarSection.svelte'

type NavSectionShape = {
  name: string
  path?: string
  pages: { name: string; count: number }[]
  children?: NavSectionShape[]
}

function makeProps(overrides: {
  section?: NavSectionShape
  depth?: number
  activeSection?: string
  expandedSections?: Set<string>
} = {}) {
  return {
    section: overrides.section ?? { name: 'Journal', path: 'Journal', pages: [{ name: 'Daily', count: 5 }] },
    depth: overrides.depth ?? 0,
    activeNotebook: 'Work',
    activeSection: overrides.activeSection ?? '',
    activePage: '',
    expandedSections: overrides.expandedSections ?? new Set<string>(),
    navOrder: { pages: {} as Record<string, string[]> },
    dropTarget: null,
    onToggleSection: vi.fn(),
    onSelectPage: vi.fn(),
    onSelectSection: vi.fn(),
    onCreatePageInline: vi.fn(),
    onDragStart: vi.fn(),
    onDragOver: vi.fn(),
    onDragLeave: vi.fn(),
    onDrop: vi.fn(),
    onDragEnd: vi.fn(),
    onContextMenu: vi.fn()
  }
}

describe('SidebarSection (#88 deep-nesting)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  afterEach(() => {
    cleanup()
  })

  it('renders a section header and its pages when expanded', () => {
    const props = makeProps({
      section: {
        name: 'Journal',
        pages: [
          { name: 'Daily', count: 5 },
          { name: 'Weekly', count: 2 }
        ]
      },
      expandedSections: new Set(['Journal'])
    })
    render(SidebarSection, { props })
    expect(screen.getByText('Journal')).toBeInTheDocument()
    expect(screen.getByText('Daily')).toBeInTheDocument()
    expect(screen.getByText('Weekly')).toBeInTheDocument()
  })

  it('does not render pages when not expanded', () => {
    const props = makeProps({
      section: { name: 'Journal', pages: [{ name: 'Daily', count: 5 }] },
      expandedSections: new Set<string>()
    })
    render(SidebarSection, { props })
    expect(screen.getByText('Journal')).toBeInTheDocument()
    expect(screen.queryByText('Daily')).not.toBeInTheDocument()
  })

  it('renders nested children recursively (#88)', () => {
    const deepSection: NavSectionShape = {
      name: 'Projects',
      path: 'Projects',
      pages: [],
      children: [
        {
          name: 'Active',
          path: 'Projects/Active',
          pages: [{ name: 'SiteLaunch', count: 3 }],
          children: [
            {
              name: 'Sub',
              path: 'Projects/Active/Sub',
              pages: [{ name: 'DeepPage', count: 1 }],
              children: []
            }
          ]
        }
      ]
    }
    const props = makeProps({
      section: deepSection,
      expandedSections: new Set(['Projects', 'Projects/Active', 'Projects/Active/Sub'])
    })
    render(SidebarSection, { props })
    expect(screen.getByText('Projects')).toBeInTheDocument()
    expect(screen.getByText('Active')).toBeInTheDocument()
    expect(screen.getByText('Sub')).toBeInTheDocument()
    expect(screen.getByText('DeepPage')).toBeInTheDocument()
  })

  it('toggles expansion on click', async () => {
    const onToggle = vi.fn()
    const props = makeProps({
      section: { name: 'Journal', path: 'Journal', pages: [{ name: 'Daily', count: 5 }] }
    })
    props.onToggleSection = onToggle
    render(SidebarSection, { props })
    const header = screen.getByRole('treeitem', { name: /Journal/ })
    await fireEvent.click(header)
    expect(onToggle).toHaveBeenCalledWith('Journal')
  })

  it('toggles expansion on Enter/Space key', async () => {
    const onToggle = vi.fn()
    const props = makeProps({
      section: { name: 'Journal', path: 'Journal', pages: [] }
    })
    props.onToggleSection = onToggle
    render(SidebarSection, { props })
    const header = screen.getByRole('treeitem', { name: /Journal/ })
    header.focus()
    await fireEvent.keyDown(header, { key: 'Enter' })
    expect(onToggle).toHaveBeenCalledWith('Journal')
    await fireEvent.keyDown(header, { key: ' ' })
    expect(onToggle).toHaveBeenCalledTimes(2)
  })

  it('reports aria-level for nested sections', () => {
    const props = makeProps({
      section: { name: 'Top', pages: [] },
      depth: 0
    })
    render(SidebarSection, { props })
    const top = screen.getByRole('treeitem', { name: /Top/ })
    expect(top).toHaveAttribute('aria-level', '1')

    cleanup()

    const props2 = makeProps({
      section: { name: 'Deep', pages: [] },
      depth: 2
    })
    render(SidebarSection, { props: props2 })
    const deep = screen.getByRole('treeitem', { name: /Deep/ })
    expect(deep).toHaveAttribute('aria-level', '3')
  })

  it('emits selectPage when a page is clicked', async () => {
    const onSelectPage = vi.fn()
    const props = makeProps({
      section: { name: 'Journal', path: 'Journal', pages: [{ name: 'Daily', count: 5 }] },
      expandedSections: new Set(['Journal'])
    })
    props.onSelectPage = onSelectPage
    render(SidebarSection, { props })
    await fireEvent.click(screen.getByText('Daily'))
    expect(onSelectPage).toHaveBeenCalledWith('Journal', 'Daily')
  })
})
