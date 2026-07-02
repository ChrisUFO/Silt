<script lang="ts">
  import { onMount, onDestroy } from 'svelte'
  import WorkspaceTab from './WorkspaceTab.svelte'
  import EditorTab from './EditorTab.svelte'
  import HotkeysTab from './HotkeysTab.svelte'
  import AppearanceTab from './AppearanceTab.svelte'
  import AboutTab from './AboutTab.svelte'
  import PluginsTab from './PluginsTab.svelte'
  import DevTab from './DevTab.svelte'
  import PluginSettingsPanel from './PluginSettingsPanel.svelte'
  import { loadConfig, settings } from '../../settings/store.svelte'
  import { loadPlugins } from '../../plugins/loader'
  import { loadedPlugins } from '../../plugins/store.svelte'
  import { getSurfaces, onSurfacesChanged } from '../../plugins/surfaces'
  import type { RegisteredPlugin } from '../../plugins/sdk'

  interface Props {
    activeTab?: string
    onClose: () => void
    activeNotebook: string
    activeSection: string
    activePage: string
  }

  let {
    activeTab = $bindable('workspace'),
    onClose,
    activeNotebook,
    activeSection,
    activePage
  }: Props = $props()

  let devMode = $derived(settings.config?.ui?.open_devtools_on_startup === true)

  // #214: plugin-contributed settings tabs. A plugin contributes a tab when it
  // has a bespoke settings page — either a first-party settingsPageComponent
  // (compiled Svelte) or a registered 'settings-panel' iframe surface
  // (third-party). Disabled plugins are excluded: they are not in
  // loadedPlugins.plugins and their surfaces are not registered.
  let settingsPanelSurfaces = $state(getSurfaces('settings-panel'))
  const offSurfacesChanged = onSurfacesChanged((all) => {
    settingsPanelSurfaces = all.filter((s) => s.kind === 'settings-panel')
  })
  onDestroy(() => offSurfacesChanged())

  // Plugins with a bespoke settings page (first-party via component, third-party
  // via surface). Tab id is `plugin:<id>` so it never collides with core tabs.
  let pluginSettingsTabs = $derived.by(() => {
    const tabs: {
      id: string
      label: string
      icon: string
      plugin: RegisteredPlugin
    }[] = []
    // First-party: compiled Svelte component.
    for (const plugin of loadedPlugins.plugins.values()) {
      if (plugin.settingsPageComponent) {
        tabs.push({
          id: `plugin:${plugin.manifest.id}`,
          label: plugin.manifest.name,
          icon: plugin.manifest.icon ?? 'tune',
          plugin
        })
      }
    }
    // Third-party: 'settings-panel' iframe surface. Group by pluginID; a plugin
    // may register one settings surface.
    const seen = new Set(tabs.map((t) => t.plugin.manifest.id))
    for (const surface of settingsPanelSurfaces) {
      if (seen.has(surface.pluginID)) continue
      const plugin = loadedPlugins.plugins.get(surface.pluginID)
      if (plugin) {
        tabs.push({
          id: `plugin:${surface.pluginID}`,
          label: plugin.manifest.name,
          icon: plugin.manifest.icon ?? 'tune',
          plugin
        })
        seen.add(surface.pluginID)
      }
    }
    return tabs
  })

  const tabs = $derived([
    { id: 'workspace', label: 'Workspace', icon: 'folder' },
    { id: 'editor', label: 'Editor', icon: 'edit_note' },
    { id: 'appearance', label: 'Appearance', icon: 'palette' },
    { id: 'hotkeys', label: 'Hotkeys', icon: 'keyboard' },
    { id: 'plugins', label: 'Plugins', icon: 'extension' },
    ...pluginSettingsTabs.map((t) => ({
      id: t.id,
      label: t.label,
      icon: t.icon
    })),
    ...(devMode ? [{ id: 'dev' as const, label: 'Dev', icon: 'code' }] : []),
    { id: 'about', label: 'About', icon: 'info' }
  ])

  let railRefs: HTMLButtonElement[] = $state([])
  let dialogEl: HTMLDivElement | null = $state(null)
  let previouslyFocused: HTMLElement | null = null

  // Selector for all focusable descendants of the dialog (for the Tab trap).
  const FOCUSABLE =
    'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

  function focusableElements(): HTMLElement[] {
    if (!dialogEl) return []
    return Array.from(dialogEl.querySelectorAll<HTMLElement>(FOCUSABLE))
  }

  onMount(() => {
    // Capture the element that had focus before the dialog opened so it can be
    // restored on close (standard modal a11y behaviour).
    previouslyFocused = document.activeElement as HTMLElement
    loadConfig().catch((e) => console.error('loadConfig failed:', e))
    // Move focus into the dialog (the active tab button).
    queueMicrotask(() => {
      const idx = tabs.findIndex((t) => t.id === activeTab)
      railRefs[Math.max(0, idx)]?.focus()
    })
    return () => {
      previouslyFocused?.focus?.()
    }
  })

  function selectTab(id: string) {
    activeTab = id
    const idx = tabs.findIndex((t) => t.id === id)
    railRefs[idx]?.focus()
  }

  function handleRailKeydown(e: KeyboardEvent) {
    const idx = tabs.findIndex((t) => t.id === activeTab)
    if (e.key === 'ArrowDown' || e.key === 'ArrowRight') {
      e.preventDefault()
      selectTab(tabs[(idx + 1) % tabs.length].id)
    } else if (e.key === 'ArrowUp' || e.key === 'ArrowLeft') {
      e.preventDefault()
      selectTab(tabs[(idx - 1 + tabs.length) % tabs.length].id)
    } else if (e.key === 'Home') {
      e.preventDefault()
      selectTab(tabs[0].id)
    } else if (e.key === 'End') {
      e.preventDefault()
      selectTab(tabs[tabs.length - 1].id)
    }
  }

  // Esc closes and Tab is trapped inside the dialog (modal focus management).
  function handleKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      e.preventDefault()
      e.stopPropagation()
      onClose()
      return
    }
    if (e.key === 'Tab' && dialogEl) {
      const els = focusableElements()
      if (els.length === 0) return
      const first = els[0]
      const last = els[els.length - 1]
      const active = document.activeElement as HTMLElement | null
      if (e.shiftKey) {
        if (active === first || !dialogEl.contains(active)) {
          e.preventDefault()
          last.focus()
        }
      } else {
        if (active === last) {
          e.preventDefault()
          first.focus()
        }
      }
    }
  }

  // Reload plugin list when entering the Plugins tab so installs/enables done
  // elsewhere are reflected.
  let lastPlugins = ''
  $effect(() => {
    if (activeTab === 'plugins' && lastPlugins !== 'plugins') {
      loadPlugins(activeNotebook, activeSection, activePage).catch((e) =>
        console.error('Plugin reload failed:', e)
      )
    }
    lastPlugins = activeTab
  })

  // Map legacy 'general' tab selection to the new 'workspace' tab
  $effect(() => {
    if (activeTab === 'general') {
      activeTab = 'workspace'
    }
  })

  // #214: if the active plugin tab disappears (plugin disabled/uninstalled/
  // surface unregistered while its settings tab is open), reset to the
  // Plugins tab so the panel is never blank with an orphaned activeTab id.
  $effect(() => {
    if (
      activeTab.startsWith('plugin:') &&
      !tabs.some((t) => t.id === activeTab)
    ) {
      activeTab = 'plugins'
    }
  })
</script>

<svelte:window on:keydown={handleKeydown} />

<!-- Positioning wrapper (no interaction handler, so no a11y warning). -->
<div class="fixed inset-0 z-[200] flex items-center justify-center p-6">
  <!-- Scrim: a real button so it is keyboard/AT-accessible; tabindex=-1 keeps
       it out of the tab order (focus stays inside the dialog; Esc also closes). -->
  <button
    type="button"
    tabindex="-1"
    aria-label="Close settings"
    title="Close settings"
    onclick={onClose}
    class="absolute inset-0 h-full w-full bg-[#000]/40 backdrop-blur-[2px] border-none cursor-default p-0"
  ></button>
  <div
    bind:this={dialogEl}
    role="dialog"
    aria-modal="true"
    aria-label="Settings"
    tabindex="-1"
    class="relative z-10 w-full max-w-4xl h-[80vh] glass-palette border border-border-zinc rounded-xl shadow-2xl overflow-hidden flex"
    style="backdrop-filter: blur(16px) saturate(140%); background: color-mix(in srgb, var(--color-panel) 94%, transparent);"
  >
    <!-- Left rail: tab list -->
    <nav
      class="w-52 flex-shrink-0 border-r border-border-muted bg-surface/40 flex flex-col py-3"
      aria-label="Settings sections"
    >
      {#each tabs as tab, i (tab.id)}
        <button
          bind:this={railRefs[i]}
          onclick={() => selectTab(tab.id)}
          onkeydown={handleRailKeydown}
          role="tab"
          aria-selected={activeTab === tab.id}
          tabindex={activeTab === tab.id ? 0 : -1}
          class="relative flex items-center gap-3 pl-5 pr-4 py-2.5 mx-2 rounded-lg font-label-sm text-label-sm transition-all border-none cursor-pointer text-left focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-primary-start/60 {activeTab ===
          tab.id
            ? 'bg-white/5 text-accent-primary-start'
            : 'text-text-muted hover:bg-white/[0.02] hover:text-text-primary'}"
        >
          <!-- Left rail glowing line -->
          {#if activeTab === tab.id}
            <div
              class="absolute left-1.5 top-2.5 bottom-2.5 w-0.5 rounded-full bg-accent-primary-start shadow-[0_0_8px_var(--color-accent-primary-start)]"
            ></div>
          {/if}
          <span class="material-symbols-outlined text-[20px]">{tab.icon}</span>
          {tab.label}
        </button>
      {/each}
    </nav>

    <!-- Right: active panel -->
    <div class="flex-1 min-w-0 flex flex-col overflow-hidden bg-void/5">
      <div
        class="flex items-center justify-between px-6 py-4 border-b border-border-muted flex-shrink-0"
      >
        <h2
          class="font-headline-md text-headline-md text-text-primary capitalize"
        >
          {tabs.find((t) => t.id === activeTab)?.label}
        </h2>
        <button
          onclick={onClose}
          aria-label="Close settings"
          title="Close (Esc)"
          class="text-text-muted hover:text-text-primary border-none bg-transparent cursor-pointer p-1.5 rounded transition-colors focus:outline-none"
        >
          <span class="material-symbols-outlined text-[20px]">close</span>
        </button>
      </div>

      <div
        class="flex-1 min-h-0"
        class:overflow-y-auto={['appearance', 'plugins', 'about'].includes(
          activeTab
        ) || activeTab.startsWith('plugin:')}
        class:custom-scrollbar={['appearance', 'plugins', 'about'].includes(
          activeTab
        ) || activeTab.startsWith('plugin:')}
        class:flex={['workspace', 'editor', 'hotkeys'].includes(activeTab)}
        class:flex-col={['workspace', 'editor', 'hotkeys'].includes(activeTab)}
        class:overflow-hidden={['workspace', 'editor', 'hotkeys'].includes(
          activeTab
        )}
      >
        {#if settings.loading && !settings.config}
          <div class="p-8 text-text-muted animate-pulse font-body-md">
            Loading settings…
          </div>
        {:else if !settings.config && settings.error}
          <div class="p-8">
            <div
              class="flex items-start gap-2 p-3 rounded-lg bg-error/10 border border-error/30 text-error text-[12px] font-body-md max-w-xl"
            >
              <span class="material-symbols-outlined text-[18px]">error</span>
              <span class="flex-1">{settings.error}</span>
            </div>
          </div>
        {:else if activeTab === 'workspace'}
          <WorkspaceTab />
        {:else if activeTab === 'editor'}
          <EditorTab />
        {:else if activeTab === 'appearance'}
          <AppearanceTab />
        {:else if activeTab === 'hotkeys'}
          <HotkeysTab />
        {:else if activeTab === 'plugins'}
          <PluginsTab
            {activeNotebook}
            {activeSection}
            {activePage}
            onSwitchTab={(id) => selectTab(id)}
          />
        {:else if activeTab.startsWith('plugin:')}
          {@const pluginTab = pluginSettingsTabs.find(
            (t) => t.id === activeTab
          )}
          {#if pluginTab}
            <PluginSettingsPanel
              plugin={pluginTab.plugin}
              {activeNotebook}
              {activeSection}
              {activePage}
            />
          {/if}
        {:else if activeTab === 'dev'}
          <DevTab />
        {:else if activeTab === 'about'}
          <AboutTab />
        {/if}
      </div>
    </div>
  </div>
</div>
