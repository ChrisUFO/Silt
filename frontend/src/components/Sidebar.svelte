<script lang="ts">
  import { onMount, untrack } from 'svelte'
  import {
    ListNavigation,
    CreateNotebook,
    CreateSection,
    CreatePage,
    PickNotebookFolder,
    RenamePage,
    RenameSection,
    RenameNotebook,
    DeletePage,
    DeleteSection,
    DeleteNotebook,
    GetNavOrder,
    SetNavOrder
  } from '../../wailsjs/go/main/App.js'

  interface NavPage {
    name: string
    count: number
  }
  interface NavSection {
    name: string
    pages: NavPage[]
  }
  interface NavNotebook {
    name: string
    sections: NavSection[]
  }
  interface NavigationTree {
    notebooks: NavNotebook[]
  }

  interface Props {
    activeNotebook: string
    activeSection: string
    activePage: string
    activeView: string
    collapsed: boolean
    sidebarWidth?: number
    sidebarDragging?: boolean
    onSelectNotebook: (notebook: string) => void
    onSelectSection: (section: string) => void
    onSelectPage: (notebook: string, section: string, page: string) => void
    onSelectView: (view: string) => void
    onCloseVault?: () => void
  }

  let {
    activeNotebook = $bindable(),
    activeSection = $bindable(),
    activePage = $bindable(),
    activeView = $bindable(),
    collapsed = $bindable(),
    sidebarWidth = 256,
    sidebarDragging = false,
    onSelectNotebook,
    onSelectSection,
    onSelectPage,
    onSelectView,
    onCloseVault
  }: Props = $props()

  let tree = $state<NavigationTree>({ notebooks: [] })
  let showNotebookDropdown = $state(false)

  // Creation/rename modal state
  let createMode = $state<'' | 'notebook' | 'section'>('')
  let editingMode = $state<'create' | 'rename'>('create')
  let renameCtx = $state<{ level: 'notebook' | 'section'; notebook: string; section?: string } | null>(null)
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

  // Nav order for drag-to-reorder (#68). Explicit ordering from config.yaml;
  // items not in the map fall back to alphabetical.
  let navOrder = $state<{ notebooks: string[]; sections: Record<string, string[]>; pages: Record<string, string[]> }>({
    notebooks: [],
    sections: {},
    pages: {}
  })
  let dragItem = $state<{ level: string; name: string; section?: string } | null>(null)
  let dropTarget = $state<{ level: string; name: string; before: boolean } | null>(null)

  function sortByName<T extends { name: string }>(items: T[], order: string[] | undefined): T[] {
    if (!order || order.length === 0) return items
    const orderMap = new Map(order.map((n, i) => [n, i]))
    return [...items].sort((a, b) => {
      const ai = orderMap.has(a.name) ? orderMap.get(a.name)! : Infinity
      const bi = orderMap.has(b.name) ? orderMap.get(b.name)! : Infinity
      if (ai !== bi) return ai - bi
      return a.name.localeCompare(b.name)
    })
  }

  async function loadNavOrder() {
    try {
      const order = await GetNavOrder()
      navOrder = {
        notebooks: order.notebooks ?? [],
        sections: Object.fromEntries(Object.entries(order.sections ?? {})),
        pages: Object.fromEntries(Object.entries(order.pages ?? {}))
      }
    } catch {
      // Pre-vault or config not loaded — alphabetical fallback
    }
  }

  async function persistSectionOrder(notebook: string, sections: string[]) {
    navOrder.sections[notebook] = sections
    try {
      await SetNavOrder({ notebooks: navOrder.notebooks, sections: navOrder.sections, pages: navOrder.pages })
    } catch (e) {
      console.error('SetNavOrder failed:', e)
    }
  }

  async function persistPageOrder(sectionKey: string, pages: string[]) {
    navOrder.pages[sectionKey] = pages
    try {
      await SetNavOrder({ notebooks: navOrder.notebooks, sections: navOrder.sections, pages: navOrder.pages })
    } catch (e) {
      console.error('SetNavOrder failed:', e)
    }
  }

  // Drag-and-drop handlers (#68)
  function handleDragStart(e: DragEvent, level: string, name: string, section?: string) {
    dragItem = { level, name, section }
    if (e.dataTransfer) {
      e.dataTransfer.effectAllowed = 'move'
      e.dataTransfer.setData('text/plain', name)
    }
  }

  function handleDragOver(e: DragEvent, level: string, name: string) {
    if (!dragItem || dragItem.level !== level) return
    e.preventDefault()
    if (e.dataTransfer) e.dataTransfer.dropEffect = 'move'
    // Determine if dropping before or after based on mouse position
    const rect = (e.currentTarget as HTMLElement).getBoundingClientRect()
    const before = e.clientY < rect.top + rect.height / 2
    dropTarget = { level, name, before }
  }

  function handleDragLeave() {
    dropTarget = null
  }

  function handleDrop(e: DragEvent, level: string, targetName: string, notebook?: string, section?: string) {
    e.preventDefault()
    e.stopPropagation()
    if (!dragItem || dragItem.level !== level || dragItem.name === targetName) {
      dragItem = null
      dropTarget = null
      return
    }

    if (level === 'section' && notebook) {
      const sorted = sortByName(activeNotebookObj?.sections ?? [], navOrder.sections[notebook])
      const names = sorted.map((s) => s.name)
      const fromIdx = names.indexOf(dragItem.name)
      const toIdx = names.indexOf(targetName)
      if (fromIdx === -1 || toIdx === -1) return
      names.splice(fromIdx, 1)
      const insertAt = dropTarget?.before ? names.indexOf(targetName) : names.indexOf(targetName) + 1
      names.splice(insertAt, 0, dragItem.name)
      persistSectionOrder(notebook, names)
    } else if (level === 'page' && section) {
      const sec = activeNotebookObj?.sections.find((s) => s.name === section)
      const sectionKey = `${notebook ?? activeNotebook}/${section}`
      const sorted = sortByName(sec?.pages ?? [], navOrder.pages[sectionKey])
      const names = sorted.map((p) => p.name)
      const fromIdx = names.indexOf(dragItem.name)
      const toIdx = names.indexOf(targetName)
      if (fromIdx === -1 || toIdx === -1) return
      names.splice(fromIdx, 1)
      const insertAt = dropTarget?.before ? names.indexOf(targetName) : names.indexOf(targetName) + 1
      names.splice(insertAt, 0, dragItem.name)
      persistPageOrder(sectionKey, names)
    }

    dragItem = null
    dropTarget = null
  }

  function handleDragEnd() {
    dragItem = null
    dropTarget = null
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
    return sortByName(activeNotebookObj.sections, navOrder.sections[activeNotebook] ?? [])
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

  async function loadNavigation() {
    try {
      const data = await ListNavigation()
      if (data) {
        tree = data
        // Pick a sensible active notebook if none selected.
        if (tree.notebooks.length > 0) {
          if (
            !activeNotebook ||
            !tree.notebooks.some((nb) => nb.name === activeNotebook)
          ) {
            activeNotebook = tree.notebooks[0].name
            onSelectNotebook(activeNotebook)
          }
          // Ensure active section/page still exist; clear if not.
          const nb = tree.notebooks.find((n) => n.name === activeNotebook)
          if (nb) {
            if (
              activeSection &&
              !nb.sections.some((s) => s.name === activeSection)
            ) {
              activeSection = ''
            }
          } else {
            activeSection = ''
            activePage = ''
          }
        }
      }
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

  function openCreate(mode: 'notebook' | 'section') {
    createMode = mode
    editingMode = 'create'
    renameCtx = null
    newName = ''
    createError = ''
    setTimeout(() => modalInputEl?.focus(), 0)
  }

  function openRename(level: 'notebook' | 'section', notebook: string, section: string | undefined, currentName: string) {
    createMode = level
    editingMode = 'rename'
    renameCtx = { level, notebook, section }
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
          await RenameSection(renameCtx.notebook, renameCtx.section ?? '', trimmed)
          await loadNavigation()
          if (activeSection === renameCtx.section) {
            activeSection = trimmed
            onSelectSection(trimmed)
          }
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
    const base = 'Untitled'
    const nb = tree.notebooks.find((n) => n.name === activeNotebook)
    if (!nb) return base
    const sec = nb.sections.find((s) => s.name === sectionName)
    if (!sec) return base
    const existing = new Set(sec.pages.map((p) => p.name))
    if (!existing.has(base)) return base
    let i = 2
    while (existing.has(`${base} ${i}`)) i++
    return `${base} ${i}`
  }

  // --- Context menu handlers (#62) ---
  function openContextMenu(
    e: MouseEvent,
    level: 'notebook' | 'section' | 'page',
    notebook: string,
    section?: string,
    page?: string
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
      activeSection = section
      activePage = page
      onSelectPage(notebook, section, page)
      activeView = 'notes'
      onSelectView('notes')
      setTimeout(() => {
        window.dispatchEvent(new CustomEvent('focus-page-title'))
      }, 100)
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
    let label = ''
    if (level === 'page' && page) label = `page "${page}"`
    else if (level === 'section' && section) label = `section "${section}" and all its pages`
    else if (level === 'notebook') label = `notebook "${notebook}" and all its content`
    deleteTarget = { level, notebook, section, page, label }
  }

  async function confirmDelete() {
    if (!deleteTarget) return
    const { level, notebook, section, page } = deleteTarget
    deleteTarget = null
    try {
      if (level === 'page' && page !== undefined) {
        await DeletePage(notebook, section ?? '', page)
      } else if (level === 'section' && section !== undefined) {
        await DeleteSection(notebook, section)
      } else if (level === 'notebook') {
        await DeleteNotebook(notebook)
      }
      await loadNavigation()
      // Navigate away if the active item was deleted.
      if (level === 'notebook' && activeNotebook === notebook) {
        activeNotebook = ''
        activeSection = ''
        activePage = ''
      } else if (level === 'section' && activeSection === section) {
        activeSection = ''
        activePage = ''
      } else if (level === 'page' && activePage === page) {
        activePage = ''
      }
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
    }
  })
</script>

<aside
  class="bg-bg-surface border-r border-border-muted flex flex-col py-[4px] h-full flex-shrink-0 select-none z-40"
  style:width={collapsed ? '0px' : sidebarWidth + 'px'}
  style:transition={sidebarDragging ? 'none' : 'all 200ms ease-out'}
  style:overflow={collapsed ? 'hidden' : 'visible'}
  style:border-right={collapsed ? '0' : '1px solid var(--border-muted)'}
>
  <div
    class="px-3 py-3 flex flex-col gap-1 relative flex-1 overflow-hidden flex"
  >
    <!-- Notebook selector -->
    <div class="px-1 mb-3 relative">
      <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
      <div
        onclick={() => (showNotebookDropdown = !showNotebookDropdown)}
        class="flex items-center gap-2 cursor-pointer group py-1.5 rounded hover:bg-bg-hover transition-colors"
        role="button"
        tabindex="0"
      >
        <span
          class="material-symbols-outlined text-accent-primary-start text-[20px]"
          >menu_book</span
        >
        <div class="flex flex-col min-w-0 flex-1">
          <span
            class="text-accent-primary-start font-headline-md text-headline-md truncate"
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
        <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
        <div
          onclick={() => (showNotebookDropdown = false)}
          class="fixed inset-0 z-[60]"
        ></div>
        <div
          class="absolute left-1 right-1 top-14 glass-palette border border-accent-primary-start/20 rounded-lg shadow-2xl z-[70] py-2 max-h-[60vh] overflow-y-auto custom-scrollbar"
          style="backdrop-filter: blur(16px); background: color-mix(in srgb, var(--bg-panel) 92%, transparent);"
        >
          {#if tree.notebooks.length === 0}
            <div class="px-4 py-3 text-text-muted text-[12px] font-body-md">
              No notebooks yet.
            </div>
          {:else}
            {#each tree.notebooks as nb (nb.name)}
              <button
                onclick={() => handleSelectNotebook(nb.name)}
                class="flex items-center gap-3 px-4 py-2 w-full text-left cursor-pointer hover:bg-bg-hover transition-colors font-body-md border-none bg-transparent"
              >
                <span
                  class="material-symbols-outlined text-accent-primary-start text-[18px]"
                  >folder_special</span
                >
                <span
                  class="font-label-sm text-label-sm text-text-primary truncate flex-1"
                  >{nb.name}</span
                >
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
              class="flex items-center gap-3 px-4 py-2 w-full text-left cursor-pointer hover:bg-bg-hover transition-colors font-body-md border-none bg-transparent text-accent-primary-start"
            >
              <span class="material-symbols-outlined text-[18px]"
                >create_new_folder</span
              >
              <span class="font-label-sm text-label-sm">New Notebook</span>
            </button>
            <button
              onclick={handleOpenNotebookFolder}
              disabled={creating}
              class="flex items-center gap-3 px-4 py-2 w-full text-left cursor-pointer hover:bg-bg-hover transition-colors font-body-md border-none bg-transparent text-text-muted disabled:opacity-50"
            >
              <span class="material-symbols-outlined text-[18px]"
                >folder_open</span
              >
              <span class="font-label-sm text-label-sm">Open Notebook…</span>
            </button>
          </div>
        </div>
      {/if}
    </div>

    <!-- Primary actions (icon-only, consistent style). Each button is wrapped
         in a span whose title gives the prerequisite reason — a native title
         on a disabled button doesn't show, but on the wrapper it does. -->
    <div class="px-1 flex items-center gap-1.5 mb-1">
      <span title={sectionHint} class="flex-1 flex">
        <button
          onclick={() => openCreate('section')}
          disabled={!activeNotebook}
          title={sectionHint}
          aria-label="New Section"
          class="w-full bg-accent-primary-glow border border-accent-primary-start/30 text-accent-primary-start font-label-sm-bold py-2 rounded flex items-center justify-center hover:brightness-110 hover:border-accent-primary-start transition-all cursor-pointer focus:outline-none disabled:opacity-40 disabled:cursor-not-allowed"
        >
          <span class="material-symbols-outlined text-[20px]"
            >create_new_folder</span
          >
        </button>
      </span>
      <span title={pageHint} class="flex-1 flex">
        <button
          onclick={() => handleCreatePageInline(activeSection || '')}
          disabled={!activeNotebook}
          title={pageHint}
          aria-label="New Page"
          class="w-full bg-bg-panel border border-border-muted text-text-muted hover:text-accent-primary-start hover:border-accent-primary-start/40 font-label-sm-bold py-2 rounded flex items-center justify-center transition-all cursor-pointer focus:outline-none disabled:opacity-40 disabled:cursor-not-allowed"
        >
          <span class="material-symbols-outlined text-[20px]">note_add</span>
        </button>
      </span>
      <span title="New page from template" class="flex items-center">
        <button
          onclick={() => window.dispatchEvent(new CustomEvent('open-template-picker'))}
          disabled={!activeNotebook}
          title="New page from template (Ctrl+Shift+T)"
          aria-label="New Page from Template"
          class="w-9 bg-bg-panel border border-border-muted text-text-muted hover:text-accent-primary-start hover:border-accent-primary-start/40 font-label-sm-bold py-2 rounded flex items-center justify-center transition-all cursor-pointer focus:outline-none disabled:opacity-40 disabled:cursor-not-allowed"
        >
          <span class="material-symbols-outlined text-[20px]">content_copy</span>
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
      {:else if activeNotebookObj.sections.length === 0}
        <div
          class="text-text-muted py-10 text-center font-body-md text-[13px] border border-dashed border-border-muted rounded-lg mx-1"
        >
          No sections yet.<br />Create a section to add pages.
        </div>
      {:else}
        {#each sortedSections as sec (sec.name)}
          {@const isExpanded = expandedSections.has(sec.name)}
          <div class="mb-0.5">
            <!-- Section header -->
            <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
            <div
              class="group flex items-center gap-1 px-2 py-1.5 cursor-pointer rounded hover:bg-bg-hover transition-colors"
              class:drag-over-top={dropTarget?.level === 'section' && dropTarget.name === sec.name && dropTarget.before}
              class:drag-over-bottom={dropTarget?.level === 'section' && dropTarget.name === sec.name && !dropTarget.before}
              draggable="true"
              ondragstart={(e) => handleDragStart(e, 'section', sec.name)}
              ondragover={(e) => handleDragOver(e, 'section', sec.name)}
              ondragleave={handleDragLeave}
              ondrop={(e) => handleDrop(e, 'section', sec.name, activeNotebook)}
              ondragend={handleDragEnd}
              onclick={() => toggleSection(sec.name)}
              oncontextmenu={(e) => openContextMenu(e, 'section', activeNotebook, sec.name)}
              role="treeitem"
              tabindex="0"
              aria-expanded={isExpanded}
            >
              <span
                class="material-symbols-outlined text-text-muted text-[16px] transition-transform"
                class:rotate-90={isExpanded}
              >
                chevron_right
              </span>
              <span
                class="material-symbols-outlined text-text-muted text-[17px]"
                >{sec.name ? 'folder' : 'drafts'}</span
              >
              <span
                class="font-label-sm-bold text-label-sm-bold uppercase tracking-wider text-text-primary truncate flex-1"
                >{sec.name ? sec.name : 'Pages (no section)'}</span
              >
              <span
                class="text-[9px] font-label-sm text-text-muted bg-bg-panel border border-border-muted rounded-full px-1.5 py-0.5"
                >{sec.pages.length}</span
              >
              <button
                onclick={(e) => {
                  e.stopPropagation()
                  activeSection = sec.name
                  onSelectSection(sec.name)
                  handleCreatePageInline(sec.name)
                }}
                title="New page in this section"
                class="opacity-0 group-hover:opacity-100 text-text-muted hover:text-accent-primary-start border-none bg-transparent cursor-pointer p-0.5 rounded transition-all"
              >
                <span class="material-symbols-outlined text-[16px]">add</span>
              </button>
            </div>

            <!-- Pages -->
            {#if isExpanded}
              <div class="ml-4 border-l border-border-muted pl-1 mt-0.5 mb-1.5">
                {#if sec.pages.length === 0}
                  <div
                    class="text-text-muted text-[11px] font-body-md py-1.5 px-2 italic"
                  >
                    No pages. Click + to add one.
                  </div>
                {:else}
                  {#each sortByName(sec.pages, navOrder.pages[`${activeNotebook}/${sec.name}`] ?? []) as pg (pg.name)}
                    {@const isActive =
                      activeSection === sec.name && activePage === pg.name}
                    <button
                      onclick={() => handleSelectPage(sec.name, pg.name)}
                      oncontextmenu={(e) => openContextMenu(e, 'page', activeNotebook, sec.name, pg.name)}
                      draggable="true"
                      ondragstart={(e) => handleDragStart(e, 'page', pg.name, sec.name)}
                      ondragover={(e) => handleDragOver(e, 'page', pg.name)}
                      ondragleave={handleDragLeave}
                      ondrop={(e) => handleDrop(e, 'page', pg.name, activeNotebook, sec.name)}
                      ondragend={handleDragEnd}
                      class="relative w-full text-left pl-4 pr-2 py-1.5 rounded text-[13px] font-body-md transition-colors border-none bg-transparent cursor-pointer flex items-center gap-2"
                      class:bg-bg-hover={isActive}
                      class:text-accent-primary-start={isActive}
                      class:text-text-muted={!isActive}
                      class:hover:text-text-primary={!isActive}
                      role="treeitem"
                    >
                      {#if isActive}
                        <span
                          class="absolute left-0 top-1 bottom-1 w-[2px] bg-accent-primary-start rounded-full"
                        ></span>
                      {/if}
                      <span class="material-symbols-outlined text-[15px]"
                        >article</span
                      >
                      <span class="truncate flex-1" title={pg.name}
                        >{pg.name}</span
                      >
                    </button>
                  {/each}
                {/if}
              </div>
            {/if}
          </div>
        {/each}
      {/if}
    </div>
  </div>

  <!-- Inline create modal -->
  {#if createMode}
    <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
    <div
      onclick={() => (createMode = '')}
      class="fixed inset-0 bg-black/60 backdrop-blur-sm z-[160] flex items-start justify-center pt-32"
    >
      <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
      <div
        onclick={(e) => e.stopPropagation()}
        class="w-full max-w-md glass-palette border border-border-zinc rounded-xl shadow-2xl overflow-hidden"
        style="backdrop-filter: blur(16px) saturate(140%); background: color-mix(in srgb, var(--bg-panel) 90%, transparent);"
      >
        <div class="px-5 py-4 border-b border-border-muted">
          <h2 class="font-headline-md text-headline-md text-text-primary">
            {editingMode === 'rename' ? 'Rename' : 'New'} {createMode === 'notebook'
              ? 'Notebook'
              : 'Section'}
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
            class="w-full bg-bg-surface border border-border-zinc rounded-lg px-3 py-2.5 text-text-primary text-[14px] font-body-md outline-none focus:border-accent-primary-start transition-colors"
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
            {creating ? (editingMode === 'rename' ? 'Renaming…' : 'Creating…') : (editingMode === 'rename' ? 'Rename' : 'Create')}
          </button>
        </div>
      </div>
    </div>
  {/if}

  <!-- Sidebar Footer -->
  <div
    class="px-3 py-2 border-t border-border-muted flex items-center justify-between bg-bg-surface flex-shrink-0"
  >
    {#if onCloseVault}
      <button
        onclick={onCloseVault}
        aria-label="Change Vault"
        title="Close this vault and return to the setup screen"
        class="flex items-center gap-1.5 px-2 py-1 rounded text-[11px] font-label-sm text-text-muted hover:text-accent-primary-start hover:bg-bg-hover transition-all duration-150 border-none bg-transparent cursor-pointer focus:outline-none"
      >
        <span class="material-symbols-outlined text-[15px]">swap_horiz</span>
        <span>Change Vault</span>
      </button>
    {/if}
    <button
      onclick={() => (collapsed = true)}
      aria-label="Hide sidebar"
      title="Hide sidebar (Ctrl+B)"
      class="p-1.5 rounded hover:bg-bg-hover text-text-muted hover:text-accent-primary-start transition-all duration-150 border-none bg-transparent cursor-pointer focus:outline-none flex items-center justify-center hover:scale-105 active:scale-95"
    >
      <span class="material-symbols-outlined text-[18px]">left_panel_close</span
      >
    </button>
  </div>
</aside>

<!-- Context menu (#62) -->
{#if contextMenu}
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
  <div
    onclick={closeContextMenu}
    oncontextmenu={(e) => { e.preventDefault(); closeContextMenu() }}
    class="fixed inset-0 z-[180]"
  >
    <div
      class="fixed context-menu-card"
      style:left={contextMenu.x + 'px'}
      style:top={contextMenu.y + 'px'}
      onclick={(e) => e.stopPropagation()}
      role="menu"
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
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
  <div
    onclick={cancelDelete}
    class="fixed inset-0 bg-black/60 backdrop-blur-sm z-[190] flex items-center justify-center"
  >
    <div
      role="dialog"
      aria-modal="true"
      aria-label="Confirm delete"
      tabindex="-1"
      onclick={(e) => e.stopPropagation()}
      class="bg-bg-panel border border-border-active rounded-xl shadow-2xl max-w-sm w-full mx-4 overflow-hidden"
    >
      <div class="px-5 py-4 border-b border-border-muted">
        <h2 class="font-headline-md text-headline-md text-text-primary">
          Delete {deleteTarget.level}?
        </h2>
        <p class="text-text-muted text-[12px] font-body-md mt-1">
          This will move the {deleteTarget.label} to <code>.system/trash/</code>.
          You can recover it from there manually.
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
          Delete
        </button>
      </div>
    </div>
  </div>
{/if}

<style>
  .context-menu-card {
    background-color: rgba(22, 22, 25, 0.9);
    backdrop-filter: blur(12px) saturate(140%);
    border: 1px solid var(--border-active);
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
    color: var(--text-primary);
    font-size: 13px;
    font-family: var(--font-body, inherit);
    text-align: left;
    cursor: pointer;
    border-radius: 6px;
    transition: background-color 120ms ease-out;
  }
  .context-menu-item:hover {
    background-color: var(--bg-hover);
  }
  :global(.drag-over-top) {
    box-shadow: inset 0 2px 0 var(--accent-primary-start);
  }
  :global(.drag-over-bottom) {
    box-shadow: inset 0 -2px 0 var(--accent-primary-start);
  }
</style>
