<script lang="ts">
  import { onMount } from 'svelte'
  import {
    IsVaultInitialized,
    InitializeVault,
    CloseVault
  } from '../wailsjs/go/main/App.js'
  import { EventsOn } from '../wailsjs/runtime/runtime.js'
  import { fade } from 'svelte/transition'
  import TitleBar from './components/TitleBar.svelte'
  import Sidebar from './components/Sidebar.svelte'
  import VirtualScrollContainer from './components/VirtualScrollContainer.svelte'
  import SearchModal from './components/SearchModal.svelte'
  import TagsExplorer from './components/TagsExplorer.svelte'
  import PluginView from './components/PluginView.svelte'
  import PluginManagerModal from './components/PluginManagerModal.svelte'
  import { loadPlugins } from './plugins/loader'
  import logo from './assets/logo.svg'

  let isInitialized = $state(false)
  let loading = $state(true)

  // Navigation state (3-level: notebook > section > page)
  let activeNotebook = $state('')
  let activeSection = $state('')
  let activePage = $state('')
  let activeView = $state('notes')
  let selectedTag = $state('')

  // Shell state
  let sidebarCollapsed = $state(false)
  let showSearch = $state(false)
  let showPluginManager = $state(false)

  // Focused block ancestry path highlighting
  let activeFocusedBlockAncestors = $state<string[]>([])
  let searchTargetDate = $state('')
  let searchTargetBlockId = $state('')
  let searchTargetKey = $state('')

  onMount(() => {
    async function checkInit() {
      try {
        isInitialized = await IsVaultInitialized()
      } catch (e) {
        console.error('Startup check failed:', e)
      } finally {
        loading = false
      }
    }
    checkInit()
    // Discover and initialize plugins once the frontend is mounted. They
    // load from the (possibly empty) vault registry and first-party bundle.
    loadPlugins('', '', '').catch((e) =>
      console.error('Plugin load failed:', e)
    )

    function handleGlobalKeyDown(e: KeyboardEvent) {
      // Ctrl+P → search
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'p') {
        e.preventDefault()
        showSearch = !showSearch
      }
      // Ctrl+B → toggle sidebar
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'b') {
        e.preventDefault()
        sidebarCollapsed = !sidebarCollapsed
      }
    }

    // Smart Graph navigation: refs/embeds/tag-pills dispatch these.
    function handleNavigateToBlock(e: Event) {
      const d = (e as CustomEvent).detail
      if (d) {
        handleSearchJump(d.notebook, d.section, d.page, d.date, d.blockId)
      }
    }
    function handleNavigateToTag(e: Event) {
      const tagPath = (e as CustomEvent).detail
      if (tagPath) {
        selectedTag = tagPath
        activeView = 'tags'
      }
    }
    function handleOpenPluginManager() {
      showPluginManager = true
    }
    function handlePluginsChanged() {
      // Re-run discovery with the live location so newly installed/enabled
      // plugins appear and removed ones drop out.
      loadPlugins(activeNotebook, activeSection, activePage).catch((e) =>
        console.error('Plugin reload failed:', e)
      )
    }

    window.addEventListener('keydown', handleGlobalKeyDown)
    window.addEventListener('navigate-to-block', handleNavigateToBlock)
    window.addEventListener('navigate-to-tag', handleNavigateToTag)
    window.addEventListener('open-plugin-manager', handleOpenPluginManager)
    // `plugins:changed` is a Wails event (Go runtime.EventsEmit), so it must
    // be received via EventsOn — a DOM addEventListener would never fire.
    const offPluginsChanged = EventsOn('plugins:changed', () =>
      handlePluginsChanged()
    )
    return () => {
      window.removeEventListener('keydown', handleGlobalKeyDown)
      window.removeEventListener('navigate-to-block', handleNavigateToBlock)
      window.removeEventListener('navigate-to-tag', handleNavigateToTag)
      window.removeEventListener('open-plugin-manager', handleOpenPluginManager)
      offPluginsChanged()
    }
  })

  async function handleSelectFolder() {
    try {
      const success = await InitializeVault()
      if (success) {
        isInitialized = true
        window.dispatchEvent(new CustomEvent('refresh-navigation'))
      }
    } catch (e) {
      alert('Failed to initialize vault: ' + e)
    }
  }

  // Change Vault: tear down the active vault and re-show the onboarding
  // screen so the user can pick (or re-pick) a workspace folder (#33). The
  // backend CloseVault waits on any in-flight writes and checkpoints the WAL.
  async function handleChangeVault() {
    try {
      await CloseVault()
      // Re-query rather than assume — CloseVault is the source of truth.
      isInitialized = await IsVaultInitialized()
      activeNotebook = ''
      activeSection = ''
      activePage = ''
      activeView = 'notes'
    } catch (e) {
      console.error('Failed to close vault:', e)
    }
  }

  function handleSearchJump(
    notebook: string,
    section: string,
    page: string,
    date: string,
    blockId: string
  ) {
    activeNotebook = notebook
    activeSection = section
    activePage = page
    activeView = 'notes'
    searchTargetDate = date
    searchTargetBlockId = blockId
    searchTargetKey = `${date}:${blockId}:${Date.now()}`
  }

  function handleBlockFocus(blockId: string, ancestors: string[]) {
    activeFocusedBlockAncestors = ancestors
  }

  function handleBlockBlur() {
    activeFocusedBlockAncestors = []
  }

  // SearchModal returns a flat result object; adapt it to the 5-arg jump.
  function handleSearchResultJump(res: any) {
    handleSearchJump(res.notebook, res.section, res.page, res.file_date, res.id)
  }

  // Whether the notes view has a complete (notebook/section/page) target.
  let notesReady = $derived(
    activeView === 'notes' &&
      !!activeNotebook &&
      !!activeSection &&
      !!activePage
  )
</script>

<main
  class="w-full h-full flex flex-col bg-bg-void text-text-primary overflow-hidden font-body-md"
>
  {#if loading}
    <div class="onboarding-container">
      <div class="text-text-muted animate-pulse text-lg font-headline-md">
        Initializing Silt Core…
      </div>
    </div>
  {:else if !isInitialized}
    <!-- First run onboarding -->
    <div class="onboarding-container select-none">
      <div class="onboarding-card">
        <img
          src={logo}
          alt="Silt Logo"
          class="onboarding-logo animate-spin-slow"
        />
        <h1 class="onboarding-title font-headline-lg">Silt</h1>
        <p class="onboarding-description font-body-md">
          A local-first hybrid journal and task manager. Plain-text Markdown on
          your drive, real-time index in memory.
        </p>
        <button
          class="onboarding-btn font-label-sm-bold"
          onclick={handleSelectFolder}
        >
          Initialize Workspace Folder
        </button>
      </div>
    </div>
  {:else}
    <TitleBar
      bind:activeView
      bind:sidebarCollapsed
      onSearchClick={() => (showSearch = true)}
    />

    <div class="flex mt-14 h-[calc(100vh-56px)] w-full relative">
      {#if sidebarCollapsed}
        <button
          onclick={() => (sidebarCollapsed = false)}
          transition:fade={{ duration: 150 }}
          aria-label="Show sidebar"
          title="Show sidebar (Ctrl+B)"
          class="absolute bottom-4 left-4 z-50 w-8 h-8 rounded-lg bg-bg-surface/80 backdrop-blur-md border border-border-muted text-text-muted hover:text-accent-primary-start hover:border-accent-primary-start/40 flex items-center justify-center transition-all cursor-pointer shadow-lg hover:scale-105 active:scale-95"
        >
          <span class="material-symbols-outlined text-[18px]"
            >left_panel_open</span
          >
        </button>
      {/if}

      <Sidebar
        bind:activeNotebook
        bind:activeSection
        bind:activePage
        bind:activeView
        bind:collapsed={sidebarCollapsed}
        onSelectNotebook={(nb) => (activeNotebook = nb)}
        onSelectSection={(sec) => (activeSection = sec)}
        onSelectPage={(nb, sec, pg) => {
          activeNotebook = nb
          activeSection = sec
          activePage = pg
        }}
        onSelectView={(v) => (activeView = v)}
        onCloseVault={handleChangeVault}
      />

      <!-- Content viewport -->
      <div
        class="flex-1 h-full min-w-0 flex flex-col overflow-hidden bg-bg-void"
      >
        {#if activeView === 'notes'}
          {#if notesReady}
            <VirtualScrollContainer
              notebook={activeNotebook}
              section={activeSection}
              page={activePage}
              targetDate={searchTargetDate}
              targetBlockId={searchTargetBlockId}
              targetKey={searchTargetKey}
              {activeFocusedBlockAncestors}
              onBlockFocus={handleBlockFocus}
              onBlockBlur={handleBlockBlur}
            />
          {:else}
            <div
              class="flex-1 flex flex-col items-center justify-center text-center px-8 select-none"
            >
              <span
                class="material-symbols-outlined text-text-muted text-[64px] mb-4 opacity-40"
                >edit_note</span
              >
              <h2
                class="font-headline-md text-headline-md text-text-primary mb-2"
              >
                {#if !activeNotebook}
                  Create or open a notebook to begin
                {:else if !activeSection}
                  Select or create a section
                {:else}
                  Select or create a page
                {/if}
              </h2>
              <p class="text-text-muted font-body-md max-w-md">
                Silt organizes notes as Notebook › Section › Page. Use the
                sidebar navigator to create your first notebook, then add a
                section and a page to start writing.
              </p>
            </div>
          {/if}
        {:else if activeView === 'tags'}
          <TagsExplorer {selectedTag} />
        {:else if activeView === 'agenda' || activeView === 'calendar' || activeView === 'kanban'}
          <PluginView
            pluginId={'silt-' + activeView}
            {activeNotebook}
            {activeSection}
            {activePage}
          />
        {:else}
          <!-- Unknown view -->
          <div class="flex-1 p-8 flex flex-col select-none">
            <h1
              class="font-headline-lg text-headline-lg text-text-primary mb-2 capitalize"
            >
              {activeView}
            </h1>
          </div>
        {/if}
      </div>
    </div>
  {/if}

  {#if showSearch}
    <SearchModal
      onClose={() => (showSearch = false)}
      onJump={handleSearchResultJump}
    />
  {/if}

  {#if showPluginManager}
    <PluginManagerModal
      onClose={() => (showPluginManager = false)}
      {activeNotebook}
      {activeSection}
      {activePage}
    />
  {/if}
</main>

<style>
  .animate-spin-slow {
    animation: spin 8s linear infinite;
  }
  @keyframes spin {
    from {
      transform: rotate(0deg);
    }
    to {
      transform: rotate(360deg);
    }
  }
</style>
