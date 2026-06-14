<script lang="ts">
  import { onMount, untrack } from 'svelte'
  import {
    ListNavigation,
    CreateNotebook,
    CreateSection,
    CreatePage,
    PickNotebookFolder
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
    onSelectNotebook: (notebook: string) => void
    onSelectSection: (section: string) => void
    onSelectPage: (notebook: string, section: string, page: string) => void
    onSelectView: (view: string) => void
  }

  let {
    activeNotebook = $bindable(),
    activeSection = $bindable(),
    activePage = $bindable(),
    activeView = $bindable(),
    collapsed = $bindable(),
    onSelectNotebook,
    onSelectSection,
    onSelectPage,
    onSelectView
  }: Props = $props()

  let tree = $state<NavigationTree>({ notebooks: [] })
  let showNotebookDropdown = $state(false)

  // Creation modals
  let createMode = $state<'' | 'notebook' | 'section' | 'page'>('')
  let newName = $state('')
  let createError = $state('')
  let creating = $state(false)
  let modalInputEl = $state<HTMLInputElement | null>(null)

  // Expanded section names (within the active notebook). The active section is
  // always expanded so the active path stays visible (spatial memory).
  let expandedSections = $state<Set<string>>(new Set())

  let activeNotebookObj = $derived(
    tree.notebooks.find((nb) => nb.name === activeNotebook)
  )

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

  function openCreate(mode: 'notebook' | 'section' | 'page') {
    createMode = mode
    newName = ''
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
      if (createMode === 'notebook') {
        await CreateNotebook(trimmed)
        await loadNavigation()
        handleSelectNotebook(trimmed)
      } else if (createMode === 'section') {
        await CreateSection(activeNotebook, trimmed)
        await loadNavigation()
        activeSection = trimmed
        onSelectSection(trimmed)
        expandedSections = new Set([...expandedSections, trimmed])
      } else if (createMode === 'page') {
        const sectionForPage = activeSection || ''
        await CreatePage(activeNotebook, sectionForPage, trimmed, '')
        await loadNavigation()
        activeSection = sectionForPage
        activePage = trimmed
        onSelectPage(activeNotebook, sectionForPage, trimmed)
        activeView = 'notes'
        onSelectView('notes')
      }
      createMode = ''
      newName = ''
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

  onMount(() => {
    loadNavigation()
    const handleRefresh = () => loadNavigation()
    window.addEventListener('refresh-navigation', handleRefresh)
    return () => {
      window.removeEventListener('refresh-navigation', handleRefresh)
    }
  })
</script>

<aside
  class="bg-bg-surface border-r border-border-muted flex flex-col py-[4px] h-full transition-all duration-200 ease-out flex-shrink-0 select-none z-40"
  class:w-64={!collapsed}
  class:w-0={collapsed}
  class:overflow-hidden={collapsed}
  class:border-r-0={collapsed}
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
          class="material-symbols-outlined text-accent-teal-start text-[20px]"
          >menu_book</span
        >
        <div class="flex flex-col min-w-0 flex-1">
          <span
            class="text-accent-teal-start font-headline-md text-headline-md truncate"
            >{activeNotebook || 'No Notebook'}</span
          >
          <span
            class="text-text-muted text-[9px] uppercase tracking-widest font-label-sm-bold"
            >Active Notebook</span
          >
        </div>
        <span
          class="material-symbols-outlined text-text-muted text-[18px] group-hover:text-accent-teal-start transition-colors"
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
          class="absolute left-1 right-1 top-14 glass-palette border border-accent-teal-start/20 rounded-lg shadow-2xl z-[70] py-2 max-h-[60vh] overflow-y-auto custom-scrollbar"
          style="backdrop-filter: blur(16px); background: rgba(22, 22, 25, 0.92);"
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
                  class="material-symbols-outlined text-accent-teal-start text-[18px]"
                  >folder_special</span
                >
                <span
                  class="font-label-sm text-label-sm text-text-primary truncate flex-1"
                  >{nb.name}</span
                >
                {#if nb.name === activeNotebook}
                  <span
                    class="material-symbols-outlined text-accent-teal-start text-[16px]"
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
              class="flex items-center gap-3 px-4 py-2 w-full text-left cursor-pointer hover:bg-bg-hover transition-colors font-body-md border-none bg-transparent text-accent-teal-start"
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
          class="w-full bg-accent-teal-glow border border-accent-teal-start/30 text-accent-teal-start font-label-sm-bold py-2 rounded flex items-center justify-center hover:brightness-110 hover:border-accent-teal-start transition-all cursor-pointer focus:outline-none disabled:opacity-40 disabled:cursor-not-allowed"
        >
          <span class="material-symbols-outlined text-[20px]"
            >create_new_folder</span
          >
        </button>
      </span>
      <span title={pageHint} class="flex-1 flex">
        <button
          onclick={() => openCreate('page')}
          disabled={!activeNotebook}
          title={pageHint}
          aria-label="New Page"
          class="w-full bg-bg-panel border border-border-muted text-text-muted hover:text-accent-teal-start hover:border-accent-teal-start/40 font-label-sm-bold py-2 rounded flex items-center justify-center transition-all cursor-pointer focus:outline-none disabled:opacity-40 disabled:cursor-not-allowed"
        >
          <span class="material-symbols-outlined text-[20px]">note_add</span>
        </button>
      </span>
    </div>
    {#if nextStep}
      <div
        class="px-2 pb-2 text-[10px] text-text-muted font-label-sm flex items-center gap-1"
      >
        <span
          class="material-symbols-outlined text-[12px] text-accent-teal-start/70"
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
        {#each activeNotebookObj.sections as sec (sec.name)}
          {@const isExpanded = expandedSections.has(sec.name)}
          <div class="mb-0.5">
            <!-- Section header -->
            <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
            <div
              class="group flex items-center gap-1 px-2 py-1.5 cursor-pointer rounded hover:bg-bg-hover transition-colors"
              onclick={() => toggleSection(sec.name)}
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
                  openCreate('page')
                }}
                title="New page in this section"
                class="opacity-0 group-hover:opacity-100 text-text-muted hover:text-accent-teal-start border-none bg-transparent cursor-pointer p-0.5 rounded transition-all"
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
                  {#each sec.pages as pg (pg.name)}
                    {@const isActive =
                      activeSection === sec.name && activePage === pg.name}
                    <button
                      onclick={() => handleSelectPage(sec.name, pg.name)}
                      class="relative w-full text-left pl-4 pr-2 py-1.5 rounded text-[13px] font-body-md transition-colors border-none bg-transparent cursor-pointer flex items-center gap-2"
                      class:bg-bg-hover={isActive}
                      class:text-accent-teal-start={isActive}
                      class:text-text-muted={!isActive}
                      class:hover:text-text-primary={!isActive}
                      role="treeitem"
                    >
                      {#if isActive}
                        <span
                          class="absolute left-0 top-1 bottom-1 w-[2px] bg-accent-teal-start rounded-full"
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
      class="fixed inset-0 bg-[#000]/60 backdrop-blur-sm z-[160] flex items-start justify-center pt-32"
    >
      <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
      <div
        onclick={(e) => e.stopPropagation()}
        class="w-full max-w-md glass-palette border border-border-zinc rounded-xl shadow-2xl overflow-hidden"
        style="backdrop-filter: blur(16px) saturate(140%); background: rgba(22, 22, 25, 0.9);"
      >
        <div class="px-5 py-4 border-b border-border-muted">
          <h2 class="font-headline-md text-headline-md text-text-primary">
            New {createMode === 'notebook'
              ? 'Notebook'
              : createMode === 'section'
                ? 'Section'
                : 'Page'}
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
            placeholder={createMode === 'notebook'
              ? 'Notebook name…'
              : createMode === 'section'
                ? 'Section name…'
                : 'Page name…'}
            class="w-full bg-bg-surface border border-border-zinc rounded-lg px-3 py-2.5 text-text-primary text-[14px] font-body-md outline-none focus:border-accent-teal-start transition-colors"
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
            class="px-4 py-2 rounded-lg bg-accent-teal-start/20 border border-accent-teal-start/40 text-accent-teal-start font-label-sm-bold hover:brightness-110 transition-all cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {creating ? 'Creating…' : 'Create'}
          </button>
        </div>
      </div>
    </div>
  {/if}

  <!-- Sidebar Footer -->
  <div
    class="px-3 py-2 border-t border-border-muted flex items-center justify-end bg-bg-surface flex-shrink-0"
  >
    <button
      onclick={() => (collapsed = true)}
      aria-label="Hide sidebar"
      title="Hide sidebar (Ctrl+B)"
      class="p-1.5 rounded hover:bg-bg-hover text-text-muted hover:text-accent-teal-start transition-all duration-150 border-none bg-transparent cursor-pointer focus:outline-none flex items-center justify-center hover:scale-105 active:scale-95"
    >
      <span class="material-symbols-outlined text-[18px]">left_panel_close</span
      >
    </button>
  </div>
</aside>
