<script lang="ts">
  import { onMount } from 'svelte'
  import { fade } from 'svelte/transition'
  import {
    ListPlugins,
    ValidatePluginArchive,
    InstallPlugin,
    UninstallPlugin,
    EnablePlugin,
    DisablePlugin,
    PickPluginArchive,
    RequestCapability,
    RevokeCapability,
    GetGrantedCapabilities,
    GetNetworkAudit,
    CheckPluginUpdate
  } from '../../../wailsjs/go/main/App.js'
  import { loadPlugins, teardownPlugin } from '../../plugins/loader'
  import { firstPartyPlugins } from '../../plugins/registry'
  import { loadedPlugins } from '../../plugins/store.svelte'
  import { getSurfaces } from '../../plugins/surfaces'
  import { settings, saveConfig } from '../../settings/store.svelte'
  import SettingsForm from './SettingsForm.svelte'
  import NetworkAuditViewer from './NetworkAuditViewer.svelte'
  import type { SettingSchema } from '../../plugins/sdk'

  interface Props {
    activeNotebook: string
    activeSection: string
    activePage: string
    onSwitchTab?: (tabId: string) => void
  }
  let { activeNotebook, activeSection, activePage, onSwitchTab }: Props =
    $props()

  interface Card {
    id: string
    name: string
    version: string
    author: string
    description: string
    icon: string
    source: 'first-party' | 'disk'
    disabled: boolean // disk plugins only
    hasIndex: boolean
    loadError?: string
    /** Capabilities requested by the manifest (#113): cap id → qualifier (true | "notebook" | "vault"). */
    requestedCapabilities?: Record<string, true | string>
    /** Capabilities currently granted to this plugin (cap id → qualifier). */
    grantedCapabilities?: Record<string, string>
    /** Declarative settings schema (#103), read from the manifest. */
    settingsSchema?: SettingSchema[]
    /** Optional update URL for distribution-v2 update checks (#111). */
    updateUrl?: string
    /** True when a newer version is available (#111). */
    updateAvailable?: boolean
  }

  /** Human label for a capability id. */
  const capabilityLabels: Record<string, string> = {
    'read-files': 'Read notebook files',
    'write-files': 'Write notebook files',
    network: 'Network access',
    'os-open': 'Open files / URLs',
    'os-clipboard': 'Clipboard',
    'os-notify': 'Notifications',
    'ui-surface': 'Render UI surfaces',
    'editor-schema': 'Extend the editor'
  }

  function qualifierLabel(q: true | string): string {
    if (q === true || q === 'granted' || q === '') return ''
    return ` (${q})`
  }

  let cards = $state<Card[]>([])
  let loading = $state(true)
  let expanded = $state<string | null>(null)
  let grantBusy = $state<string>('') // "<pluginId>:<cap>" while a grant/revoke is in flight

  // Install flow state.
  let installing = $state(false)
  let preview = $state<any>(null)
  let previewError = $state('')
  let pendingPath = $state('')
  let actionError = $state('')

  async function refresh() {
    loading = true
    actionError = ''
    try {
      const disk = (await ListPlugins()) ?? []
      const fps = firstPartyPlugins()
      const fpIds = new Set(fps.map((p) => p.manifest.id))
      const errs = loadedPlugins.errors
      // v2 capability grants (#113): pluginID → cap → qualifier. First-party
      // plugins are not surfaced here (they are implicitly granted).
      const grants = (await GetGrantedCapabilities()) ?? {}

      const merged: Card[] = []

      // First-party plugins (disableable via config, never uninstallable).
      const fpDisabled = new Set<string>(
        settings.config?.plugins?.disabled ?? []
      )
      for (const fp of fps) {
        const m = fp.manifest as any
        merged.push({
          id: m.id,
          name: m.name || m.id,
          version: m.version || '—',
          author: m.author || 'Silt',
          description: m.description || '',
          icon: m.icon || 'extension',
          source: 'first-party',
          disabled: fpDisabled.has(m.id),
          hasIndex: true,
          requestedCapabilities: m.capabilities,
          grantedCapabilities: m.capabilities
            ? Object.fromEntries(
                Object.keys(m.capabilities).map((c) => [c, 'granted'])
              )
            : undefined,
          loadError: errs.find((e) => e.id === m.id)?.message,
          settingsSchema: m.settings as SettingSchema[] | undefined
        })
      }
      // On-disk plugins (skip any shadowed by a first-party id).
      for (const p of disk as any[]) {
        if (fpIds.has(p.id)) continue
        merged.push({
          id: p.id,
          name: p.name || p.id,
          version: p.version || '—',
          author: p.author || '',
          description: p.description || '',
          icon: p.icon || 'extension',
          source: 'disk',
          disabled: !!p.disabled,
          hasIndex: !!p.has_index,
          requestedCapabilities: p.capabilities,
          grantedCapabilities: grants[p.id],
          loadError: errs.find((e) => e.id === p.id)?.message,
          settingsSchema: p.settings as SettingSchema[] | undefined,
          updateUrl: p.update_url || undefined
        })
      }
      merged.sort((a, b) => a.name.localeCompare(b.name))
      cards = merged
    } catch (e) {
      actionError = e instanceof Error ? e.message : String(e)
      cards = []
    } finally {
      loading = false
    }
  }

  async function checkForUpdates() {
    actionError = ''
    for (const card of cards) {
      if (!card.updateUrl || card.source !== 'disk') continue
      try {
        const info = await CheckPluginUpdate(
          card.id,
          card.version,
          card.updateUrl
        )
        if (info?.updateAvailable) {
          card.updateAvailable = true
        }
      } catch {
        // best-effort — network errors are non-fatal for update checks
      }
    }
    cards = [...cards]
  }

  /** Whether a capability is currently granted on a card. */
  function isGranted(card: Card, cap: string): boolean {
    return !!card.grantedCapabilities?.[cap]
  }

  async function grant(card: Card, cap: string) {
    grantBusy = `${card.id}:${cap}`
    actionError = ''
    try {
      const qual = card.requestedCapabilities?.[cap]
      const qualStr = typeof qual === 'string' ? qual : ''
      await RequestCapability(card.id, cap, qualStr)
      await refresh()
    } catch (e) {
      actionError = e instanceof Error ? e.message : String(e)
    } finally {
      grantBusy = ''
    }
  }

  async function revoke(card: Card, cap: string) {
    grantBusy = `${card.id}:${cap}`
    actionError = ''
    try {
      await RevokeCapability(card.id, cap)
      await refresh()
    } catch (e) {
      actionError = e instanceof Error ? e.message : String(e)
    } finally {
      grantBusy = ''
    }
  }

  async function reloadAll() {
    await loadPlugins(activeNotebook, activeSection, activePage)
    await refresh()
  }

  async function chooseArchive() {
    preview = null
    previewError = ''
    pendingPath = ''
    try {
      const selected = await PickPluginArchive()
      if (!selected) return
      pendingPath = selected
      const result = await ValidatePluginArchive(selected)
      // ValidatePluginArchive returns { manifest, warnings }.
      preview = {
        manifest: result.manifest,
        warnings: result.warnings ?? []
      }
    } catch (e) {
      previewError = e instanceof Error ? e.message : String(e)
    }
  }

  async function confirmInstall() {
    if (!pendingPath) return
    installing = true
    try {
      await InstallPlugin(pendingPath)
      pendingPath = ''
      preview = null
      await reloadAll()
    } catch (e) {
      previewError = e instanceof Error ? e.message : String(e)
    } finally {
      installing = false
    }
  }

  async function toggle(card: Card) {
    actionError = ''
    try {
      if (card.source === 'first-party') {
        // First-party plugins are disabled via the config disabled list
        // (there's no on-disk folder for a .disabled sentinel).
        const cfg = settings.config!
        // Defensive: the Go backend always populates cfg.plugins, but the
        // wails-generated SystemConfig class returns undefined for missing
        // keys, and a hand-edited config.yaml could omit the section.
        if (!cfg.plugins) {
          cfg.plugins = { active: [], disabled: [], plugin_settings: {} }
        }
        const disabled = new Set(cfg.plugins.disabled ?? [])
        if (card.disabled) {
          disabled.delete(card.id)
        } else {
          disabled.add(card.id)
          // Tearing down before reload drops the plugin's event-bus
          // subscriptions + lifecycle hooks (#106) so they don't linger.
          teardownPlugin(card.id)
        }
        cfg.plugins.disabled = [...disabled]
        await saveConfig(cfg)
        await reloadAll()
      } else {
        // Disk plugins use the .disabled sentinel file.
        if (card.disabled) {
          await EnablePlugin(card.id)
        } else {
          await DisablePlugin(card.id)
          teardownPlugin(card.id)
        }
        await reloadAll()
      }
    } catch (e) {
      actionError = e instanceof Error ? e.message : String(e)
    }
  }

  async function uninstall(card: Card) {
    actionError = ''
    if (
      !window.confirm(
        `Uninstall plugin "${card.name}"? This removes it from .system/plugins/.`
      )
    ) {
      return
    }
    try {
      // Tear down the plugin's host surface (lifecycle hooks + event-bus
      // subscriptions) BEFORE removing the folder + reloading (#106).
      teardownPlugin(card.id)
      await UninstallPlugin(card.id)
      if (expanded === card.id) expanded = null
      await reloadAll()
    } catch (e) {
      actionError = e instanceof Error ? e.message : String(e)
    }
  }

  function pluginSettings(id: string): Record<string, any> | undefined {
    return settings.config?.plugins.plugin_settings?.[id]
  }

  // #214: Hoist the surface lookup so the per-card check is O(1), not O(N²).
  // A single $derived set of pluginIDs that have a registered settings-panel
  // surface, recomputed only when the surfaces list changes.
  let settingsPanelPluginIDs = $derived(
    new Set(getSurfaces('settings-panel').map((s) => s.pluginID))
  )

  // #214: hasBespokeSettings reports whether a plugin renders its settings via a
  // dedicated tab (first-party settingsPageComponent or a registered
  // 'settings-panel' surface) rather than the generic schema form. When true,
  // the card shows a redirect note instead of the generic form (either/or).
  function hasBespokeSettings(id: string): boolean {
    if (loadedPlugins.plugins.get(id)?.settingsPageComponent) return true
    return settingsPanelPluginIDs.has(id)
  }

  function openPluginView(id: string) {
    // First-party view ids map to activeView (silt-agenda → agenda, etc.).
    const viewId = id.replace(/^silt-/, '')
    window.dispatchEvent(new CustomEvent('switch-view', { detail: viewId }))
  }

  onMount(() => {
    refresh()
  })
</script>

<div class="p-6 max-w-3xl">
  <!-- Install flow -->
  <section class="mb-6">
    <button
      onclick={chooseArchive}
      class="bg-accent-primary-glow border border-accent-primary-start/30 text-accent-primary-start font-label-sm-bold px-3 py-2 rounded flex items-center gap-2 hover:brightness-110 hover:border-accent-primary-start transition-all cursor-pointer"
    >
      <span class="material-symbols-outlined text-[18px]">file_download</span>
      Install from .silt-plugin…
    </button>
    <button
      onclick={checkForUpdates}
      class="ml-2 text-text-muted hover:text-accent-primary-start text-[11px] font-label-sm-bold bg-transparent border border-border-muted rounded px-2 py-1 cursor-pointer transition-colors"
    >
      Check for updates
    </button>

    {#if previewError}
      <p class="text-error text-[12px] font-body-md mt-3">
        Validation failed: {previewError}
      </p>
    {/if}

    {#if preview}
      <div class="mt-3 p-3 rounded-lg bg-surface border border-border-muted">
        <div class="flex items-center gap-2 mb-1">
          <span class="font-label-sm-bold text-text-primary"
            >{preview.manifest.name}</span
          >
          <span class="text-[10px] text-text-muted"
            >v{preview.manifest.version || '0.0.0'}</span
          >
          <span class="text-[10px] text-text-muted"
            >· {preview.manifest.id}</span
          >
        </div>
        {#if preview.manifest.description}
          <p class="text-text-muted text-[12px] font-body-md mb-2">
            {preview.manifest.description}
          </p>
        {/if}
        {#if preview.warnings && preview.warnings.length > 0}
          <ul class="mb-2 space-y-0.5">
            {#each preview.warnings as w}
              <li
                class="text-yellow-300/80 text-[11px] font-body-md flex items-start gap-1"
              >
                <span class="material-symbols-outlined text-[13px] mt-0.5"
                  >warning</span
                >
                {w}
              </li>
            {/each}
          </ul>
        {/if}
        {#if preview.manifest.capabilities && Object.keys(preview.manifest.capabilities).length > 0}
          <div class="mb-2">
            <div
              class="text-text-muted text-[10px] font-label-sm-bold uppercase tracking-widest mb-1"
            >
              Requests capabilities
            </div>
            <ul class="space-y-0.5">
              {#each Object.keys(preview.manifest.capabilities) as cap}
                <li
                  class="text-[11px] text-text-primary font-body-md flex items-center gap-1.5"
                >
                  <span
                    class="material-symbols-outlined text-[13px] text-accent-primary-start/70"
                    >key</span
                  >
                  {capabilityLabels[cap] ?? cap}{qualifierLabel(
                    preview.manifest.capabilities[cap]
                  )}
                </li>
              {/each}
            </ul>
            <p class="text-text-muted text-[10px] mt-1 italic">
              You can grant or revoke each capability after install.
            </p>
          </div>
        {/if}
        <button
          onclick={confirmInstall}
          disabled={installing}
          class="bg-accent-primary-start/20 border border-accent-primary-start/40 text-accent-primary-start font-label-sm-bold px-3 py-1.5 rounded hover:brightness-110 transition-all cursor-pointer disabled:opacity-50"
        >
          {installing ? 'Installing…' : 'Install'}
        </button>
      </div>
    {/if}
  </section>

  {#if actionError}
    <div
      class="flex items-start gap-2 p-3 mb-4 rounded-lg bg-error/10 border border-error/30 text-error text-[12px] font-body-md"
    >
      <span class="material-symbols-outlined text-[18px]">error</span>
      <span class="flex-1">{actionError}</span>
    </div>
  {/if}

  <!-- Plugin list -->
  <h3
    class="font-label-sm-bold text-text-muted uppercase tracking-widest text-[10px] mb-2"
  >
    Plugins
  </h3>

  {#if loading}
    <div class="text-text-muted py-4 animate-pulse font-body-md">Loading…</div>
  {:else if cards.length === 0}
    <div class="text-text-muted py-4 font-body-md text-[13px]">
      No plugins installed. First-party plugins (Agenda, Calendar, Kanban,
      Attachments) are bundled.
    </div>
  {:else}
    <div class="space-y-2">
      {#each cards as card (card.id)}
        <div
          class="rounded-lg border border-border-muted bg-surface/50 overflow-hidden"
        >
          <!-- Card row -->
          <div class="flex items-center gap-3 px-4 py-3">
            <span
              class="material-symbols-outlined text-accent-primary-start/80 text-[24px]"
            >
              {card.icon || 'extension'}
            </span>
            <div class="flex-1 min-w-0">
              <div class="flex items-center gap-2 flex-wrap">
                <span class="font-body-md text-text-primary truncate"
                  >{card.name}</span
                >
                <span class="text-[10px] text-text-muted">v{card.version}</span>
                {#if card.updateAvailable}
                  <span
                    class="text-[9px] text-accent-primary-start bg-accent-primary-glow border border-accent-primary-start/30 rounded px-1.5 py-0.5 uppercase tracking-wider"
                  >
                    Update available
                  </span>
                {/if}
                {#if card.author}
                  <span class="text-[10px] text-text-muted truncate"
                    >· {card.author}</span
                  >
                {/if}
                <span
                  class={'text-[9px] rounded px-1.5 py-0.5 uppercase tracking-wider border ' +
                    (card.source === 'first-party'
                      ? 'text-accent-primary-start border-accent-primary-start/40'
                      : 'text-text-muted border-border-muted')}
                >
                  {card.source === 'first-party' ? 'Bundled' : 'Installed'}
                </span>
                {#if card.disabled}
                  <span
                    class="text-[9px] text-text-muted bg-panel border border-border-muted rounded px-1.5 py-0.5 uppercase tracking-wider"
                    >disabled</span
                  >
                {/if}
                {#if card.loadError}
                  <span
                    class="text-[9px] text-error bg-error/10 border border-error/30 rounded px-1.5 py-0.5 uppercase tracking-wider"
                    >error</span
                  >
                {/if}
              </div>
              <div class="text-[10px] text-text-muted truncate font-label-sm">
                {card.id}
              </div>
            </div>

            <!-- Expand details -->
            <button
              onclick={() => (expanded = expanded === card.id ? null : card.id)}
              aria-label={expanded === card.id ? 'Collapse' : 'Details'}
              title="Details"
              class="text-text-muted hover:text-text-primary border-none bg-transparent cursor-pointer p-1.5 rounded transition-colors"
            >
              <span class="material-symbols-outlined text-[18px]">
                {expanded === card.id ? 'expand_less' : 'expand_more'}
              </span>
            </button>

            <button
              onclick={() => toggle(card)}
              title={card.disabled ? 'Enable' : 'Disable'}
              aria-label={card.disabled ? 'Enable' : 'Disable'}
              class="text-text-muted hover:text-accent-primary-start border-none bg-transparent cursor-pointer p-1.5 rounded transition-colors"
            >
              <span class="material-symbols-outlined text-[20px]">
                {card.disabled ? 'toggle_off' : 'toggle_on'}
              </span>
            </button>
            {#if card.source === 'disk'}
              <button
                onclick={() => uninstall(card)}
                title="Uninstall"
                aria-label="Uninstall"
                class="text-text-muted hover:text-error border-none bg-transparent cursor-pointer p-1.5 rounded transition-colors"
              >
                <span class="material-symbols-outlined text-[18px]">delete</span
                >
              </button>
            {/if}
          </div>

          <!-- Inline load error -->
          {#if card.loadError}
            <div
              class="px-4 pb-2 -mt-1 text-error text-[11px] font-body-md flex items-center gap-1.5"
            >
              <span class="material-symbols-outlined text-[14px]">error</span>
              {card.loadError}
            </div>
          {/if}

          <!-- Detail panel -->
          {#if expanded === card.id}
            <div
              transition:fade={{ duration: 120 }}
              class="px-4 py-3 border-t border-border-muted bg-panel/40 space-y-2"
            >
              {#if card.description}
                <p class="text-text-muted text-[12px] font-body-md">
                  {card.description}
                </p>
              {/if}
              <dl
                class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-[11px] font-label-sm"
              >
                <dt class="text-text-muted">ID</dt>
                <dd class="text-text-primary">{card.id}</dd>
                <dt class="text-text-muted">Version</dt>
                <dd class="text-text-primary">{card.version}</dd>
                {#if card.author}
                  <dt class="text-text-muted">Author</dt>
                  <dd class="text-text-primary">{card.author}</dd>
                {/if}
                <dt class="text-text-muted">Source</dt>
                <dd class="text-text-primary capitalize">
                  {card.source === 'first-party'
                    ? 'First-party (bundled)'
                    : 'Third-party (.silt-plugin)'}
                </dd>
                <dt class="text-text-muted">Status</dt>
                <dd class="text-text-primary">
                  {#if card.loadError}
                    Error
                  {:else if card.disabled}
                    Disabled
                  {:else}
                    Active
                  {/if}
                </dd>
              </dl>

              {#if hasBespokeSettings(card.id)}
                <!-- #214: this plugin renders settings via a dedicated tab;
                     offer a one-click switch instead of dead text. -->
                <div>
                  <div
                    class="text-text-muted text-[10px] font-label-sm-bold uppercase tracking-widest mt-2 mb-1"
                  >
                    Plugin settings
                  </div>
                  {#if onSwitchTab}
                    <button
                      type="button"
                      class="text-[12px] text-accent-primary-start hover:underline bg-transparent border-none cursor-pointer p-0 font-body-md"
                      onclick={() => onSwitchTab(`plugin:${card.id}`)}
                    >
                      Open the {card.name} settings tab
                    </button>
                  {:else}
                    <p class="text-[12px] text-text-muted font-body-md">
                      This plugin has a dedicated settings page — open the
                      <strong>{card.name}</strong> tab on the left.
                    </p>
                  {/if}
                </div>
              {:else if card.settingsSchema && card.settingsSchema.length > 0}
                <div>
                  <div
                    class="text-text-muted text-[10px] font-label-sm-bold uppercase tracking-widest mt-2 mb-1"
                  >
                    Plugin settings
                  </div>
                  <SettingsForm
                    pluginID={card.id}
                    schema={card.settingsSchema}
                    values={pluginSettings(card.id) ?? {}}
                  />
                </div>
              {:else if pluginSettings(card.id)}
                <div>
                  <div
                    class="text-text-muted text-[10px] font-label-sm-bold uppercase tracking-widest mt-2 mb-1"
                  >
                    Plugin settings
                  </div>
                  <pre
                    class="text-[10px] text-text-primary bg-void/60 border border-border-muted rounded p-2 overflow-x-auto">{JSON.stringify(
                      pluginSettings(card.id),
                      null,
                      2
                    )}</pre>
                </div>
              {/if}

              {#if card.requestedCapabilities && Object.keys(card.requestedCapabilities).length > 0}
                <div>
                  <div
                    class="text-text-muted text-[10px] font-label-sm-bold uppercase tracking-widest mt-2 mb-1"
                    id="caps-{card.id}"
                  >
                    Capabilities
                  </div>
                  <ul
                    class="text-[11px] font-body-md space-y-1"
                    aria-labelledby="caps-{card.id}"
                  >
                    {#each Object.keys(card.requestedCapabilities) as cap}
                      <li class="flex items-center gap-2">
                        <span
                          class="material-symbols-outlined text-[14px] text-text-muted"
                        >
                          {isGranted(card, cap) ? 'lock_open' : 'lock'}
                        </span>
                        <span class="flex-1 text-text-primary">
                          {capabilityLabels[cap] ?? cap}{qualifierLabel(
                            card.requestedCapabilities[cap]
                          )}
                        </span>
                        {#if card.source === 'first-party'}
                          <span class="text-[10px] text-text-muted italic"
                            >trusted</span
                          >
                        {:else if isGranted(card, cap)}
                          <button
                            onclick={() => revoke(card, cap)}
                            disabled={grantBusy === `${card.id}:${cap}`}
                            class="text-text-muted hover:text-error text-[10px] font-label-sm-bold bg-transparent border border-border-muted rounded px-2 py-0.5 cursor-pointer disabled:opacity-50"
                            aria-label="Revoke {capabilityLabels[cap] ?? cap}"
                          >
                            Revoke
                          </button>
                        {:else}
                          <button
                            onclick={() => grant(card, cap)}
                            disabled={grantBusy === `${card.id}:${cap}`}
                            class="text-accent-primary-start hover:brightness-110 text-[10px] font-label-sm-bold bg-transparent border border-accent-primary-start/40 rounded px-2 py-0.5 cursor-pointer disabled:opacity-50"
                            aria-label="Grant {capabilityLabels[cap] ?? cap}"
                          >
                            Grant
                          </button>
                        {/if}
                      </li>
                    {/each}
                  </ul>
                </div>
              {/if}

              {#if card.source === 'first-party'}
                <button
                  onclick={() => openPluginView(card.id)}
                  class="mt-1 text-accent-primary-start text-[11px] font-label-sm-bold hover:brightness-110 bg-transparent border-none cursor-pointer flex items-center gap-1"
                >
                  Open {card.name} view
                  <span class="material-symbols-outlined text-[14px]"
                    >arrow_forward</span
                  >
                </button>
              {/if}

              {#if card.grantedCapabilities?.network}
                <div>
                  <div
                    class="text-text-muted text-[10px] font-label-sm-bold uppercase tracking-widest mt-2 mb-1"
                  >
                    Network activity
                  </div>
                  <NetworkAuditViewer pluginID={card.id} />
                </div>
              {/if}
            </div>
          {/if}
        </div>
      {/each}
    </div>
  {/if}
</div>
