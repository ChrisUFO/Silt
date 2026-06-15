<script lang="ts">
  import { onMount } from 'svelte'
  import {
    IsVaultInitialized,
    InitializeVault,
    CloseVault,
    GetSidebarWidth,
    SetSidebarWidth
  } from '../wailsjs/go/main/App.js'
  import { EventsOn } from '../wailsjs/runtime/runtime.js'
  import { fade } from 'svelte/transition'
  import TitleBar from './components/TitleBar.svelte'
  import Sidebar from './components/Sidebar.svelte'
  import VirtualScrollContainer from './components/VirtualScrollContainer.svelte'
  import SearchModal from './components/SearchModal.svelte'
  import TagsExplorer from './components/TagsExplorer.svelte'
  import PluginView from './components/PluginView.svelte'
  import SettingsShell from './components/settings/SettingsShell.svelte'
  import { loadPlugins } from './plugins/loader'
  import {
    initConfigHotReload,
    loadConfig,
    settings,
    type SystemConfig
  } from './settings/store.svelte'
  import { initEditorTokens } from './settings/editor-tokens.svelte'
  import { initThemes } from './theme/store.svelte'
  import { initTemplates } from './templates/store.svelte'
  import TemplatePicker from './templates/TemplatePicker.svelte'
  import { matchHotkey } from './settings/hotkeys'
  import SidebarResizeHandle from './components/SidebarResizeHandle.svelte'
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
  let sidebarWidth = $state(256)
  let manuallyCollapsed = $state(false)
  let sidebarDragging = $state(false)
  let showSearch = $state(false)
  let showSettings = $state(false)
  let settingsTab = $state('general')
  let showTemplatePicker = $state(false)
  let templatePickerMode = $state<'new-page' | 'insert'>('new-page')

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
    // Best-effort: load the config first so the initial loadPlugins call
    // observes plugins.disabled on a cold start (a config.yaml that ships
    // with a pre-disabled first-party plugin must NOT load it on the first
    // paint). loadConfig errors out before a vault is open; that's fine —
    // loadPlugins will then see an empty disabled set, matching the
    // pre-PR behavior.
    loadConfig()
      .catch((e) => console.error('Startup config load failed:', e))
      .finally(() => {
        loadPlugins('', '', '').catch((e) =>
          console.error('Plugin load failed:', e)
        )
      })

    // Load the persisted sidebar width from config.yaml (#63).
    GetSidebarWidth()
      .then((px) => {
        sidebarWidth = px
      })
      .catch(() => {})
    // Subscribe to config hot-reload (config:changed from Go) so the settings
    // store refreshes on external edits to .system/config.yaml.
    initConfigHotReload()
    // Inject editor typography CSS variables from config and re-inject on
    // hot-reload. Uses $effect.root to watch the reactive settings store.
    // The returned disposer is called on unmount to prevent duplicate root
    // effects during dev hot-reload.
    const disposeEditorTokens = initEditorTokens()
    // Populate the theme listing store (#47) and subscribe to the
    // backend's "themes:changed" event so an imported theme appears in
    // the picker immediately. Disposed on unmount alongside the other
    // store initializers.
    const disposeThemes = initThemes()
    const disposeTemplates = initTemplates()

    // Hot-reload the plugin registry when an external config.yaml edit
    // changes plugins.disabled (e.g. the user hand-edits the file as
    // documented in docs/PLUGIN_DEVELOPMENT.md). Diff against the last
    // seen value so unrelated config changes (theme, hotkeys, etc.) do
    // not pay the ESM-import + plugin init cost.
    let prevDisabled: string[] = settings.config?.plugins?.disabled ?? []
    const offConfigChangedReload = EventsOn(
      'config:changed',
      (cfg: SystemConfig) => {
        const next = (cfg?.plugins?.disabled ?? []) as string[]
        if (!arraysEqual(prevDisabled, next)) {
          prevDisabled = [...next]
          loadPlugins(activeNotebook, activeSection, activePage).catch((e) =>
            console.error('Plugin reload after config change failed:', e)
          )
        }
      }
    )

    function handleOpenSettings(e: Event) {
      const detail = (e as CustomEvent).detail
      openSettings(typeof detail === 'string' ? detail : 'general')
    }
    function handleGlobalKeyDown(e: KeyboardEvent) {
      // Config-driven global shortcuts. Read live from the settings store so
      // edits made in Settings → General take effect after Save (no rebind
      // needed — the store is a reactive proxy read at event time). Editor-
      // internal shortcuts (indent/unindent) are consumed by the editor's
      // own keydown handler; cycle_view_layout is global (it changes the
      // main view, not anything inside the contenteditable).
      const hotkeys = settings.config?.hotkeys ?? {}
      if (matchHotkey(e, hotkeys.open_search)) {
        e.preventDefault()
        showSearch = !showSearch
      }
      if (matchHotkey(e, hotkeys.toggle_sidebar)) {
        e.preventDefault()
        sidebarCollapsed = !sidebarCollapsed
        manuallyCollapsed = sidebarCollapsed
      }
      if (matchHotkey(e, hotkeys.cycle_view_layout)) {
        e.preventDefault()
        cycleView()
      }
      if (matchHotkey(e, hotkeys.open_template_picker)) {
        e.preventDefault()
        templatePickerMode = 'new-page'
        showTemplatePicker = !showTemplatePicker
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
    function handleSwitchView(e: Event) {
      // PluginsTab "Open view" + any other switch-view dispatcher.
      const detail = (e as CustomEvent).detail
      if (typeof detail === 'string' && detail) {
        activeView = detail
        showSettings = false
      }
    }
    function handleOpenPluginManager() {
      // The plugin manager is now the "Plugins" tab inside Settings.
      openSettings('plugins')
    }
    function handlePluginsChanged() {
      // Re-run discovery with the live location so newly installed/enabled
      // plugins appear and removed ones drop out.
      loadPlugins(activeNotebook, activeSection, activePage).catch((e) =>
        console.error('Plugin reload failed:', e)
      )
    }
    function handleOpenTemplatePicker() {
      templatePickerMode = 'new-page'
      showTemplatePicker = true
    }

    window.addEventListener('keydown', handleGlobalKeyDown)
    window.addEventListener('navigate-to-block', handleNavigateToBlock)
    window.addEventListener('navigate-to-tag', handleNavigateToTag)
    window.addEventListener('switch-view', handleSwitchView)
    window.addEventListener('open-plugin-manager', handleOpenPluginManager)
    window.addEventListener('open-settings', handleOpenSettings)
    window.addEventListener('open-template-picker', handleOpenTemplatePicker)
    // `plugins:changed` is a Wails event (Go runtime.EventsEmit), so it must
    // be received via EventsOn — a DOM addEventListener would never fire.
    const offPluginsChanged = EventsOn('plugins:changed', () =>
      handlePluginsChanged()
    )
    return () => {
      window.removeEventListener('keydown', handleGlobalKeyDown)
      window.removeEventListener('navigate-to-block', handleNavigateToBlock)
      window.removeEventListener('navigate-to-tag', handleNavigateToTag)
      window.removeEventListener('switch-view', handleSwitchView)
      window.removeEventListener('open-plugin-manager', handleOpenPluginManager)
      window.removeEventListener('open-settings', handleOpenSettings)
      window.removeEventListener('open-template-picker', handleOpenTemplatePicker)
      offPluginsChanged()
      offConfigChangedReload()
      disposeEditorTokens()
      disposeThemes()
      disposeTemplates()
    }
  })

  async function handleSelectFolder() {
    try {
      const success = await InitializeVault()
      if (success) {
        isInitialized = true
        // Populate the config store now that a vault exists so config-driven
        // global shortcuts work immediately after onboarding.
        loadConfig().catch((e) =>
          console.error('Post-init config load failed:', e)
        )
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

  // Called by the TemplatePicker when a new page is created from a template.
  // Navigates to the freshly-created page (the reactive cascade loads it in
  // the editor) and refreshes the sidebar tree so the new page appears.
  function handleTemplatePageCreated(page: string): void {
    activePage = page
    activeView = 'notes'
    window.dispatchEvent(new CustomEvent('refresh-navigation'))
  }

  function handleBlockFocus(blockId: string, ancestors: string[]) {
    activeFocusedBlockAncestors = ancestors
  }

  function handleBlockBlur() {
    activeFocusedBlockAncestors = []
  }

  // Sidebar resize handlers (#63).
  const MIN_MAIN_WIDTH = 480

  function handleSidebarWidthChange(px: number) {
    sidebarWidth = px
  }

  let setSidebarTimer: ReturnType<typeof setTimeout> | null = null
  function handleSidebarWidthCommit(px: number) {
    sidebarWidth = px
    if (setSidebarTimer) clearTimeout(setSidebarTimer)
    setSidebarTimer = setTimeout(() => {
      SetSidebarWidth(px).catch((e) =>
        console.error('SetSidebarWidth failed:', e)
      )
    }, 250)
  }

  function handleSidebarDragStart() {
    sidebarDragging = true
  }
  function handleSidebarDragEnd() {
    sidebarDragging = false
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

  function openSettings(tab?: string) {
    settingsTab = tab || 'general'
    showSettings = true
  }

  // Ordered view cycle for the cycle_view_layout hotkey (default Alt+Tab).
  // If the current view is not in the list (e.g. a plugin view), jump to
  // 'notes' as the anchor.
  const VIEW_CYCLE = ['notes', 'tags', 'agenda', 'calendar', 'kanban'] as const
  function cycleView() {
    const idx = VIEW_CYCLE.indexOf(activeView as (typeof VIEW_CYCLE)[number])
    if (idx === -1) {
      activeView = 'notes'
    } else {
      activeView = VIEW_CYCLE[(idx + 1) % VIEW_CYCLE.length]
    }
  }

  // Order-independent string-array equality (the disabled list is a set
  // semantically — config.yaml can re-order it without changing meaning).
  // Used by the config:changed handler to decide whether to re-run
  // loadPlugins on a hot-reload.
  function arraysEqual(a: readonly string[], b: readonly string[]): boolean {
    if (a.length !== b.length) return false
    const setA = new Set(a)
    return b.every((x) => setA.has(x))
  }
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
      {sidebarWidth}
      onSearchClick={() => (showSearch = true)}
      onOpenSettings={(tab) => openSettings(tab)}
    />

    <div class="flex mt-14 h-[calc(100vh-56px)] w-full relative">
      {#if sidebarCollapsed}
        <button
          onclick={() => {
            sidebarCollapsed = false
            manuallyCollapsed = false
          }}
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
        {sidebarWidth}
        {sidebarDragging}
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

      {#if !sidebarCollapsed}
        <SidebarResizeHandle
          width={sidebarWidth}
          onWidthChange={handleSidebarWidthChange}
          onWidthCommit={handleSidebarWidthCommit}
        />
      {/if}

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

  {#if showSettings}
    <SettingsShell
      bind:activeTab={settingsTab}
      onClose={() => (showSettings = false)}
      {activeNotebook}
      {activeSection}
      {activePage}
    />
  {/if}

  {#if showTemplatePicker}
    <TemplatePicker
      mode={templatePickerMode}
      notebook={activeNotebook}
      section={activeSection}
      onClose={() => (showTemplatePicker = false)}
      onCreatedPage={handleTemplatePageCreated}
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
