<script lang="ts">
  import { untrack } from 'svelte'
  import {
    settings,
    saveConfig,
    reloadFromBackend
  } from '../../settings/store.svelte'
  import type { SystemConfig } from '../../settings/store.svelte'
  import { parseHotkey } from '../../settings/hotkeys'

  let draft = $state<SystemConfig | null>(null)
  let lastSaved = $state<SystemConfig | null>(null)

  function deepClone<T>(value: T): T {
    return JSON.parse(JSON.stringify(value))
  }

  $effect(() => {
    const cfg = settings.config
    if (!cfg) return
    const hasDraft = untrack(() => draft)
    const dirty = untrack(() => settings.dirty)
    if (hasDraft && dirty) return
    draft = deepClone(cfg)
    lastSaved = deepClone(cfg)
  })

  function touch() {
    settings.dirty = true
  }

  function changed(): boolean {
    if (!draft || !lastSaved) return false
    return JSON.stringify(draft.hotkeys) !== JSON.stringify(lastSaved.hotkeys)
  }

  let isValid = $derived(
    draft !== null &&
      Object.values(draft.hotkeys).every(
        (h) => h.trim() === '' || parseHotkey(h) !== null
      )
  )

  let hotkeyEntries = $derived(
    draft
      ? Object.entries(draft.hotkeys).sort((a, b) => a[0].localeCompare(b[0]))
      : []
  )

  function prettyLabel(key: string): string {
    return key.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase())
  }

  async function handleSave() {
    if (!draft) return
    settings.dirty = false
    const ok = await saveConfig(draft)
    if (ok) {
      lastSaved = deepClone(draft)
    } else {
      settings.dirty = true
    }
  }

  function handleRevert() {
    if (!lastSaved) return
    draft = deepClone(lastSaved)
    settings.dirty = false
  }
</script>

{#if !draft}
  <div class="p-8 text-text-muted font-body-md">No configuration loaded.</div>
{:else}
  <div class="flex-1 flex flex-col min-h-0 overflow-hidden h-full">
    <!-- Scrollable content -->
    <div class="flex-1 overflow-y-auto p-6 space-y-6 custom-scrollbar">
      <!-- External update notice -->
      {#if settings.pendingExternal}
        <div
          class="flex items-start gap-2 p-3 rounded-lg bg-accent-primary-start/10 border border-accent-primary-start/30 text-accent-primary-start text-[12px] font-body-md"
        >
          <span class="material-symbols-outlined text-[18px]">sync</span>
          <span class="flex-1">
            Settings were updated externally. Your unsaved edits are preserved.
          </span>
          <button
            onclick={async () => {
              settings.dirty = false
              await reloadFromBackend()
            }}
            class="font-label-sm-bold underline hover:brightness-110 bg-transparent border-none cursor-pointer text-accent-primary-start"
          >
            Reload
          </button>
        </div>
      {/if}

      <!-- Hotkeys Group Card -->
      <div
        class="bg-surface/20 border border-border-muted rounded-xl p-5 space-y-4"
      >
        <div class="flex items-center justify-between">
          <h4
            class="font-label-sm-bold text-text-primary uppercase tracking-wider text-[10px]"
          >
            Keyboard Shortcuts
          </h4>
          <span class="text-text-muted text-[10px]">
            Leave empty to disable. Modifiers+key (e.g. Ctrl+Shift+9,
            Ctrl+Alt+2).
          </span>
        </div>
        <div class="grid grid-cols-2 gap-x-6 gap-y-3">
          {#each hotkeyEntries as [key, value] (key)}
            <label class="flex flex-col gap-1">
              <span
                class="text-text-muted text-[10px] font-semibold uppercase tracking-wider truncate"
                title={prettyLabel(key)}
              >
                {prettyLabel(key)}
              </span>
              <input
                {value}
                oninput={(e) => {
                  draft!.hotkeys[key] = e.currentTarget.value
                  touch()
                }}
                type="text"
                class="bg-surface border border-border-zinc rounded-lg px-3 py-1.5 text-text-primary text-[12px] font-mono outline-none focus:border-accent-primary-start transition-colors w-full"
              />
            </label>
          {/each}
        </div>
      </div>

      <!-- Error banner -->
      {#if settings.error}
        <div
          class="flex items-start gap-2 p-3 rounded-lg bg-error/10 border border-error/30 text-error text-[12px] font-body-md"
        >
          <span class="material-symbols-outlined text-[18px]">error</span>
          <span class="flex-1">{settings.error}</span>
        </div>
      {/if}
    </div>

    <!-- Fixed Footer Actions -->
    <div
      class="flex items-center justify-end gap-2 px-6 py-4 border-t border-border-muted bg-surface/10 flex-shrink-0"
    >
      <button
        onclick={handleRevert}
        disabled={!changed()}
        class="px-4 py-2 rounded-lg text-text-muted hover:text-text-primary font-label-sm-bold transition-colors border-none bg-transparent cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
      >
        Revert
      </button>
      <button
        onclick={handleSave}
        disabled={!changed() || !isValid || settings.saving}
        class="px-4 py-2 rounded-lg bg-accent-primary-start/20 border border-accent-primary-start/40 text-accent-primary-start font-label-sm-bold hover:brightness-110 transition-all cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
      >
        {settings.saving ? 'Saving…' : 'Save changes'}
      </button>
    </div>
  </div>
{/if}
