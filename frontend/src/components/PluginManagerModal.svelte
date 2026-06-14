<script lang="ts">
  import { onMount } from 'svelte'
  import {
    ListPlugins,
    ValidatePluginArchive,
    InstallPlugin,
    UninstallPlugin,
    EnablePlugin,
    DisablePlugin,
    PickPluginArchive
  } from '../../wailsjs/go/main/App.js'
  import { loadPlugins } from '../plugins/loader'

  interface Props {
    onClose: () => void
    activeNotebook: string
    activeSection: string
    activePage: string
  }

  let { onClose, activeNotebook, activeSection, activePage }: Props = $props()

  interface InstalledPlugin {
    id: string
    name: string
    version: string
    has_manifest: boolean
    has_index: boolean
    disabled: boolean
  }

  let installed = $state<InstalledPlugin[]>([])
  let loading = $state(true)
  let installing = $state(false)
  let preview = $state<any>(null)
  let previewError = $state('')
  let pendingPath = $state('')

  async function refresh() {
    loading = true
    try {
      const list = (await ListPlugins()) ?? []
      installed = list.map((p: any) => ({
        id: p.id,
        name: p.name || p.id,
        version: p.version || '—',
        has_manifest: p.has_manifest,
        has_index: p.has_index,
        disabled: !!p.disabled
      }))
    } catch (e) {
      console.error('ListPlugins failed:', e)
      installed = []
    } finally {
      loading = false
    }
  }

  async function chooseArchive() {
    preview = null
    previewError = ''
    pendingPath = ''
    try {
      const selected = await PickPluginArchive()
      if (!selected) return
      pendingPath = selected
      const m = await ValidatePluginArchive(selected)
      preview = m
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
      await refresh()
      await loadPlugins(activeNotebook, activeSection, activePage)
    } catch (e) {
      previewError = e instanceof Error ? e.message : String(e)
    } finally {
      installing = false
    }
  }

  async function uninstall(id: string) {
    if (
      !confirm(
        `Uninstall plugin "${id}"? This removes it from .system/plugins/.`
      )
    )
      return
    try {
      await UninstallPlugin(id)
      await refresh()
      await loadPlugins(activeNotebook, activeSection, activePage)
    } catch (e) {
      alert(
        'Failed to uninstall: ' + (e instanceof Error ? e.message : String(e))
      )
    }
  }

  async function toggle(id: string, disabled: boolean) {
    try {
      if (disabled) {
        await EnablePlugin(id)
      } else {
        await DisablePlugin(id)
      }
      await refresh()
      await loadPlugins(activeNotebook, activeSection, activePage)
    } catch (e) {
      alert('Failed: ' + (e instanceof Error ? e.message : String(e)))
    }
  }

  onMount(() => {
    refresh()
  })
</script>

<!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
<div
  onclick={onClose}
  class="fixed inset-0 bg-[#000]/60 backdrop-blur-sm z-[180] flex items-start justify-center pt-24"
>
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
  <div
    onclick={(e) => e.stopPropagation()}
    class="w-full max-w-2xl glass-palette border border-border-zinc rounded-xl shadow-2xl overflow-hidden flex flex-col max-h-[70vh]"
    style="backdrop-filter: blur(16px) saturate(140%); background: rgba(22, 22, 25, 0.94);"
  >
    <div class="px-5 py-4 border-b border-border-muted flex items-center gap-2">
      <span class="material-symbols-outlined text-accent-teal-start"
        >extension</span
      >
      <h2 class="font-headline-md text-headline-md text-text-primary">
        Plugin Manager
      </h2>
    </div>

    <div class="flex-1 overflow-y-auto custom-scrollbar">
      <!-- Install panel -->
      <div class="px-5 py-4 border-b border-border-muted">
        <button
          onclick={chooseArchive}
          class="bg-accent-teal-glow border border-accent-teal-start/30 text-accent-teal-start font-label-sm-bold px-3 py-2 rounded flex items-center gap-2 hover:brightness-110 hover:border-accent-teal-start transition-all cursor-pointer"
        >
          <span class="material-symbols-outlined text-[18px]"
            >file_download</span
          >
          Install from .silt-plugin…
        </button>

        {#if previewError}
          <p class="text-error text-[12px] font-body-md mt-3">
            Validation failed: {previewError}
          </p>
        {/if}

        {#if preview}
          <div
            class="mt-3 p-3 rounded-lg bg-bg-surface border border-border-muted"
          >
            <div class="flex items-center gap-2 mb-1">
              <span class="font-label-sm-bold text-text-primary"
                >{preview.name}</span
              >
              <span class="text-[10px] text-text-muted"
                >v{preview.version || '0.0.0'}</span
              >
              <span class="text-[10px] text-text-muted">· {preview.id}</span>
            </div>
            {#if preview.description}
              <p class="text-text-muted text-[12px] font-body-md mb-2">
                {preview.description}
              </p>
            {/if}
            <button
              onclick={confirmInstall}
              disabled={installing}
              class="bg-accent-teal-start/20 border border-accent-teal-start/40 text-accent-teal-start font-label-sm-bold px-3 py-1.5 rounded hover:brightness-110 transition-all cursor-pointer disabled:opacity-50"
            >
              {installing ? 'Installing…' : 'Install'}
            </button>
          </div>
        {/if}
      </div>

      <!-- Installed list -->
      <div class="px-5 py-4">
        <h3
          class="font-label-sm-bold text-text-muted uppercase tracking-widest text-[10px] mb-2"
        >
          Installed
        </h3>
        {#if loading}
          <div class="text-text-muted py-4 animate-pulse">Loading…</div>
        {:else if installed.length === 0}
          <div class="text-text-muted py-4 font-body-md text-[13px]">
            No third-party plugins installed. First-party plugins (Agenda,
            Calendar) are bundled.
          </div>
        {:else}
          {#each installed as p (p.id)}
            <div
              class="flex items-center gap-3 py-2.5 border-b border-border-muted/50 last:border-0"
            >
              <span
                class="material-symbols-outlined text-text-muted text-[20px]"
                >extension</span
              >
              <div class="flex-1 min-w-0">
                <div class="flex items-center gap-2">
                  <span class="font-body-md text-text-primary truncate"
                    >{p.name}</span
                  >
                  <span class="text-[10px] text-text-muted">v{p.version}</span>
                  {#if p.disabled}
                    <span
                      class="text-[9px] text-text-muted bg-bg-panel border border-border-muted rounded px-1.5 py-0.5 uppercase tracking-wider"
                      >disabled</span
                    >
                  {/if}
                </div>
                <div class="text-[10px] text-text-muted truncate font-label-sm">
                  {p.id}
                </div>
              </div>
              <button
                onclick={() => toggle(p.id, p.disabled)}
                title={p.disabled ? 'Enable' : 'Disable'}
                class="text-text-muted hover:text-accent-teal-start border-none bg-transparent cursor-pointer p-1.5 rounded transition-colors"
              >
                <span class="material-symbols-outlined text-[18px]"
                  >{p.disabled ? 'toggle_off' : 'toggle_on'}</span
                >
              </button>
              <button
                onclick={() => uninstall(p.id)}
                title="Uninstall"
                class="text-text-muted hover:text-error border-none bg-transparent cursor-pointer p-1.5 rounded transition-colors"
              >
                <span class="material-symbols-outlined text-[18px]">delete</span
                >
              </button>
            </div>
          {/each}
        {/if}
      </div>
    </div>

    <div
      class="flex items-center justify-end px-5 py-3 border-t border-border-muted"
    >
      <button
        onclick={onClose}
        class="px-4 py-2 rounded-lg text-text-muted hover:text-text-primary font-label-sm-bold transition-colors border-none bg-transparent cursor-pointer"
      >
        Close
      </button>
    </div>
  </div>
</div>
