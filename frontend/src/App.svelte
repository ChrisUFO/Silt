<script lang="ts">
  import { onMount } from 'svelte'
  import {
    IsVaultInitialized,
    InitializeVault
  } from '../wailsjs/go/main/App.js'
  import TitleBar from './components/TitleBar.svelte'
  import Sidebar from './components/Sidebar.svelte'
  import VirtualScrollContainer from './components/VirtualScrollContainer.svelte'
  import SearchModal from './components/SearchModal.svelte'
  import logo from './assets/logo.svg'

  let isInitialized = $state(false)
  let loading = $state(true)

  // Navigation state (3-level: notebook > section > page)
  let activeNotebook = $state('')
  let activeSection = $state('')
  let activePage = $state('')
  let activeView = $state('notes')

  // Shell state
  let sidebarCollapsed = $state(false)
  let showSearch = $state(false)

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

    window.addEventListener('keydown', handleGlobalKeyDown)
    return () => {
      window.removeEventListener('keydown', handleGlobalKeyDown)
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
      onToggleSidebar={() => (sidebarCollapsed = !sidebarCollapsed)}
    />

    <div class="flex mt-14 h-[calc(100vh-56px)] w-full relative">
      <Sidebar
        bind:activeNotebook
        bind:activeSection
        bind:activePage
        bind:activeView
        collapsed={sidebarCollapsed}
        onSelectNotebook={(nb) => (activeNotebook = nb)}
        onSelectSection={(sec) => (activeSection = sec)}
        onSelectPage={(nb, sec, pg) => {
          activeNotebook = nb
          activeSection = sec
          activePage = pg
        }}
        onSelectView={(v) => (activeView = v)}
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
        {:else}
          <!-- Placeholder views: Agenda/Tags/Calendar/Kanban arrive in later phases -->
          <div class="flex-1 p-8 flex flex-col select-none">
            <h1
              class="font-headline-lg text-headline-lg text-text-primary mb-2 capitalize"
            >
              {activeView}
            </h1>
            <p class="text-text-muted font-body-md">
              The {activeView} view loads in a later sprint phase.
            </p>
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
