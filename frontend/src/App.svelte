<script lang="ts">
  import { onMount, tick } from 'svelte'
  import {
    IsVaultInitialized,
    InitializeVault,
    CloseVault,
    GetSidebarWidth,
    SetSidebarWidth,
    GetOpenTabs,
    SetOpenTabs,
    ConfirmSettingsChange,
    ConfirmGrantsMigration,
    DeclineGrantsMigration,
    ResolveQuarantinedLinks,
    PickLinkedNotebook,
    UnlinkNotebook
  } from '../wailsjs/go/main/App.js'
  import { EventsOn } from '../wailsjs/runtime/runtime.js'
  import type { config } from '../wailsjs/go/models.js'
  import { fade } from 'svelte/transition'
  import TitleBar from './components/TitleBar.svelte'
  import Sidebar from './components/Sidebar.svelte'
  import VirtualScrollContainer from './components/VirtualScrollContainer.svelte'
  import TabStrip from './components/TabStrip.svelte'
  import SearchModal from './components/SearchModal.svelte'
  import TagsExplorer from './components/TagsExplorer.svelte'
  import PluginView from './components/PluginView.svelte'
  import SettingsShell from './components/settings/SettingsShell.svelte'
  import { loadPlugins } from './plugins/loader'
  import {
    initConfigHotReload,
    loadConfig,
    settings,
    type SystemConfig,
    toggleFormatToolbar,
    toggleFocusMode
  } from './settings/store.svelte'
  import { initEditorTokens } from './settings/editor-tokens.svelte'
  import { initThemes } from './theme/store.svelte'
  import { initTemplates } from './templates/store.svelte'
  import TemplatePicker from './templates/TemplatePicker.svelte'
  import { matchHotkey } from './settings/hotkeys'
  import SidebarResizeHandle from './components/SidebarResizeHandle.svelte'
  import PluginModalHost from './components/PluginModalHost.svelte'
  import PluginStatusBar from './components/PluginStatusBar.svelte'
  import { setActiveLocation } from './plugins/location.svelte'
  import ToastContainer from './components/ToastContainer.svelte'
  import Onboarding from './components/Onboarding.svelte'
  import { pushNotification } from './notifications/store.svelte'
  import {
    initStartupUpdateCheck,
    disposeUpdateStore
  } from './updates/store.svelte'
  import {
    openPage as openPageState,
    closeTab as closeTabState,
    promotePreview as promotePreviewState,
    cycleTab as cycleTabState,
    reorderTab as reorderTabState,
    setTabViewMode as setTabViewModeState,
    mergeReorderedTabs,
    generateTabId,
    type TabEntry,
    type PageRef,
    type OpenPageMode,
    type ViewMode
  } from './lib/tabs'
  import { nextView } from './lib/viewCycle'

  let isInitialized = $state(false)
  let loading = $state(true)

  // Tab state (#142). The tab list + active id are the source of truth for
  // the multi-page editor surface. The legacy active-notebook/section/page
  // triple (still read by Sidebar, plugins, breadcrumbs) is kept in sync
  // from the active tab by the tabSync effect below. The Sidebar's
  // onSelectPage callback funnels through openPage(); onSelectNotebook/
  // onSelectSection set the triple directly (sidebar context without a tab
  // change).
  let openTabs = $state<TabEntry[]>([])
  let activeTabId = $state<string>('')

  // Per-notebook tab scoping: the tab strip and editor surface only show
  // tabs for the active notebook. The full openTabs array (all notebooks)
  // persists to config.yaml so switching notebooks preserves each
  // notebook's tab set. (#142 — user request: tabs should not carry over
  // when switching notebooks.)
  let displayedTabs = $derived(
    openTabs.filter((t) => t.notebook === activeNotebook)
  )

  // Navigation state (3-level: notebook > section > page). Kept in sync with
  // the active tab; also set directly by onSelectNotebook/onSelectSection
  // (sidebar browsing context without opening a page).
  let activeNotebook = $state('')
  let activeSection = $state('')
  let activePage = $state('')
  let activeView = $state('notes')
  const views = [
    { id: 'notes', label: 'Notes', icon: 'description' },
    { id: 'tags', label: 'Tags', icon: 'label' },
    { id: 'calendar', label: 'Calendar', icon: 'calendar_month' }
  ]
  let selectedTag = $state('')

  // Shell state
  let sidebarCollapsed = $state(false)
  let sidebarWidth = $state(256)
  let manuallyCollapsed = $state(false)
  let sidebarDragging = $state(false)

  // Sync active navigation to the reactive plugin location (#69). Plugins
  // read ctx.activeNotebook/Section/Page via live getters backed by this state.
  $effect(() => {
    setActiveLocation(activeNotebook, activeSection, activePage)
  })

  // --- Tab management (#142) -----------------------------------------------

  // The per-vault default view mode a freshly-opened tab starts in (#195).
  // Read live from the settings store so a config.yaml edit takes effect on
  // the next open without a rebind.
  let defaultViewMode = $derived(
    settings.config?.editor?.default_view_mode === 'source'
      ? ('source' as const)
      : ('edit' as const)
  )

  // The single entry point for opening a page. All "open a page" callers
  // (sidebar click, search jump, navigate-to-block, refs) funnel through
  // here so the preview/pin logic lives in one place. Wraps the pure state
  // machine from tabs.ts and applies the result to the $state runes.
  function openPage(
    ref: PageRef,
    mode: OpenPageMode,
    blockTarget: { fileDate?: string; blockId?: string } | undefined = undefined
  ): void {
    const enablePreviewTabs = settings.config?.ui?.enable_preview_tabs !== false
    const maxOpenTabs = settings.config?.ui?.max_open_tabs ?? 8
    const result = openPageState(
      { tabs: openTabs, activeId: activeTabId },
      ref,
      mode,
      { enablePreviewTabs, maxOpenTabs, blockTarget, defaultViewMode }
    )
    openTabs = result.tabs
    activeTabId = result.activeId
    syncActiveFromTab()
    schedulePersistTabs()
  }

  // Toggle a tab between Edit and Source view (#195). The mode lives on
  // TabEntry (single source of truth) and persists to config.yaml on the next
  // debounced flush. No window-event indirection — App owns the state.
  function handleToggleViewMode(tabId: string): void {
    const tab = openTabs.find((t) => t.id === tabId)
    if (!tab) return
    const next: ViewMode = tab.viewMode === 'edit' ? 'source' : 'edit'
    openTabs = setTabViewModeState(
      { tabs: openTabs, activeId: activeTabId },
      tabId,
      next
    ).tabs
    schedulePersistTabs()
  }

  // Sync activeNotebook/Section/Page from the active tab so every downstream
  // consumer (Sidebar, plugins, breadcrumbs) keeps working unchanged.
  function syncActiveFromTab(): void {
    const tab = openTabs.find((t) => t.id === activeTabId)
    if (tab) {
      activeNotebook = tab.notebook
      activeSection = tab.section
      activePage = tab.page
    }
  }

  function handleSelectTab(id: string): void {
    activeTabId = id
    // Bump MRU ordering.
    const now = Date.now()
    openTabs = openTabs.map((t) =>
      t.id === id ? { ...t, lastActivatedAt: now } : t
    )
    syncActiveFromTab()
    schedulePersistTabs()
  }

  function handleCloseTab(id: string): void {
    const result = closeTabState({ tabs: openTabs, activeId: activeTabId }, id)
    openTabs = result.tabs
    activeTabId = result.activeId
    syncActiveFromTab()
    schedulePersistTabs()
  }

  function handlePromoteTab(id: string): void {
    openTabs = promotePreviewState(
      { tabs: openTabs, activeId: activeTabId },
      id
    ).tabs
    schedulePersistTabs()
  }

  function handleReorderTab(
    fromId: string,
    toId: string,
    before: boolean
  ): void {
    // industry-standard parity (#175 AC4): dragging a preview tab pins it on drop.
    // The promotion happens before the reorder so the pinned tab is the
    // one that gets spliced into the new position.
    const draggedTab = openTabs.find((t) => t.id === fromId)
    if (draggedTab?.preview) {
      openTabs = promotePreviewState(
        { tabs: openTabs, activeId: activeTabId },
        fromId
      ).tabs
    }
    // Reorder within the displayed (per-notebook) tabs, then splice the
    // reordered subset back into the full openTabs array — non-displayed
    // (other-notebook) tabs keep their relative positions.
    const result = reorderTabState(
      { tabs: displayedTabs, activeId: activeTabId },
      fromId,
      toId,
      before
    )
    openTabs = mergeReorderedTabs(openTabs, result.tabs, activeNotebook)
    schedulePersistTabs()
  }

  function handleCycleTab(dir: 1 | -1): void {
    // Cycle within the displayed (per-notebook) tabs only — Ctrl+Tab must
    // not jump to a hidden tab in another notebook (#142 review: cycling
    // across openTabs violated per-notebook scoping).
    const result = cycleTabState(
      { tabs: displayedTabs, activeId: activeTabId },
      dir
    )
    // Merge the MRU-bumped tabs (from the cycled subset) back into the
    // full openTabs array. cycleTabState → activateTab bumps
    // lastActivatedAt on the newly-active tab; without this merge,
    // repeated Ctrl+Tab presses would use stale timestamps and the
    // cycling order would degrade (#142 review: discarded MRU bump).
    openTabs = openTabs.map((t) => {
      const updated = result.tabs.find((x) => x.id === t.id)
      return updated ?? t
    })
    activeTabId = result.activeId
    syncActiveFromTab()
    schedulePersistTabs()
  }

  // --- Tab persistence (debounced 250ms, pinned-only) ----------------------

  let persistTabsTimer: ReturnType<typeof setTimeout> | null = null
  // Snapshot of the persisted open_tabs list for config:changed change
  // detection. Declared at component scope so loadPersistedTabs can update
  // it alongside the in-memory hydration (prevents a re-hydrate cycle).
  let prevOpenTabsKey = ''

  function schedulePersistTabs(): void {
    if (persistTabsTimer) clearTimeout(persistTabsTimer)
    persistTabsTimer = setTimeout(() => {
      persistTabsTimer = null
      void persistTabs()
    }, 250)
  }

  async function persistTabs(): Promise<void> {
    // Only persist PINNED tabs + active (preview tabs are ephemeral —
    // parity). If the active tab is a preview, don't persist it as active.
    const pinned = openTabs.filter((t) => !t.preview)
    const activeTab = openTabs.find((t) => t.id === activeTabId)
    const activePersist = activeTab && !activeTab.preview ? activeTab : null
    try {
      await SetOpenTabs(
        pinned.map((t) => ({
          notebook: t.notebook,
          section: t.section,
          page: t.page,
          // Persist the per-tab view mode only when it's Source (#195);
          // absence on disk means the Edit default, keeping config.yaml lean.
          view_mode: t.viewMode === 'source' ? 'source' : ''
        })),
        (activePersist
          ? {
              notebook: activePersist.notebook,
              section: activePersist.section,
              page: activePersist.page,
              view_mode: activePersist.viewMode === 'source' ? 'source' : ''
            }
          : null) as unknown as config.TabRef
      )
    } catch (e) {
      console.error('SetOpenTabs failed:', e)
    }
  }

  // Monotonic request sequence for loadPersistedTabs. Only the most-recent
  // call's result is applied, so overlapping calls (onMount + handleSelectFolder
  // firing in quick succession) don't race — the later call wins (#142 hardening).
  let loadTabsSeq = 0

  // Load persisted tabs on vault open / reopen. Hydrates openTabs from the
  // pinned set + active stored in config.yaml.
  async function loadPersistedTabs(): Promise<void> {
    const seq = ++loadTabsSeq
    try {
      const result = await GetOpenTabs()
      // Stale guard: a newer loadPersistedTabs call superseded this one.
      if (seq !== loadTabsSeq) return
      if (result?.open_tabs && result.open_tabs.length > 0) {
        const now = Date.now()
        openTabs = result.open_tabs.map((t, i) => ({
          id: generateTabId(),
          notebook: t.notebook,
          section: t.section,
          page: t.page,
          preview: false, // persisted tabs are always pinned
          lastActivatedAt: now - i, // stable ordering for MRU
          // Restore the per-tab view mode (#195). Only "source" is persisted;
          // absence / any other value means the Edit default.
          viewMode: t.view_mode === 'source' ? 'source' : 'edit'
        }))
        // Restore active tab if it's in the set.
        if (result.active_tab) {
          const active = openTabs.find(
            (t) =>
              t.notebook === result.active_tab!.notebook &&
              t.section === result.active_tab!.section &&
              t.page === result.active_tab!.page
          )
          if (active) {
            activeTabId = active.id
          }
        }
        // Fallback: if no active tab was persisted (or the persisted active
        // was pruned by the Go-side stale-tab check), activate the first
        // restored tab so the user sees a tab on launch instead of a blank
        // state. (#142 review: nil active_tab left displayedTabs empty.)
        if (!activeTabId && openTabs.length > 0) {
          activeTabId = openTabs[0].id
        }
        syncActiveFromTab()
        // Update the hot-reload baseline so this load doesn't immediately
        // trigger a re-hydrate cycle.
        prevOpenTabsKey = tabSetKey(
          result.open_tabs.map((t) => ({
            notebook: t.notebook,
            section: t.section,
            page: t.page
          }))
        )
      }
    } catch (e) {
      console.error('GetOpenTabs failed:', e)
    }
  }
  let showSearch = $state(false)
  let showSettings = $state(false)
  let settingsTab = $state('general')
  let showTemplatePicker = $state(false)
  let templatePickerMode = $state<'new-page' | 'insert'>('new-page')
  // F20: set when the backend emits settings:fingerprint-mismatch — the
  // trust-anchor fields (vault_path / trusted_publishers) changed since the
  // last launch. The modal asks the user to confirm or dismiss; confirm
  // clears the sentinel via ConfirmSettingsChange so the next launch is quiet.
  let showSettingsMismatch = $state(false)
  // F4: set when the backend detects a legacy grants: block in this vault's
  // config.yaml that the host has never seen. The modal asks the user to
  // confirm moving grants to per-host storage.
  let showGrantsMigration = $state(false)
  let pendingLegacyGrants = $state<Record<string, Record<string, string>>>({})
  // F3: quarantined linked notebooks (root_path moved or tampered). The modal
  // offers re-link (PickLinkedNotebook) or unlink (UnlinkNotebook).
  let quarantinedLinks = $state<
    { id: string; display_name: string; root_path: string }[]
  >([])

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
    // Restore the persisted open-tab set from config.yaml (#142).
    void loadPersistedTabs()
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

    // Throttled startup update check (#312): one quiet GitHub Releases lookup
    // per 24h. Silent on failure (AC5); raises a toast only when an update is
    // available (AC2). Runs independently of any vault being open.
    void initStartupUpdateCheck()

    // Hot-reload the plugin registry when an external config.yaml edit
    // changes plugins.disabled (e.g. the user hand-edits the file as
    // documented in docs/PLUGIN_DEVELOPMENT.md). Diff against the last
    // seen value so unrelated config changes (theme, hotkeys, etc.) do
    // not pay the ESM-import + plugin init cost.
    let prevDisabled: string[] = settings.config?.plugins?.disabled ?? []
    // Initialize the tab hot-reload baseline from the settings store.
    prevOpenTabsKey = tabSetKey(settings.config?.ui?.open_tabs)
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
        // Re-hydrate tabs if the external ui.open_tabs block changed
        // (user hand-edited config.yaml or another process wrote it).
        // tabSetKey is intentionally locator-only: a view-mode change must
        // NOT trigger a full re-hydrate (that would rebuild tabs and remount
        // editors on every in-app toggle, since the frontend's own
        // persistTabs write also fires config:changed).
        const nextTabsKey = tabSetKey(cfg?.ui?.open_tabs)
        if (nextTabsKey !== prevOpenTabsKey) {
          prevOpenTabsKey = nextTabsKey
          void loadPersistedTabs()
        }
        // Reconcile per-tab view_mode from an external config.yaml edit
        // in place — no re-hydrate, no editor remount. The frontend's own
        // writes match the in-memory state, so they produce no diff here;
        // only an external hand-edit (or another process) flips a mode.
        const externalTabs = cfg?.ui?.open_tabs ?? []
        if (externalTabs.length > 0) {
          for (const ref of externalTabs) {
            const tab = openTabs.find(
              (t) =>
                t.notebook === ref.notebook &&
                t.section === (ref.section ?? '') &&
                t.page === ref.page
            )
            if (!tab) continue
            const mode = ref.view_mode === 'source' ? 'source' : 'edit'
            if (tab.viewMode !== mode) {
              openTabs = setTabViewModeState(
                { tabs: openTabs, activeId: activeTabId },
                tab.id,
                mode
              ).tabs
              // Do NOT schedulePersistTabs — this change is already on disk.
            }
          }
        }
      }
    )

    function handleOpenSettings(e: Event) {
      const detail = (e as CustomEvent).detail
      openSettings(typeof detail === 'string' ? detail : 'general')
    }
    // Move keyboard focus into the active sidebar (#326 item 8). Expands the
    // sidebar if collapsed, then focuses the first focusable element inside it
    // (a tree node, a smart-list radio, a scope radio, or a search input —
    // whichever the active sidebar surfaces first). Ctrl+Shift+B is not a
    // format shortcut, so it fires globally even while the editor is focused.
    async function focusSidebar() {
      if (sidebarCollapsed) {
        sidebarCollapsed = false
        manuallyCollapsed = false
      }
      await tick()
      // One rAF so the expand's width transition has started and the target
      // is laid out before we focus it.
      requestAnimationFrame(() => {
        const aside = document.querySelector<HTMLElement>('[data-sidebar]')
        if (!aside) return
        const focusable = aside.querySelector<HTMLElement>(
          'button:not([disabled]), input:not([disabled]), [href], [tabindex]:not([tabindex="-1"])'
        )
        focusable?.focus()
      })
    }

    function handleGlobalKeyDown(e: KeyboardEvent) {
      // Config-driven global shortcuts. Read live from the settings store so
      // edits made in Settings → General take effect after Save (no rebind
      // needed — the store is a reactive proxy read at event time). Editor-
      // internal shortcuts (indent/unindent) are consumed by the editor's
      // own keydown handler; cycle_view_layout is global (it changes the
      // main view, not anything inside the contenteditable).
      const hotkeys = settings.config?.hotkeys ?? {}

      // If the editor (ProseMirror contenteditable) is focused, skip global
      // bindings that collide with editor format shortcuts (#168). The main
      // conflict is Ctrl+B (toggle_sidebar vs format_bold). ProseMirror
      // handles format_* shortcuts inside the contenteditable; the global
      // handler must not also fire.
      const target = e.target as HTMLElement | null
      if (target?.closest('.ProseMirror')) {
        // Skip any hotkey consumed inside the editor (format, heading,
        // alignment) so the global handler doesn't double-fire (#168, #169,
        // #173). toggle_view_mode is intentionally NOT in this list: no
        // editor-internal keymap handles it, so suppressing it left the view
        // toggle dead while typing. The global handler now flips the active
        // tab's mode directly regardless of editor focus (#171/#195).
        for (const [action, binding] of Object.entries(hotkeys)) {
          if (
            (action.startsWith('format_') ||
              action.startsWith('set_') ||
              action.startsWith('align_')) &&
            matchHotkey(e, binding)
          ) {
            return
          }
        }
      }

      if (matchHotkey(e, hotkeys.open_search)) {
        e.preventDefault()
        showSearch = !showSearch
      }
      if (matchHotkey(e, hotkeys.toggle_sidebar)) {
        e.preventDefault()
        sidebarCollapsed = !sidebarCollapsed
        manuallyCollapsed = sidebarCollapsed
      }
      if (matchHotkey(e, hotkeys.focus_sidebar)) {
        e.preventDefault()
        void focusSidebar()
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
      if (matchHotkey(e, hotkeys.toggle_view_mode)) {
        e.preventDefault()
        // Flip the active tab's view mode directly (#195) — no window-event
        // indirection, App owns the per-tab state.
        if (activeTabId) handleToggleViewMode(activeTabId)
      }
      if (matchHotkey(e, hotkeys.toggle_format_toolbar)) {
        e.preventDefault()
        void toggleFormatToolbar()
      }
      if (matchHotkey(e, hotkeys.toggle_focus_mode)) {
        e.preventDefault()
        void toggleFocusMode()
      }
      // Tab-strip hotkeys (#142). Ctrl+Tab / Ctrl+Shift+Tab cycle MRU;
      // Ctrl+W closes the active tab. All three are remappable / disable-
      // able (empty string) from Settings → General. No-op when 0 tabs.
      if (displayedTabs.length > 0) {
        if (matchHotkey(e, hotkeys.next_tab)) {
          e.preventDefault()
          handleCycleTab(1)
        }
        if (matchHotkey(e, hotkeys.prev_tab)) {
          e.preventDefault()
          handleCycleTab(-1)
        }
        if (matchHotkey(e, hotkeys.close_tab)) {
          e.preventDefault()
          // Guard: only close if the active tab is visible in the current
          // notebook's displayed set (#142 review: closing a hidden tab
          // from another notebook would be surprising to the user).
          if (activeTabId && displayedTabs.some((t) => t.id === activeTabId)) {
            handleCloseTab(activeTabId)
          }
        }
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
    function handlePageRenamed(e: Event) {
      const { notebook, section, oldName, newName } = (e as CustomEvent).detail as {
        notebook: string
        section: string
        oldName: string
        newName: string
      }
      openTabs = openTabs.map((t) =>
        t.notebook === notebook &&
        t.section === section &&
        t.page === oldName
          ? { ...t, page: newName }
          : t
      )
      if (activeNotebook === notebook && activeSection === section && activePage === oldName) {
        activePage = newName
      }
    }

    window.addEventListener('keydown', handleGlobalKeyDown)
    window.addEventListener('navigate-to-block', handleNavigateToBlock)
    window.addEventListener('navigate-to-tag', handleNavigateToTag)
    window.addEventListener('switch-view', handleSwitchView)
    window.addEventListener('open-plugin-manager', handleOpenPluginManager)
    window.addEventListener('open-settings', handleOpenSettings)
    window.addEventListener('open-template-picker', handleOpenTemplatePicker)
    window.addEventListener('silt:change-vault', handleSwitchVault)
    window.addEventListener('page-renamed', handlePageRenamed)
    // `plugins:changed` is a Wails event (Go runtime.EventsEmit), so it must
    // be received via EventsOn — a DOM addEventListener would never fire.
    const offPluginsChanged = EventsOn('plugins:changed', () =>
      handlePluginsChanged()
    )
    // `vault:moved` fires after a successful vault Move/Copy-Switch (#141).
    // The backend has already reinitialized services at the new path; reset
    // navigation, close settings, and reload the (vault-scoped) config store
    // so the UI reflects the new workspace. If the optional old-vault removal
    // didn't happen, payload.warning carries the reason → surface a non-
    // blocking toast (the move itself succeeded).
    const offVaultMoved = EventsOn(
      'vault:moved',
      (e: { from?: string; to?: string; warning?: string }) => {
        activeNotebook = ''
        activeSection = ''
        activePage = ''
        openTabs = []
        activeTabId = ''
        activeView = 'notes'
        showSettings = false
        loadConfig().catch((e) =>
          console.error('Post-move config reload failed:', e)
        )
        window.dispatchEvent(new CustomEvent('refresh-navigation'))
        if (e?.warning) {
          pushNotification({ kind: 'error', message: e.warning })
        }
      }
    )
    // F20: trust-anchor fingerprint mismatch — the backend detected that
    // vault_path or trusted_publishers changed since last launch (possible
    // tampering, or a legit external edit). Show a confirmation modal; the
    // user can confirm (clears the sentinel) or dismiss (mismatch persists
    // on next launch).
    const offSettingsMismatch = EventsOn(
      'settings:fingerprint-mismatch',
      () => {
        showSettingsMismatch = true
      }
    )
    // F4: grants migration — the vault's legacy config.yaml carries a grants
    // block this host has never seen. Show a one-time confirmation modal.
    const offGrantsMigration = EventsOn(
      'grants:migration-required',
      (grants: Record<string, Record<string, string>>) => {
        pendingLegacyGrants = grants
        showGrantsMigration = true
      }
    )
    // F3: linked-notebook quarantined — the root was moved or tampered with.
    // Refresh the quarantine list so the modal shows the latest set.
    const offLinkedQuarantined = EventsOn(
      'linked-notebook:quarantined',
      async () => {
        try {
          quarantinedLinks = await ResolveQuarantinedLinks()
        } catch (e) {
          console.error('ResolveQuarantinedLinks failed:', e)
        }
      }
    )
    return () => {
      window.removeEventListener('keydown', handleGlobalKeyDown)
      window.removeEventListener('navigate-to-block', handleNavigateToBlock)
      window.removeEventListener('navigate-to-tag', handleNavigateToTag)
      window.removeEventListener('switch-view', handleSwitchView)
      window.removeEventListener('open-plugin-manager', handleOpenPluginManager)
      window.removeEventListener('open-settings', handleOpenSettings)
      window.removeEventListener(
        'open-template-picker',
        handleOpenTemplatePicker
      )
      window.removeEventListener('silt:change-vault', handleSwitchVault)
      window.removeEventListener('page-renamed', handlePageRenamed)
      offPluginsChanged()
      offVaultMoved()
      offConfigChangedReload()
      offSettingsMismatch()
      offGrantsMigration()
      offLinkedQuarantined()
      disposeEditorTokens()
      disposeThemes()
      disposeTemplates()
      disposeUpdateStore()
      // Flush any pending tab-state persistence so the user's last tab
      // change survives a component unmount / app close (#142 hardening).
      if (persistTabsTimer) {
        clearTimeout(persistTabsTimer)
        persistTabsTimer = null
        void persistTabs()
      }
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
        // Restore the persisted tab set from config.yaml (#142).
        void loadPersistedTabs()
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
      // Clear the tab strip (#142).
      openTabs = []
      activeTabId = ''
    } catch (e) {
      console.error('Failed to close vault:', e)
    }
  }

  // Settings → Workspace → "Switch vault…" entry. Closes the settings overlay
  // then runs the same tear-down flow as the (removed) sidebar Change Vault
  // button, returning the user to the onboarding screen to pick a vault.
  async function handleSwitchVault() {
    showSettings = false
    await handleChangeVault()
  }

  function handleSearchJump(
    notebook: string,
    section: string,
    page: string,
    date: string,
    blockId: string
  ) {
    // Route through openPage (preview-tab semantics, #142).
    // Use activate-only when the target IS the active page so block
    // navigation does not re-bump the MRU timestamp (the state machine's
    // activate-only path is a true no-op on tab state, just sets the
    // scroll-to-block target). Otherwise open in preview mode.
    const activeTab = openTabs.find((t) => t.id === activeTabId)
    const isSamePage =
      activeTab &&
      activeTab.notebook === notebook &&
      activeTab.section === section &&
      activeTab.page === page
    openPage(
      { notebook, section, page },
      isSamePage ? 'activate-only' : 'preview',
      { fileDate: date, blockId }
    )
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
  // With tabs (#142), also requires an active tab so closing the last tab
  // returns to the blank view. displayedTabs ensures per-notebook scoping.
  let notesReady = $derived(
    activeView === 'notes' &&
      !!activeNotebook &&
      !!activePage &&
      !!activeTabId &&
      displayedTabs.length > 0
  )

  $effect(() => {
    console.log('[Silt] notesReady:', notesReady,
      '| activeView:', activeView,
      '| notebook:', activeNotebook,
      '| section:', activeSection,
      '| page:', activePage,
      '| activeTabId:', activeTabId,
      '| displayedTabs:', displayedTabs.length,
      '| openTabs:', openTabs.length)
  })

  function openSettings(tab: string = '') {
    settingsTab = tab || 'general'
    showSettings = true
  }

  // Ordered view cycle for the cycle_view_layout hotkey (default Ctrl+Alt+V).
  // If the current view is not in the list (e.g. a plugin view), jump to
  // 'notes' as the anchor.
  function cycleView() {
    activeView = nextView(activeView)
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

  // Stable serialization of the persisted open_tabs list for change detection.
  // The config:changed handler compares the previous and next keys to decide
  // whether to re-hydrate the tab strip on an external config.yaml edit.
  function tabSetKey(
    tabs: { notebook?: string; section?: string; page?: string }[] | undefined
  ): string {
    if (!tabs || tabs.length === 0) return ''
    return tabs
      .map(
        (t) => `${t.notebook ?? ''}\x00${t.section ?? ''}\x00${t.page ?? ''}`
      )
      .sort()
      .join('|')
  }
</script>

<main
  class="w-full h-full flex flex-col bg-void text-text-primary overflow-hidden font-body-md"
>
  {#if loading || !isInitialized}
    <Onboarding
      {loading}
      initialized={isInitialized}
      onSelectFolder={handleSelectFolder}
    />
  {:else}
    <TitleBar
      bind:sidebarCollapsed
      {sidebarWidth}
      onSearchClick={() => (showSearch = true)}
    >
      {#if activeView === 'notes'}
        <TabStrip
          tabs={displayedTabs}
          {activeTabId}
          onSelectTab={handleSelectTab}
          onCloseTab={handleCloseTab}
          onPromoteTab={handlePromoteTab}
          onReorderTab={handleReorderTab}
          showDirtyIndicators={settings.config?.ui
            ?.show_tab_dirty_indicators !== false}
        />
      {:else}
        <div
          class="flex items-center px-4 py-1 text-text-muted text-[11px] uppercase tracking-widest font-label-sm-bold"
        >
          {activeView}
        </div>
      {/if}
    </TitleBar>

    <div class="flex mt-14 h-[calc(100vh-56px)] w-full relative">
      <!-- Activity Bar -->
      <div
        class="w-12 bg-surface border-r border-border-muted flex flex-col items-center py-4 justify-between h-full select-none z-50 flex-shrink-0"
      >
        <div class="flex flex-col gap-4 items-center w-full">
          {#each views as v (v.id)}
            <button
              onclick={() => {
                if (activeView === v.id) {
                  sidebarCollapsed = !sidebarCollapsed
                  manuallyCollapsed = sidebarCollapsed
                } else {
                  activeView = v.id
                  sidebarCollapsed = false
                  manuallyCollapsed = false
                }
              }}
              class="relative w-9 h-9 rounded-lg flex items-center justify-center transition-all cursor-pointer border-none bg-transparent hover:bg-hover hover:scale-105 active:scale-95 group focus:outline-none"
              class:text-accent-primary-start={activeView === v.id &&
                !sidebarCollapsed}
              class:text-text-muted={activeView !== v.id || sidebarCollapsed}
              aria-label={v.label}
              aria-pressed={activeView === v.id}
              title={v.label}
            >
              {#if activeView === v.id && !sidebarCollapsed}
                <div
                  class="absolute left-0 top-2 bottom-2 w-0.5 bg-accent-primary-start rounded-full shadow-[0_0_8px_var(--color-accent-primary-start)]"
                ></div>
              {/if}
              <span class="material-symbols-outlined text-[20px]">{v.icon}</span
              >
            </button>
          {/each}
        </div>

        <button
          onclick={() => openSettings('workspace')}
          class="w-9 h-9 rounded-lg flex items-center justify-center text-text-muted hover:text-accent-primary-start hover:bg-hover hover:scale-105 active:scale-95 transition-all cursor-pointer border-none bg-transparent focus:outline-none"
          aria-label="Settings"
          title="Settings"
        >
          <span class="material-symbols-outlined text-[20px]">settings</span>
        </button>
      </div>

      {#if sidebarCollapsed}
        <button
          onclick={() => {
            sidebarCollapsed = false
            manuallyCollapsed = false
          }}
          transition:fade={{ duration: 150 }}
          aria-label="Show sidebar"
          title="Show sidebar (Ctrl+B)"
          class="absolute bottom-4 left-16 z-50 w-8 h-8 rounded-lg bg-surface/80 backdrop-blur-md border border-border-muted text-text-muted hover:text-accent-primary-start hover:border-accent-primary-start/40 flex items-center justify-center transition-all cursor-pointer shadow-lg hover:scale-105 active:scale-95"
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
        bind:selectedTag
        bind:collapsed={sidebarCollapsed}
        {sidebarWidth}
        {sidebarDragging}
        onSelectNotebook={(nb) => {
          activeNotebook = nb
          // Per-notebook tab scoping: activate the MRU tab for the new
          // notebook (or clear if none exist for it).
          const notebookTabs = openTabs
            .filter((t) => t.notebook === nb)
            .sort((a, b) => b.lastActivatedAt - a.lastActivatedAt)
          if (notebookTabs.length > 0) {
            activeTabId = notebookTabs[0].id
          } else {
            activeTabId = ''
          }
          syncActiveFromTab()
        }}
        onSelectSection={(sec) => (activeSection = sec)}
        onSelectPage={(nb, sec, pg) => {
          // Single-click opens in preview mode (industry-standard parity, #142).
          openPage({ notebook: nb, section: sec, page: pg }, 'preview')
        }}
        onPinPage={(nb, sec, pg) => {
          // Double-click / middle-click opens a pinned tab (#142).
          openPage({ notebook: nb, section: sec, page: pg }, 'pin')
        }}
        onSelectView={(v) => (activeView = v)}
        onPageMoved={(nb, fromSection, toSection, page) => {
          // A page was dragged across sections in the sidebar (#177). Update
          // the open tab for this specific page+section so its section field
          // points to the new location. Matching on fromSection is critical —
          // without it, a same-named sibling in another section would also be
          // repointed, causing its next save to write to the wrong path.
          openTabs = openTabs.map((t) =>
            t.notebook === nb && t.section === fromSection && t.page === page
              ? { ...t, section: toSection }
              : t
          )
          if (
            activeNotebook === nb &&
            activePage === page &&
            activeSection === fromSection
          ) {
            activeSection = toSection
          }
          schedulePersistTabs()
        }}
      />

      {#if !sidebarCollapsed}
        <SidebarResizeHandle
          width={sidebarWidth}
          onWidthChange={handleSidebarWidthChange}
          onWidthCommit={handleSidebarWidthCommit}
        />
      {/if}

      <!-- Content viewport -->
      <div class="flex-1 h-full min-w-0 flex flex-col overflow-hidden bg-void">
        {#if settings.config?.ui?.open_devtools_on_startup === true}
          <div class="absolute bottom-2 left-1/2 -translate-x-1/2 z-[999] bg-red-600 text-white text-[10px] font-mono px-2 py-1 rounded opacity-80 pointer-events-none">
            view={activeView} nb={activeNotebook || '-'} pg={activePage || '-'} tab={activeTabId || '-'} dt={displayedTabs.length} nr={notesReady}
          </div>
        {/if}
        {#if activeView === 'notes'}
          {#if notesReady}
            <div
              id="silt-tabpanel"
              role="tabpanel"
              aria-labelledby="silt-tab-{activeTabId}"
              class="flex-1 min-h-0 flex flex-col overflow-hidden"
            >
              {#each displayedTabs as tab (tab.id)}
                <div
                  class="flex-1 min-h-0 flex flex-col overflow-hidden"
                  style:display={tab.id === activeTabId ? 'flex' : 'none'}
                >
                  <VirtualScrollContainer
                    notebook={tab.notebook}
                    section={tab.section}
                    page={tab.page}
                    viewMode={tab.viewMode}
                    onToggleViewMode={() => handleToggleViewMode(tab.id)}
                    isActive={tab.id === activeTabId}
                    targetBlockId={tab.id === activeTabId
                      ? searchTargetBlockId
                      : ''}
                    targetKey={tab.id === activeTabId ? searchTargetKey : ''}
                    activeFocusedBlockAncestors={tab.id === activeTabId
                      ? activeFocusedBlockAncestors
                      : []}
                    onBlockFocus={tab.id === activeTabId
                      ? handleBlockFocus
                      : undefined}
                    onBlockBlur={tab.id === activeTabId
                      ? handleBlockBlur
                      : undefined}
                    onPageRenamed={(newName) => {
                      // Update the tab's page name AND the active triple.
                      openTabs = openTabs.map((t) =>
                        t.id === tab.id ? { ...t, page: newName } : t
                      )
                      if (tab.id === activeTabId) activePage = newName
                    }}
                    onFirstEdit={tab.preview
                      ? () => handlePromoteTab(tab.id)
                      : undefined}
                    onSaveStateChange={(s) => {
                      // Surface the editor's save state on the tab header
                      // so it's visible from any tab (#167).
                      openTabs = openTabs.map((t) =>
                        t.id === tab.id
                          ? { ...t, dirty: s.dirty, saveError: s.error }
                          : t
                      )
                    }}
                  />
                </div>
              {/each}
            </div>
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
                {#if openTabs.length > 0 && !activeTabId}
                  No active tab — click a tab above to switch
                {:else if !activeNotebook}
                  Create or open a notebook to begin
                {:else if openTabs.length === 0}
                  No pages open
                {:else}
                  Select or create a page
                {/if}
              </h2>
              <p class="text-text-muted font-body-md max-w-md">
                {#if openTabs.length === 0}
                  Click a page in the sidebar to open it in a tab. Single-click
                  opens a preview; double-click opens a pinned tab.
                {:else}
                  Silt organizes notes as Notebook › Section › Page. Use the
                  sidebar navigator to create your first notebook, then add a
                  section and a page to start writing.
                {/if}
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

  {#if showSettingsMismatch}
    <div
      class="settings-mismatch-overlay"
      role="alertdialog"
      aria-modal="true"
      aria-labelledby="settings-mismatch-title"
      aria-describedby="settings-mismatch-desc"
      tabindex="-1"
      onkeydown={(e) => {
        if (e.key === 'Escape') showSettingsMismatch = false
      }}
      transition:fade={{ duration: 150 }}
    >
      <div class="settings-mismatch-modal">
        <h2 id="settings-mismatch-title">Settings changed</h2>
        <p id="settings-mismatch-desc">
          Silt's vault path or trusted-publishers list has changed since the
          last launch. Confirm this change is intentional. If you did not make
          this change, dismiss and verify your <code>settings.json</code>.
        </p>
        <div class="settings-mismatch-actions">
          <button
            class="secondary"
            onclick={() => (showSettingsMismatch = false)}>Dismiss</button
          >
          <button
            class="primary"
            onclick={async () => {
              try {
                await ConfirmSettingsChange()
                showSettingsMismatch = false
              } catch (e) {
                pushNotification({
                  kind: 'error',
                  message: `Failed to confirm settings change: ${e}`
                })
              }
            }}>Confirm change</button
          >
        </div>
      </div>
    </div>
  {/if}

  {#if showGrantsMigration}
    <div
      class="settings-mismatch-overlay"
      role="alertdialog"
      aria-modal="true"
      aria-labelledby="grants-migration-title"
      aria-describedby="grants-migration-desc"
      tabindex="-1"
      onkeydown={async (e) => {
        if (e.key === 'Escape') {
          try {
            await DeclineGrantsMigration()
          } catch (err) {
            console.error('DeclineGrantsMigration failed:', err)
          }
          showGrantsMigration = false
        }
      }}
      transition:fade={{ duration: 150 }}
    >
      <div class="settings-mismatch-modal">
        <h2 id="grants-migration-title">Move plugin permissions</h2>
        <p id="grants-migration-desc">
          Silt is moving plugin permissions to per-host storage so they no
          longer travel with synced vaults. {Object.keys(pendingLegacyGrants)
            .length}{' '}
          plugin(s) have existing permissions in this vault. Confirm to move them,
          or dismiss to re-grant each plugin on first use.
        </p>
        <div class="settings-mismatch-actions">
          <button
            class="secondary"
            onclick={async () => {
              try {
                await DeclineGrantsMigration()
              } catch (e) {
                console.error('DeclineGrantsMigration failed:', e)
              }
              showGrantsMigration = false
            }}>Dismiss</button
          >
          <button
            class="primary"
            onclick={async () => {
              try {
                await ConfirmGrantsMigration(pendingLegacyGrants)
                showGrantsMigration = false
              } catch (e) {
                pushNotification({
                  kind: 'error',
                  message: `Failed to move plugin permissions: ${e}`
                })
              }
            }}>Move permissions</button
          >
        </div>
      </div>
    </div>
  {/if}

  {#if quarantinedLinks.length > 0}
    <div
      class="settings-mismatch-overlay"
      role="alertdialog"
      aria-modal="true"
      aria-labelledby="quarantine-title"
      aria-describedby="quarantine-desc"
      tabindex="-1"
      onkeydown={(e) => {
        if (e.key === 'Escape') quarantinedLinks = []
      }}
      transition:fade={{ duration: 150 }}
    >
      <div class="settings-mismatch-modal">
        <h2 id="quarantine-title">Linked notebook moved or tampered</h2>
        <p id="quarantine-desc">
          {#each quarantinedLinks as q (q.id)}
            <strong>{q.display_name}</strong> has moved or been tampered with. Re-link
            it or unlink it.
          {/each}
        </p>
        <div class="settings-mismatch-actions">
          {#each quarantinedLinks as q (q.id)}
            <button
              class="secondary"
              onclick={async () => {
                try {
                  await UnlinkNotebook(q.id)
                  quarantinedLinks = quarantinedLinks.filter(
                    (l) => l.id !== q.id
                  )
                } catch (e) {
                  pushNotification({
                    kind: 'error',
                    message: `Failed to unlink ${q.display_name}: ${e}`
                  })
                }
              }}>Unlink {q.display_name}</button
            >
            <button
              class="primary"
              onclick={async () => {
                try {
                  await UnlinkNotebook(q.id)
                  await PickLinkedNotebook()
                  quarantinedLinks = quarantinedLinks.filter(
                    (l) => l.id !== q.id
                  )
                } catch (e) {
                  pushNotification({
                    kind: 'error',
                    message: `Failed to re-link ${q.display_name}: ${e}`
                  })
                }
              }}>Re-link {q.display_name}</button
            >
          {/each}
        </div>
      </div>
    </div>
  {/if}

  <!-- Plugin rendered-UI surfaces (#117) -->
  <PluginModalHost />
</main>

<ToastContainer />
<PluginStatusBar />

<style>
  .settings-mismatch-overlay {
    position: fixed;
    inset: 0;
    z-index: 10000;
    display: flex;
    align-items: center;
    justify-content: center;
    background: rgba(0, 0, 0, 0.6);
    backdrop-filter: blur(4px);
  }

  .settings-mismatch-modal {
    max-width: 460px;
    padding: 28px 32px;
    border-radius: 12px;
    background: var(--color-surface, #1a1a1e);
    border: 1px solid var(--color-border, rgba(255, 255, 255, 0.1));
    box-shadow: 0 16px 48px rgba(0, 0, 0, 0.4);
  }

  .settings-mismatch-modal h2 {
    margin: 0 0 12px;
    font-size: 1.15rem;
    color: var(--color-text, #e0e0e0);
  }

  .settings-mismatch-modal p {
    margin: 0 0 20px;
    font-size: 0.9rem;
    line-height: 1.5;
    color: var(--color-text-muted, #999);
  }

  .settings-mismatch-modal code {
    padding: 1px 4px;
    border-radius: 3px;
    background: rgba(255, 255, 255, 0.08);
    font-family: var(--font-mono, monospace);
    font-size: 0.85em;
  }

  .settings-mismatch-actions {
    display: flex;
    justify-content: flex-end;
    gap: 10px;
  }

  .settings-mismatch-actions button {
    padding: 8px 18px;
    border: none;
    border-radius: 8px;
    font-size: 0.875rem;
    cursor: pointer;
    transition: opacity 0.15s;
  }

  .settings-mismatch-actions button:hover {
    opacity: 0.85;
  }

  .settings-mismatch-actions .secondary {
    background: transparent;
    color: var(--color-text-muted, #999);
    border: 1px solid var(--color-border, rgba(255, 255, 255, 0.15));
  }

  .settings-mismatch-actions .primary {
    background: var(--color-accent-primary-start, #4a9eff);
    color: #fff;
    font-weight: 600;
  }
</style>
