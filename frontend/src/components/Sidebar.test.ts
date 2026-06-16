import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest'
import { tick } from 'svelte'
import { render, screen, cleanup, fireEvent } from '@testing-library/svelte'

const mocks = vi.hoisted(() => ({
  listNavigation: vi.fn(),
  createNotebook: vi.fn(),
  createSection: vi.fn(),
  createPage: vi.fn(),
  pickNotebookFolder: vi.fn(),
  renamePage: vi.fn(),
  renameSection: vi.fn(),
  renameNotebook: vi.fn(),
  deletePage: vi.fn(),
  deleteSection: vi.fn(),
  deleteNotebook: vi.fn(),
  getNavOrder: vi.fn(),
  setNavOrder: vi.fn()
}))

vi.mock('../../../wailsjs/go/main/App.js', () => ({
  ListNavigation: mocks.listNavigation,
  CreateNotebook: mocks.createNotebook,
  CreateSection: mocks.createSection,
  CreatePage: mocks.createPage,
  PickNotebookFolder: mocks.pickNotebookFolder,
  RenamePage: mocks.renamePage,
  RenameSection: mocks.renameSection,
  RenameNotebook: mocks.renameNotebook,
  DeletePage: mocks.deletePage,
  DeleteSection: mocks.deleteSection,
  DeleteNotebook: mocks.deleteNotebook,
  GetNavOrder: mocks.getNavOrder,
  SetNavOrder: mocks.setNavOrder
}))

import Sidebar from './Sidebar.svelte'

const NAV_TREE = {
  notebooks: [
    {
      name: 'Work',
      sections: [
        { name: 'Journal', pages: [{ name: 'Daily', count: 5 }] },
        { name: 'Meetings', pages: [{ name: 'Standup', count: 2 }] }
      ]
    },
    {
      name: 'Personal',
      sections: []
    }
  ]
}

async function flush() {
  await tick()
  await new Promise((r) => setTimeout(r, 0))
}

describe('Sidebar', () => {
  beforeEach(() => {
    mocks.listNavigation.mockReset()
    mocks.createNotebook.mockReset()
    mocks.createSection.mockReset()
    mocks.createPage.mockReset()
    mocks.pickNotebookFolder.mockReset()
    mocks.renamePage.mockReset()
    mocks.renameSection.mockReset()
    mocks.renameNotebook.mockReset()
    mocks.deletePage.mockReset()
    mocks.deleteSection.mockReset()
    mocks.deleteNotebook.mockReset()
    mocks.getNavOrder.mockReset()
    mocks.setNavOrder.mockReset()
    mocks.listNavigation.mockResolvedValue(NAV_TREE)
    mocks.getNavOrder.mockResolvedValue({ notebooks: [], sections: {}, pages: {} })
  })

  afterEach(() => {
    cleanup()
  })

  // Note: Sidebar's loadNavigation runs in onMount, which does not fire
  // reliably under Svelte 5 + testing-library/jsdom (unlike $effect, which
  // Kanban/Agenda/Calendar use successfully). The tree-render + auto-select
  // behaviour is covered by manual verification + the PluginView integration
  // test. The tests below cover the reliably-testable Sidebar interactions.

  it('collapses without crashing when collapsed=true', async () => {
    render(Sidebar, {
      props: {
        activeNotebook: 'Work',
        activeSection: '',
        activePage: '',
        activeView: 'notes',
        collapsed: true,
        onSelectNotebook: () => {},
        onSelectSection: () => {},
        onSelectPage: () => {},
        onSelectView: () => {},
        onCloseVault: () => {}
      }
    })
    await flush()

    // When collapsed, the sidebar renders but the titlebar/expand button is
    // handled by App.svelte. We just verify the component didn't crash.
    expect(document.body).toBeTruthy()
  })

  it('renders the Change Vault button which calls onCloseVault', async () => {
    const handler = vi.fn()
    render(Sidebar, {
      props: {
        activeNotebook: '',
        activeSection: '',
        activePage: '',
        activeView: 'notes',
        collapsed: false,
        onSelectNotebook: () => {},
        onSelectSection: () => {},
        onSelectPage: () => {},
        onSelectView: () => {},
        onCloseVault: handler
      }
    })
    await flush()

    const changeVaultBtn = screen.getByText(/change vault/i)
    expect(changeVaultBtn).toBeInTheDocument()
    await fireEvent.click(changeVaultBtn)
    expect(handler).toHaveBeenCalledTimes(1)
  })
})
