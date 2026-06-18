import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest'
import { tick } from 'svelte'
import { render, screen, cleanup, fireEvent } from '@testing-library/svelte'

const mocks = vi.hoisted(() => ({
  listNavigation: vi.fn(),
  createNotebook: vi.fn(),
  createSection: vi.fn(),
  createPage: vi.fn(),
  pickNotebookFolder: vi.fn()
}))

vi.mock('../../../wailsjs/go/main/App.js', () => ({
  ListNavigation: mocks.listNavigation,
  CreateNotebook: mocks.createNotebook,
  CreateSection: mocks.createSection,
  CreatePage: mocks.createPage,
  PickNotebookFolder: mocks.pickNotebookFolder
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
    mocks.listNavigation.mockResolvedValue(NAV_TREE)
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

  it('renders the active-notebook label in text-primary (not accent) per #138', async () => {
    // The notebook-selector header label (Sidebar.svelte:680) used the accent
    // token, which masked theme switches on the 3 cool-accent themes (#138).
    // It now follows --text-primary so each theme's body-text hue shows up in
    // the sidebar. The "No Notebook" fallback only appears in this label, so
    // getByText uniquely targets it (independent of the nav tree load).
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
        onCloseVault: () => {}
      }
    })
    await flush()

    const label = screen.getByText('No Notebook')
    expect(label).toHaveClass('text-text-primary')
    expect(label).not.toHaveClass('text-accent-primary-start')
  })
})
