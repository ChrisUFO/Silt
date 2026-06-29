<script lang="ts">
  import { onMount, untrack } from 'svelte'
  import SidebarSection from './SidebarSection.svelte'
  import PluginSidebarPanels from './PluginSidebarPanels.svelte'
  import TagSidebarPanel from './TagSidebarPanel.svelte'
  import {
    ListNavigation,
    CreateNotebook,
    CreateSection,
    CreatePage,
    PickNotebookFolder,
    PickLinkedNotebook,
    UnlinkNotebook,
    RenamePage,
    RenameSection,
    RenameNotebook,
    DeletePage,
    DeleteSection,
    DeleteNotebook,
    MovePage
  } from '../../wailsjs/go/main/App.js'
  import { NavOrderManager, sortByName } from '../lib/sidebar/navOrder'
  import { DragDropManager } from '../lib/sidebar/useDragDrop'
  import type { NavNotebook, NavigationTree } from '../lib/sidebar/types'
  import {
    reconcileActive,
    generateUniquePageName as generateUniquePageNameHelper
  } from '../lib/sidebar/navTree'
  import {
    linkedNotebookId,
    isLinkedNotebook,
    deleteTargetLabel,
    reconcileActiveAfterDelete,
    findNotebook
  } from '../lib/sidebar/navActions'

  import type { PluginContext, PluginManifest } from '../plugins/sdk'
  import {
    getPluginSidebar,
    pluginIdForView
  } from '../plugins/getPluginSidebar'
  import { getSessionToken } from '../plugins/loader'
  import { makePluginContext } from '../plugins/context'
  import { loadedPlugins } from '../plugins/store.svelte'

  interface Props {
    activeNotebook: string
    activeSection: string
    activePage: string
    activeView: string
    selectedTag?: string
    collapsed: boolean
    sidebarWidth?: number
    sidebarDragging?: boolean
    onSelectNotebook: (notebook: string) => void
    onSelectSection: (section: string) => void
    onSelectPage: (notebook: string, section: string, page: string) => void
    onPinPage: (notebook: string, section: string, page: string) => void
    onSelectView: (view: string) => void
    onPageMoved?: (
      notebook: string,
      fromSection: string,
      toSection: string,
      page: string
    ) => void
  }

  let {
    activeNotebook = $bindable(),
    activeSection = $bindable(),
    activePage = $bindable(),
    activeView = $bindable(),
    selectedTag = $bindable(''),
    collapsed = $bindable(),
    sidebarWidth = 256,
    sidebarDragging = false,
    onSelectNotebook,
    onSelectSection,
    onSelectPage,
    onPinPage,
    onSelectView,
    onPageMoved
  }: Props = $props()

  // Resolve the active view's plugin sidebar (#321). Mirrors the lookup
  // PluginView.svelte does for the main view: read the live plugin entry
  // from the reactive store, build the context with the session token the
  // loader registered. The render branch uses the resolved `SidebarCmp`
  // and passes it `{ ctx, manifest }` — the same shape PluginView passes.
  //
  // Gating on `loadedPlugins.loadersReady` re-runs this derived when the
  // flag flips back to true AFTER vault:closing's clear→re-register cycle,
  // so getSessionToken(id) captures the FRESH token instead of an empty
  // one captured mid-teardown (#326 item 5).
  let pluginSidebarEntry = $derived(getPluginSidebar(activeView))
  let SidebarCmp = $derived(pluginSidebarEntry?.sidebarComponent)
  let pluginSidebarCtx: PluginContext | null = $derived.by(() => {
    if (!loadedPlugins.loadersReady) return null // suspend during vault switch
    const id = pluginIdForView(activeView)
    if (!id) return null
    return makePluginContext(id, getSessionToken(id))
  })
  let pluginSidebarManifest: PluginManifest | null = $derived(
    pluginSidebarEntry?.manifest ?? null
  )

  let tree = $state<NavigationTree>({ notebooks: [] })
  let showNotebookDropdown = $state(false)

  // Creation/rename modal state
  let createMode = $state<'' | 'notebook' | 'section' | 'page'>('')
  let editingMode = $state<'create' | 'rename'>('create')
  let renameCtx = $state<{
    level: 'notebook' | 'section' | 'page'
    notebook: string
    section?: string
    page?: string
  } | null>(null)
  let newName = $state('')
  let createError = $state('')
  let creating = $state(false)
  let modalInputEl = $state<HTMLInputElement | null>(null)

  // Context menu state (#62)
  let contextMenu = $state<{
    x: number
    y: number
    level: 'notebook' | 'section' | 'page'
    notebook: string
    section?: string
    page?: string
  } | null>(null)

  // Delete confirmation dialog state
  let deleteTarget = $state<{
    level: 'notebook' | 'section' | 'page'
    notebook: string
    section?: string
    page?: string
    label: string
  } | null>(null)

  // True when the delete target is a LINKED notebook. Deleting a linked
  // notebook UNLINKS it (files untouched) — the confirm copy must say so
  // rather than the vault-trash message (#100).
  let deleteTargetLinked = $derived(
    !!deleteTarget &&
      deleteTarget.level === 'notebook' &&
      isLinkedNotebook(findNotebook(tree, deleteTarget.notebook))
  )

  // Nav order for drag-to-reorder (#68). Explicit ordering from config.yaml;
  // items not in the map fall back to alphabetical.
  let navOrder = $state<{
    notebooks: string[]
    sections: Record<string, string[]>
    pages: Record<string, string[]>
  }>({
    notebooks: [],
    sections: {},
    pages: {}
  })
  let dragItem = $state<{
    level: string
    name: string
    section?: string
  } | null>(null)
  let dropTarget = $state<{
    level: string
    name: string
    before: boolean
  } | null>(null)
  let dndError = $state('')
  let dndErrorTimer: ReturnType<typeof setTimeout> | null = null

  function showDndError(msg: string) {
    dndError = msg
    if (dndErrorTimer) clearTimeout(dndErrorTimer)
    dndErrorTimer = setTimeout(() => {
      dndError = ''
      dndErrorTimer = null
    }, 4000)
  }

  const navOrderManager = new NavOrderManager({
    onStateChange: (s) => {
      navOrder = s
    }
  })

  async function loadNavOrder() {
    await navOrderManager.load()
  }

  const dnd = new DragDropManager({
    getActiveNotebook: () => activeNotebook,
    getActiveNotebookSections: () => activeNotebookObj?.sections ?? [],
    navOrder: navOrderManager,
    onDragItemChange: (item) => {
      dragItem = item
    },
    onDropTargetChange: (target) => {
      dropTarget = target
    },
    onError: showDndError,
    onMoved: async () => {
      await loadNavigation()
      await navOrderManager.load()
    },
    onPageMoved: (nb, from, to, page) => onPageMoved?.(nb, from, to, page)
  })

  function handleDragStart(
    e: DragEvent,
    level: string,
    name: string,
    section: string = ''
  ) {
    dnd.handleDragStart(e, level, name, section)
  }
  function handleDragOver(e: DragEvent, level: string, name: string) {
    dnd.handleDragOver(e, level, name)
  }
  function handleDragLeave() {
    dnd.handleDragLeave()
  }
  async function handleDrop(
    e: DragEvent,
    level: string,
    targetName: string,
    notebook: string = '',
    section: string = ''
  ) {
    await dnd.handleDrop(e, level, targetName, notebook, section)
  }
  function handleDragEnd() {
    dnd.handleDragEnd()
  }

  // Expanded section names (within the active notebook). The active section is
  // always expanded so the active path stays visible (spatial memory).
  let expandedSections = $state<Set<string>>(new Set())

  let activeNotebookObj = $derived(
    tree.notebooks.find((nb) => nb.name === activeNotebook)
  )

  // Sections sorted by nav_order (falling back to alphabetical) for #68.
  let sortedSections = $derived.by(() => {
    if (!activeNotebookObj) return []
    return sortByName(
      activeNotebookObj.sections,
      navOrder.sections[activeNotebook] ?? []
    )
  })

  // Sections are optional — a page can live directly under a notebook — so the
  // only persistent hint is "create/open a notebook"; section guidance is
  // hover-only on the buttons. A native title on a disabled button never shows,
  // so the wrapper span carries it.
  let sectionHint = $derived(
    activeNotebook ? 'New Section' : 'Create or open a Notebook first'
  )
  let pageHint = $derived(
    activeNotebook
      ? activeSection
        ? 'New Page in ' + activeSection
        : 'New Page (no section)'
      : 'Create or open a Notebook first'
  )
  let nextStep = $derived(
    !activeNotebook ? 'Create or open a Notebook to get started.' : ''
  )
  let hasNoContent = $derived(
    activeNotebookObj &&
      activeNotebookObj.sections.filter((s) => s.name !== '').length === 0 &&
      (activeNotebookObj.sections.find((s) => s.name === '')?.pages.length ??
        0) === 0
  )

  async function loadNavigation() {
    try {
      const data = await ListNavigation()
      if (!data) return
      tree = data
      const next = reconcileActive(tree, {
        notebook: activeNotebook,
        section: activeSection,
        page: activePage
      })
      if (next.notebook !== activeNotebook) {
        activeNotebook = next.notebook
        onSelectNotebook(activeNotebook)
      }
      if (next.section !== activeSection) activeSection = next.section
      if (next.page !== activePage) activePage = next.page
    } catch (e) {
      console.error('Failed to load navigation:', e)
    }
  }

  // Keep the active section expanded. Only `activeSection` drives this effect;
  // the expandedSections mutation runs under untrack so the write can't
  // re-trigger the effect (which previously caused an update-depth loop).
  $effect(() => {
    const sec = activeSection
    if (!sec) return
    untrack(() => {
      if (!expandedSections.has(sec)) {
        expandedSections = new Set(expandedSections).add(sec)
      }
    })
  })

  function toggleSection(name: string) {
    const next = new Set(expandedSections)
    if (next.has(name)) {
      next.delete(name)
    } else {
      next.add(name)
    }
    expandedSections = next
  }

  function handleSelectNotebook(nb: string) {
    activeNotebook = nb
    activeSection = ''
    activePage = ''
    showNotebookDropdown = false
    onSelectNotebook(nb)
    // Expand the first section if present, for orientation.
    const nbObj = tree.notebooks.find((n) => n.name === nb)
    if (nbObj && nbObj.sections.length > 0) {
      expandedSections = new Set([nbObj.sections[0].name])
    }
  }

  function handleSelectPage(section: string, page: string) {
    activeSection = section
    activePage = page
    onSelectPage(activeNotebook, section, page)
  }

  function handlePinPage(section: string, page: string) {
    activeSection = section
    activePage = page
    onPinPage(activeNotebook, section, page)
  }

  function openCreate(mode: 'notebook' | 'section') {
    createMode = mode
    editingMode = 'create'
    renameCtx = null
    newName = ''
    createError = ''
    setTimeout(() => modalInputEl?.focus(), 0)
  }

  function openRename(
    level: 'notebook' | 'section' | 'page',
    notebook: string,
    section: string | undefined,
    currentName: string,
    page?: string
  ) {
    createMode = level
    editingMode = 'rename'
    renameCtx = { level, notebook, section, page }
    newName = currentName
    createError = ''
    setTimeout(() => modalInputEl?.focus(), 0)
  }

  async function handleOpenNotebookFolder() {
    try {
      creating = true
      createError = ''
      const name = await PickNotebookFolder()
      if (!name) {
        // user cancelled
        showNotebookDropdown = false
        return
      }
      await loadNavigation()
      handleSelectNotebook(name)
      showNotebookDropdown = false
    } catch (e) {
      createError = e instanceof Error ? e.message : String(e)
      createMode = 'notebook'
      setTimeout(() => modalInputEl?.focus(), 0)
    } finally {
      creating = false
    }
  }

  // #100: link an external folder (e.g. a synced SharePoint mount) as a
  // notebook, edited in place. The folder is never copied into the vault.
  async function handleLinkExternalNotebook() {
    try {
      creating = true
      createError = ''
      const ln = await PickLinkedNotebook()
      if (!ln || !ln.id) {
        showNotebookDropdown = false
        return // user cancelled
      }
      await loadNavigation()
      handleSelectNotebook(ln.display_name)
      showNotebookDropdown = false
    } catch (e) {
      createError = e instanceof Error ? e.message : String(e)
      showNotebookDropdown = false
    } finally {
      creating = false
    }
  }

  async function handleCreate() {
    const trimmed = newName.trim()
    if (trimmed === '') return
    creating = true
    createError = ''
    try {
      if (editingMode === 'rename' && renameCtx) {
        if (renameCtx.level === 'notebook') {
          await RenameNotebook(renameCtx.notebook, trimmed)
          await loadNavigation()
          if (activeNotebook === renameCtx.notebook) {
            activeNotebook = trimmed
            handleSelectNotebook(trimmed)
          }
        } else if (renameCtx.level === 'section') {
          await RenameSection(
            renameCtx.notebook,
            renameCtx.section ?? '',
            trimmed
          )
          await loadNavigation()
          if (activeSection === renameCtx.section) {
            activeSection = trimmed
            onSelectSection(trimmed)
          }
        } else if (renameCtx.level === 'page') {
          await RenamePage(
            renameCtx.notebook,
            renameCtx.section ?? '',
            renameCtx.page ?? '',
            trimmed
          )
          await loadNavigation()
          if (activePage === renameCtx.page) {
            activePage = trimmed
          }
          window.dispatchEvent(new CustomEvent('page-renamed', { detail: { notebook: renameCtx.notebook, section: renameCtx.section, oldName: renameCtx.page, newName: trimmed } }))
        }
      } else if (createMode === 'notebook') {
        await CreateNotebook(trimmed)
        await loadNavigation()
        handleSelectNotebook(trimmed)
      } else if (createMode === 'section') {
        await CreateSection(activeNotebook, trimmed)
        await loadNavigation()
        activeSection = trimmed
        onSelectSection(trimmed)
        expandedSections = new Set([...expandedSections, trimmed])
      }
      createMode = ''
      newName = ''
      renameCtx = null
    } catch (e) {
      createError = e instanceof Error ? e.message : String(e)
    } finally {
      creating = false
    }
  }

  function handleModalKeydown(e: KeyboardEvent) {
    if (e.key === 'Enter') {
      e.preventDefault()
      e.stopPropagation()
      handleCreate()
    } else if (e.key === 'Escape') {
      e.preventDefault()
      e.stopPropagation()
      createMode = ''
    }
  }

  // --- Inline page creation (#83) ---
  // OneNote model: create "Untitled" immediately and navigate; the editor's
  // title field auto-focuses so the user can type the real name inline.
  async function handleCreatePageInline(sectionName: string) {
    creating = true
    try {
      const pageName = await generateUniquePageName(sectionName)
      await CreatePage(activeNotebook, sectionName, pageName, '')
      await loadNavigation()
      activeSection = sectionName
      activePage = pageName
      onSelectPage(activeNotebook, sectionName, pageName)
      activeView = 'notes'
      onSelectView('notes')
      // Signal the editor to focus the title for inline rename.
      setTimeout(() => {
        window.dispatchEvent(new CustomEvent('focus-page-title'))
      }, 100)
    } catch (e) {
      console.error('CreatePage inline failed:', e)
    } finally {
      creating = false
    }
  }

  async function generateUniquePageName(sectionName: string): Promise<string> {
    return generateUniquePageNameHelper(tree, activeNotebook, sectionName)
  }

  // --- Context menu handlers (#62) ---
  function openContextMenu(
    e: MouseEvent,
    level: 'notebook' | 'section' | 'page',
    notebook: string,
    section: string = '',
    page: string = ''
  ) {
    e.preventDefault()
    contextMenu = { x: e.clientX, y: e.clientY, level, notebook, section, page }
  }

  function closeContextMenu() {
    contextMenu = null
  }

  function handleContextRename() {
    if (!contextMenu) return
    const { level, notebook, section, page } = contextMenu
    contextMenu = null
    if (level === 'page' && section !== undefined && page !== undefined) {
      openRename('page', notebook, section, page, page)
    } else if (level === 'section' && section !== undefined) {
      openRename('section', notebook, section, section)
    } else if (level === 'notebook') {
      openRename('notebook', notebook, undefined, notebook)
    }
  }

  function handleContextDelete() {
    if (!contextMenu) return
    const { level, notebook, section, page } = contextMenu
    contextMenu = null
    deleteTarget = {
      level,
      notebook,
      section,
      page,
      label: deleteTargetLabel({ level, notebook, section, page })
    }
  }

  async function confirmDelete() {
    if (!deleteTarget) return
    const target = deleteTarget
    deleteTarget = null
    try {
      if (target.level === 'page' && target.page !== undefined) {
        await DeletePage(target.notebook, target.section ?? '', target.page)
      } else if (target.level === 'section' && target.section !== undefined) {
        await DeleteSection(target.notebook, target.section)
      } else if (target.level === 'notebook') {
        // #100: a linked notebook is UNLINKED (files untouched), not moved to
        // trash. Vault notebooks are deleted as before.
        const id = linkedNotebookId(findNotebook(tree, target.notebook))
        if (id !== null) {
          await UnlinkNotebook(id)
        } else {
          await DeleteNotebook(target.notebook)
        }
      }
      await loadNavigation()
      const next = reconcileActiveAfterDelete(target, {
        notebook: activeNotebook,
        section: activeSection,
        page: activePage
      })
      activeNotebook = next.notebook
      activeSection = next.section
      activePage = next.page
    } catch (e) {
      console.error('Delete failed:', e)
    }
  }

  function cancelDelete() {
    deleteTarget = null
  }

  onMount(() => {
    loadNavigation()
    loadNavOrder()
    const handleRefresh = () => {
      loadNavigation()
      loadNavOrder()
    }
    window.addEventListener('refresh-navigation', handleRefresh)
    return () => {
      window.removeEventListener('refresh-navigation', handleRefresh)
      if (dndErrorTimer) clearTimeout(dndErrorTimer)
    }
  })
</script>

<aside
  data-sidebar
  class="bg-surface border-r border-border-muted flex flex-col py-[4px] h-full flex-shrink-0 select-none z-40"
  style:width={collapsed ? '0px' : sidebarWidth + 'px'}
  style:transition={sidebarDragging ? 'none' : 'all 200ms ease-out'}
  style:overflow={collapsed ? 'hidden' : 'visible'}
  style:border-right={collapsed ? '0' : '1px solid var(--color-border-muted)'}
>
  <div
    class="px-3 py-3 flex flex-col gap-1 relative flex-1 overflow-hidden flex"
  >
    {#if activeView === 'tags'}
      <TagSidebarPanel bind:selectedTag />
    {:else if SidebarCmp && pluginSidebarCtx}
      <!-- Plugin-provided primary sidebar (#321). The active view's plugin
           owns the entire sidebar slot when it registers a sidebarComponent;
           the notebook selector + page tree are skipped because the plugin
           is responsible for any navigation affordance it wants to expose. -->
      <SidebarCmp ctx={pluginSidebarCtx} manifest={pluginSidebarManifest} />
    {:else}
      <!-- Notebook selector -->
      <div class="px-1 mb-3 relative">
        <div
          onclick={() => (showNotebookDropdown = !showNotebookDropdown)}
          onkeydown={(e) => {
            if (e.key === 'Enter' || e.key === ' ') {
              e.preventDefault()
              showNotebookDropdown = !showNotebookDropdown
            }
          }}
          class="flex items-center gap-2 cursor-pointer group py-1.5 rounded hover:bg-hover transition-colors"
          role="button"
          tabindex="0"
        >
          <span
            class="material-symbols-outlined text-accent-primary-start text-[20px]"
            >menu_book</span
          >
          <div class="flex flex-col min-w-0 flex-1">
            <span
              class="text-text-primary font-headline-md text-headline-md truncate"
              >{activeNotebook || 'No Notebook'}</span
            >
            <span
              class="text-text-muted text-[9px] uppercase tracking-widest font-label-sm-bold"
              >Active Notebook</span
            >
          </div>
          <span
            class="material-symbols-outlined text-text-muted text-[18px] group-hover:text-accent-primary-start transition-colors"
          >
            {showNotebookDropdown ? 'expand_less' : 'expand_more'}
          </span>
        </div>

        {#if showNotebookDropdown}
          <button
            tabindex="-1"
            aria-label="Close notebook menu"
            onclick={() => (showNotebookDropdown = false)}
            class="fixed inset-0 z-[60] cursor-default border-none bg-transparent p-0"
          ></button>
          <div
            class="absolute left-1 right-1 top-14 glass-palette border border-accent-primary-start/20 rounded-lg shadow-2xl z-[70] py-2 max-h-[60vh] overflow-y-auto custom-scrollbar"
            style="backdrop-filter: blur(16px); background: color-mix(in srgb, var(--color-panel) 92%, transparent);"
          >
            {#if tree.notebooks.length === 0}
              <div class="px-4 py-3 text-text-muted text-[12px] font-body-md">
                No notebooks yet.
              </div>
            {:else}
              {#each tree.notebooks as nb (nb.name)}
                <button
                  onclick={() => handleSelectNotebook(nb.name)}
                  class="flex items-center gap-3 px-4 py-2 w-full text-left cursor-pointer hover:bg-hover transition-colors font-body-md border-none bg-transparent"
                >
                  <span
                    class="material-symbols-outlined text-accent-primary-start text-[18px]"
                    >folder_special</span
                  >
                  <span
                    class="font-label-sm text-label-sm text-text-primary truncate flex-1"
                    >{nb.name}</span
                  >
                  {#if nb.source && nb.source !== 'vault'}
                    <span
                      class="material-symbols-outlined text-[14px] {nb.disconnected
                        ? 'text-status-warn'
                        : 'text-text-muted'}"
                      title={nb.disconnected
                        ? `Linked (offline): ${nb.root_path}`
                        : `Linked: ${nb.root_path}`}
                      aria-label={nb.disconnected
                        ? 'Linked notebook offline'
                        : 'Linked notebook'}
                      >{nb.disconnected ? 'cloud_off' : 'link'}</span
                    >
                  {/if}
                  {#if nb.name === activeNotebook}
                    <span
                      class="material-symbols-outlined text-accent-primary-start text-[16px]"
                      >check</span
                    >
                  {/if}
                </button>
              {/each}
            {/if}

            <div class="border-t border-border-muted mt-1 pt-1">
              <button
                onclick={() => {
                  showNotebookDropdown = false
                  openCreate('notebook')
                }}
                class="flex items-center gap-3 px-4 py-2 w-full text-left cursor-pointer hover:bg-hover transition-colors font-body-md border-none bg-transparent text-accent-primary-start"
              >
                <span class="material-symbols-outlined text-[18px]"
                  >create_new_folder</span
                >
                <span class="font-label-sm text-label-sm">New Notebook</span>
              </button>
              <button
                onclick={handleOpenNotebookFolder}
                disabled={creating}
                class="flex items-center gap-3 px-4 py-2 w-full text-left cursor-pointer hover:bg-hover transition-colors font-body-md border-none bg-transparent text-text-muted disabled:opacity-50"
              >
                <span class="material-symbols-outlined text-[18px]"
                  >folder_open</span
                >
                <span class="font-label-sm text-label-sm">Open Notebook…</span>
              </button>
              <button
                onclick={handleLinkExternalNotebook}
                disabled={creating}
                title="Link a folder that lives outside the vault (e.g. a synced SharePoint mount); it is edited in place, never copied in."
                class="flex items-center gap-3 px-4 py-2 w-full text-left cursor-pointer hover:bg-hover transition-colors font-body-md border-none bg-transparent text-text-muted disabled:opacity-50"
              >
                <span class="material-symbols-outlined text-[18px]"
                  >add_link</span
                >
                <span class="font-label-sm text-label-sm"
                  >Link External Folder…</span
                >
              </button>
            </div>
          </div>
        {/if}
      </div>

      <!-- Primary actions (icon-only, consistent style). Each button is wrapped
         in a span whose title gives the prerequisite reason — a native title
         on a disabled button doesn't show, but on the wrapper it does. -->
      <div
        class="px-1 flex items-stretch gap-0.5 mb-1 p-0.5 bg-panel border border-border-muted rounded-lg"
      >
        <span title={sectionHint} class="flex-1 flex">
          <button
            onclick={() => openCreate('section')}
            disabled={!activeNotebook}
            title={sectionHint}
            aria-label="New Section"
            class="w-full bg-transparent border-none text-text-muted hover:text-accent-primary-start hover:bg-hover disabled:opacity-40 disabled:hover:bg-transparent disabled:cursor-not-allowed py-1.5 rounded flex items-center justify-center transition-all cursor-pointer focus:outline-none"
          >
            <span class="material-symbols-outlined text-[20px]"
              >create_new_folder</span
            >
          </button>
        </span>
        <div class="w-px bg-border-muted my-1.5 flex-shrink-0"></div>
        <span title={pageHint} class="flex-1 flex">
          <button
            onclick={() => handleCreatePageInline(activeSection || '')}
            disabled={!activeNotebook}
            title={pageHint}
            aria-label="New Page"
            class="w-full bg-transparent border-none text-text-muted hover:text-accent-primary-start hover:bg-hover disabled:opacity-40 disabled:hover:bg-transparent disabled:cursor-not-allowed py-1.5 rounded flex items-center justify-center transition-all cursor-pointer focus:outline-none"
          >
            <span class="material-symbols-outlined text-[20px]">note_add</span>
          </button>
        </span>
        <div class="w-px bg-border-muted my-1.5 flex-shrink-0"></div>
        <span title="New page from template" class="flex-1 flex">
          <button
            onclick={() =>
              window.dispatchEvent(new CustomEvent('open-template-picker'))}
            disabled={!activeNotebook}
            title="New page from template (Ctrl+Shift+T)"
            aria-label="New Page from Template"
            class="w-full bg-transparent border-none text-text-muted hover:text-accent-primary-start hover:bg-hover disabled:opacity-40 disabled:hover:bg-transparent disabled:cursor-not-allowed py-1.5 rounded flex items-center justify-center transition-all cursor-pointer focus:outline-none"
          >
            <span class="material-symbols-outlined text-[20px]"
              >content_copy</span
            >
          </button>
        </span>
      </div>
      {#if nextStep}
        <div
          class="px-2 pb-2 text-[10px] text-text-muted font-label-sm flex items-center gap-1"
        >
          <span
            class="material-symbols-outlined text-[12px] text-accent-primary-start/70"
            >info</span
          >
          {nextStep}
        </div>
      {/if}

      <!-- Navigation tree -->
      <div class="flex-1 overflow-y-auto custom-scrollbar px-1">
        {#if !activeNotebookObj}
          <div
            class="text-text-muted py-10 text-center font-body-md text-[13px] border border-dashed border-border-muted rounded-lg mx-1"
          >
            {#if tree.notebooks.length === 0}
              No notebooks yet.<br />Create or open one to begin.
            {:else}
              Select a notebook.
            {/if}
          </div>
        {:else}
          {#if hasNoContent}
            <div
              class="text-text-muted py-6 text-center font-body-md text-[13px] border border-dashed border-border-muted rounded-lg mx-1"
            >
              No sections or pages yet.<br />Create one to get started.
            </div>
          {/if}
          {#each sortedSections.filter((s) => s.name !== '') as sec (sec.name)}
            <SidebarSection
              section={sec}
              depth={0}
              {activeNotebook}
              {activeSection}
              {activePage}
              {expandedSections}
              {navOrder}
              {dropTarget}
              {dragItem}
              onToggleSection={toggleSection}
              onSelectPage={handleSelectPage}
              onPinPage={handlePinPage}
              {onSelectSection}
              onCreatePageInline={handleCreatePageInline}
              onDragStart={handleDragStart}
              onDragOver={handleDragOver}
              onDragLeave={handleDragLeave}
              onDrop={handleDrop}
              onDragEnd={handleDragEnd}
              onContextMenu={openContextMenu}
            />
          {/each}

          <!-- Section-less root pages -->
          {#each sortedSections.filter((s) => s.name === '') as rootSec}
            {#if rootSec.pages.length > 0}
              <div class="h-px bg-border-muted my-2 mx-1 opacity-60"></div>
              <div
                class="px-2 py-1 text-[9px] uppercase tracking-wider text-text-muted/40 font-label-sm-bold"
              >
                Root Pages
              </div>
              {#each sortByName(rootSec.pages, navOrder.pages[`${activeNotebook}/`] ?? []) as pg (pg.name)}
                {@const isActive =
                  activeSection === '' && activePage === pg.name}
                <button
                  onclick={() => handleSelectPage('', pg.name)}
                  ondblclick={() => handlePinPage('', pg.name)}
                  onauxclick={(e) => {
                    if (e.button === 1) {
                      e.preventDefault()
                      handlePinPage('', pg.name)
                    }
                  }}
                  oncontextmenu={(e) =>
                    openContextMenu(e, 'page', activeNotebook, '', pg.name)}
                  draggable="true"
                  ondragstart={(e) => handleDragStart(e, 'page', pg.name, '')}
                  ondragover={(e) => handleDragOver(e, 'page', pg.name)}
                  ondragleave={handleDragLeave}
                  ondrop={(e) =>
                    handleDrop(e, 'page', pg.name, activeNotebook, '')}
                  ondragend={handleDragEnd}
                  class="relative w-full text-left pl-[28px] pr-2 py-1.5 rounded text-[13px] font-body-md transition-colors border-none bg-transparent cursor-pointer flex items-center gap-2"
                  class:bg-hover={isActive}
                  class:text-accent-primary-start={isActive}
                  class:text-text-muted={!isActive}
                  class:hover:text-text-primary={!isActive}
                  class:drag-over-top={dropTarget?.level === 'page' &&
                    dropTarget.name === pg.name &&
                    dropTarget.before}
                  class:drag-over-bottom={dropTarget?.level === 'page' &&
                    dropTarget.name === pg.name &&
                    !dropTarget.before}
                  role="treeitem"
                  aria-level="1"
                  aria-selected={isActive}
                >
                  {#if isActive}
                    <span
                      class="absolute left-1 top-1 bottom-1 w-[2px] bg-accent-primary-start rounded-full"
                    ></span>
                  {/if}
                  <span class="truncate flex-1" title={pg.name}>{pg.name}</span>
                </button>
              {/each}
            {/if}
          {/each}
          <!-- Notebook-root drop zone (#177): drag a page here to move it
             out of any section (section-less / root). Invisible until a
             page is actively dragged over it. -->
          <!-- svelte-ignore a11y_no_static_element_interactions -->
          <div
            class="mx-1 mt-1 rounded transition-colors min-h-[24px]"
            class:drag-over-into={dropTarget?.level === 'section' &&
              dropTarget.name === '__root__'}
            ondragover={(e) => {
              if (!dragItem || dragItem.level !== 'page') return
              e.preventDefault()
              if (e.dataTransfer) e.dataTransfer.dropEffect = 'move'
              dropTarget = { level: 'section', name: '__root__', before: false }
            }}
            ondragleave={handleDragLeave}
            ondrop={(e) =>
              handleDrop(e, 'section', '__root__', activeNotebook, '')}
            role="region"
            aria-label={dragItem?.level === 'page'
              ? 'Drop here to move page to notebook root'
              : undefined}
          >
            {#if dragItem?.level === 'page'}
              <div
                class="text-text-muted text-[11px] font-body-md py-1.5 px-2 text-center border border-dashed border-border-muted rounded"
              >
                Drop to move to notebook root
              </div>
            {/if}
          </div>
        {/if}
      </div>
    {/if}
  </div>

  <!-- DnD error toast (#177 collision / FS error). Perceivable without
       color via icon + text; aria-live so AT users hear the error. -->
  {#if dndError}
    <div
      class="fixed bottom-4 left-1/2 -translate-x-1/2 z-[200] flex items-center gap-2 px-4 py-2.5 rounded-lg shadow-2xl border border-status-danger/40 bg-panel"
      role="alert"
      aria-live="assertive"
    >
      <span
        class="material-symbols-outlined text-status-danger text-[18px]"
        aria-hidden="true">error</span
      >
      <span class="text-text-primary text-[13px] font-body-md">{dndError}</span>
    </div>
  {/if}

  <!-- Inline create/rename modal -->
  {#if createMode}
    <div
      class="fixed inset-0 bg-black/40 backdrop-blur-[2px] z-[160] flex items-start justify-center pt-32"
    >
      <button
        tabindex="-1"
        aria-label="Close dialog"
        onclick={() => (createMode = '')}
        class="absolute inset-0 cursor-default border-none bg-transparent p-0"
      ></button>
      <div
        role="dialog"
        aria-modal="true"
        aria-label={editingMode === 'rename'
          ? `Rename ${createMode}`
          : `New ${createMode}`}
        tabindex="-1"
        class="relative w-full max-w-md glass-palette border border-border-zinc rounded-xl shadow-2xl overflow-hidden"
        style="backdrop-filter: blur(16px) saturate(140%); background: color-mix(in srgb, var(--color-panel) 90%, transparent);"
      >
        <div class="px-5 py-4 border-b border-border-muted">
          <h2 class="font-headline-md text-headline-md text-text-primary">
            {editingMode === 'rename' ? 'Rename' : 'New'}
            {createMode === 'notebook' ? 'Notebook' : 'Section'}
          </h2>
          <p class="text-text-muted text-[12px] font-body-md mt-0.5">
            {#if createMode === 'notebook'}
              in this vault
            {:else if createMode === 'section'}
              in {activeNotebook}
            {:else if activeSection}
              {activeNotebook} › {activeSection}
            {:else}
              choose a section first
            {/if}
          </p>
        </div>
        <div class="px-5 py-4">
          <input
            bind:this={modalInputEl}
            bind:value={newName}
            onkeydown={handleModalKeydown}
            type="text"
            placeholder={editingMode === 'rename'
              ? createMode === 'notebook'
                ? 'New notebook name…'
                : 'New section name…'
              : createMode === 'notebook'
                ? 'Notebook name…'
                : 'Section name…'}
            class="w-full bg-surface border border-border-zinc rounded-lg px-3 py-2.5 text-text-primary text-[14px] font-body-md outline-none focus:border-accent-primary-start transition-colors"
          />
          {#if createError}
            <p class="text-error text-[12px] font-body-md mt-2">
              {createError}
            </p>
          {/if}
        </div>
        <div
          class="flex items-center justify-end gap-2 px-5 py-3 border-t border-border-muted"
        >
          <button
            onclick={() => (createMode = '')}
            class="px-4 py-2 rounded-lg text-text-muted hover:text-text-primary font-label-sm-bold transition-colors border-none bg-transparent cursor-pointer"
          >
            Cancel
          </button>
          <button
            onclick={handleCreate}
            disabled={creating || !newName.trim()}
            class="px-4 py-2 rounded-lg bg-accent-primary-start/20 border border-accent-primary-start/40 text-accent-primary-start font-label-sm-bold hover:brightness-110 transition-all cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {creating
              ? editingMode === 'rename'
                ? 'Renaming…'
                : 'Creating…'
              : editingMode === 'rename'
                ? 'Rename'
                : 'Create'}
          </button>
        </div>
      </div>
    </div>
  {/if}

  <!-- Sidebar Footer -->
  <div
    class="px-3 py-2 border-t border-border-muted flex items-center justify-between bg-surface flex-shrink-0"
  >
    <button
      onclick={() => (collapsed = true)}
      aria-label="Hide sidebar"
      title="Hide sidebar (Ctrl+B)"
      class="p-1.5 rounded hover:bg-hover text-text-muted hover:text-accent-primary-start transition-all duration-150 border-none bg-transparent cursor-pointer focus:outline-none flex items-center justify-center hover:scale-105 active:scale-95"
    >
      <span class="material-symbols-outlined text-[18px]">left_panel_close</span
      >
    </button>

    <!-- Plugin sidebar panels (#117) -->
    <PluginSidebarPanels />
  </div>
</aside>

<!-- Context menu (#62) -->
{#if contextMenu}
  <div class="fixed inset-0 z-[180]">
    <button
      tabindex="-1"
      aria-label="Close context menu"
      onclick={closeContextMenu}
      oncontextmenu={(e) => {
        e.preventDefault()
        closeContextMenu()
      }}
      class="absolute inset-0 cursor-default border-none bg-transparent p-0"
    ></button>
    <div
      class="fixed context-menu-card"
      style:left={contextMenu.x + 'px'}
      style:top={contextMenu.y + 'px'}
      role="menu"
      tabindex="-1"
      aria-label="Actions"
    >
      <button
        type="button"
        onclick={handleContextRename}
        role="menuitem"
        class="context-menu-item"
      >
        <span class="material-symbols-outlined text-[16px]">edit</span>
        Rename
      </button>
      <button
        type="button"
        onclick={handleContextDelete}
        role="menuitem"
        class="context-menu-item text-status-danger"
      >
        <span class="material-symbols-outlined text-[16px]">delete</span>
        Delete
      </button>
    </div>
  </div>
{/if}

<!-- Delete confirmation dialog (#62) -->
{#if deleteTarget}
  <div
    class="fixed inset-0 bg-black/40 backdrop-blur-[2px] z-[190] flex items-center justify-center"
  >
    <button
      tabindex="-1"
      aria-label="Cancel delete"
      onclick={cancelDelete}
      class="absolute inset-0 cursor-default border-none bg-transparent p-0"
    ></button>
    <div
      role="dialog"
      aria-modal="true"
      aria-label="Confirm delete"
      tabindex="-1"
      class="relative w-full max-w-sm glass-palette border border-border-zinc rounded-xl shadow-2xl overflow-hidden"
      style="backdrop-filter: blur(16px) saturate(140%); background: color-mix(in srgb, var(--color-panel) 90%, transparent);"
    >
      <div class="px-5 py-4 border-b border-border-muted">
        <h2 class="font-headline-md text-headline-md text-text-primary">
          {deleteTargetLinked
            ? 'Unlink Notebook?'
            : `Delete ${deleteTarget.level}?`}
        </h2>
        <p class="text-text-muted text-[12px] font-body-md mt-1">
          {#if deleteTargetLinked}
            Unlinking <strong>{deleteTarget.label}</strong> stops indexing it.
            Its files are left <strong>completely untouched</strong> — re-link the
            folder later to index it again.
          {:else}
            This will move the {deleteTarget.label} to
            <code>.system/trash/</code>. You can recover it from there manually.
          {/if}
        </p>
      </div>
      <div class="flex items-center justify-end gap-2 px-5 py-3">
        <button
          onclick={cancelDelete}
          class="px-4 py-2 rounded-lg text-text-muted hover:text-text-primary font-label-sm-bold transition-colors border-none bg-transparent cursor-pointer"
        >
          Cancel
        </button>
        <button
          onclick={confirmDelete}
          class="px-4 py-2 rounded-lg bg-status-danger/20 border border-status-danger/40 text-status-danger font-label-sm-bold hover:brightness-110 transition-all cursor-pointer"
        >
          {deleteTargetLinked ? 'Unlink' : 'Delete'}
        </button>
      </div>
    </div>
  </div>
{/if}

<style>
  .context-menu-card {
    background-color: color-mix(in srgb, var(--color-panel) 90%, transparent);
    backdrop-filter: blur(12px) saturate(140%);
    border: 1px solid var(--color-border-active);
    border-radius: 8px;
    box-shadow: 0 10px 25px -5px rgba(0, 0, 0, 0.5);
    padding: 4px;
    min-width: 160px;
    z-index: 181;
  }
  .context-menu-item {
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    padding: 8px 12px;
    border: none;
    background: transparent;
    color: var(--color-text-primary);
    font-size: 13px;
    font-family: var(--font-body, inherit);
    text-align: left;
    cursor: pointer;
    border-radius: 6px;
    transition: background-color 120ms ease-out;
  }
  .context-menu-item:hover {
    background-color: var(--color-hover);
  }
  :global(.drag-over-top) {
    box-shadow: inset 0 2px 0 var(--color-accent-primary-start);
  }
  :global(.drag-over-bottom) {
    box-shadow: inset 0 -2px 0 var(--color-accent-primary-start);
  }
  :global(.drag-over-into) {
    background: color-mix(
      in srgb,
      var(--color-accent-primary-start) 18%,
      transparent
    );
    box-shadow: inset 0 0 0 1px var(--color-accent-primary-start);
    border-radius: 6px;
  }
</style>
