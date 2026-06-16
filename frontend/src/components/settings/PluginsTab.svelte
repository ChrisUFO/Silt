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
    PickPluginArchive
  } from '../../../wailsjs/go/main/App.js'
  import { loadPlugins } from '../../plugins/loader'
  import { firstPartyPlugins } from '../../plugins/registry'
  import { loadedPlugins } from '../../plugins/store.svelte'
  import { settings, saveConfig } from '../../settings/store.svelte'

  interface Props {
    activeNotebook: string
    activeSection: string
    activePage: string
  }
  let { activeNotebook, activeSection, activePage }: Props = $props()

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
  }

  let cards = $state<Card[]>([])
  let loading = $state(true)
  let expanded = $state<string | null>(null)

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
          loadError: errs.find((e) => e.id === m.id)?.message
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
          loadError: errs.find((e) => e.id === p.id)?.message
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

    {#if previewError}
      <p class="text-error text-[12px] font-body-md mt-3">
        Validation failed: {previewError}
      </p>
    {/if}

    {#if preview}
      <div class="mt-3 p-3 rounded-lg bg-bg-surface border border-border-muted">
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
      No plugins installed. First-party plugins (Agenda, Calendar) are bundled.
    </div>
  {:else}
    <div class="space-y-2">
      {#each cards as card (card.id)}
        <div
          class="rounded-lg border border-border-muted bg-bg-surface/50 overflow-hidden"
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
                    class="text-[9px] text-text-muted bg-bg-panel border border-border-muted rounded px-1.5 py-0.5 uppercase tracking-wider"
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
              class="px-4 py-3 border-t border-border-muted bg-bg-panel/40 space-y-2"
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

              {#if pluginSettings(card.id)}
                <div>
                  <div
                    class="text-text-muted text-[10px] font-label-sm-bold uppercase tracking-widest mt-2 mb-1"
                  >
                    Plugin settings
                  </div>
                  <pre
                    class="text-[10px] text-text-primary bg-bg-void/60 border border-border-muted rounded p-2 overflow-x-auto">{JSON.stringify(
                      pluginSettings(card.id),
                      null,
                      2
                    )}</pre>
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
            </div>
          {/if}
        </div>
      {/each}
    </div>
  {/if}
</div>
