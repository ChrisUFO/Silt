<script lang="ts">
  import { untrack } from 'svelte'
  import {
    settings,
    saveConfig,
    reloadFromBackend
  } from '../../settings/store.svelte'
  import type { SystemConfig } from '../../settings/store.svelte'
  import { parseHotkey } from '../../settings/hotkeys'

  // Local editable draft. Initialized from the store config; the user edits
  // here and commits with Save (so an external hot-reload doesn't fight a
  // half-edited form).
  let draft = $state<SystemConfig | null>(null)
  let lastSaved = $state<SystemConfig | null>(null)

  // Svelte 5 $state proxies cannot be passed to structuredClone() — they
  // carry non-cloneable internal machinery and throw DataCloneError, which
  // (because this runs inside an $effect) aborts the reaction flush and
  // leaves the whole settings modal non-interactive. The config is plain
  // serializable data (strings/numbers/booleans/arrays/objects), so a JSON
  // round-trip is a safe, proxy-unwrapping deep copy.
  function deepClone<T>(value: T): T {
    return JSON.parse(JSON.stringify(value))
  }

  // Sync the draft whenever the store config changes and the user has no
  // unsaved local edits (avoid clobbering in-progress edits).
  //
  // The guard reads `draft` and `settings.dirty`, but this effect ALSO writes
  // `draft`/`lastSaved`. If those reads were tracked, the write would re-trigger
  // the effect → infinite loop → the modal freezes for ~10-20s (and may never
  // recover). `untrack` lets us read the guard state WITHOUT subscribing to it,
  // so the effect depends ONLY on `settings.config` (the real external trigger)
  // and the write doesn't loop back into itself.
  $effect(() => {
    const cfg = settings.config
    if (!cfg) return
    const hasDraft = untrack(() => draft)
    const dirty = untrack(() => settings.dirty)
    if (hasDraft && dirty) return // keep local edits
    draft = deepClone(cfg)
    lastSaved = deepClone(cfg)
  })

  // Any mutation to the draft marks the store dirty.
  function touch() {
    settings.dirty = true
  }

  function changed(): boolean {
    if (!draft || !lastSaved) return false
    return JSON.stringify(draft) !== JSON.stringify(lastSaved)
  }

  // Mirrors the backend SaveSystemConfig validation so the Save button is
  // disabled before submission rather than relying on a backend rejection.
  // Hotkeys: empty is allowed (= disabled); a non-empty binding must parse.
  let isValid = $derived(
    draft !== null &&
      draft.editor.font_size_px > 0 &&
      draft.editor.tab_indent_spaces > 0 &&
      draft.editor.line_height > 0 &&
      draft.editor.auto_save_delay_ms >= 0 &&
      Object.values(draft.hotkeys).every(
        (h) => h.trim() === '' || parseHotkey(h) !== null
      )
  )

  let hotkeyEntries = $derived(
    draft
      ? Object.entries(draft.hotkeys).sort((a, b) => a[0].localeCompare(b[0]))
      : []
  )

  async function handleSave() {
    if (!draft) return
    settings.dirty = false // optimistic; saveConfig re-asserts on failure
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

  function prettyLabel(key: string): string {
    return key.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase())
  }
</script>

{#if !draft}
  <div class="p-8 text-text-muted font-body-md">No configuration loaded.</div>
{:else}
  <div class="p-6 space-y-8 max-w-2xl">
    <!-- External update notice (unsaved edits preserved, not clobbered) -->
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
    <!-- Vault path -->
    <section>
      <h3
        class="font-label-sm-bold text-text-muted uppercase tracking-widest text-[10px] mb-3"
      >
        Workspace
      </h3>
      <div
        class="flex items-center gap-2 bg-bg-surface border border-border-muted rounded-lg px-3 py-2.5"
      >
        <span class="material-symbols-outlined text-text-muted text-[18px]"
          >folder</span
        >
        <span
          class="text-text-primary text-[13px] font-body-md truncate flex-1"
        >
          {draft.notebooks.path || '—'}
        </span>
      </div>
      <p class="text-text-muted text-[11px] font-label-sm mt-1.5">
        Vault path is set during onboarding. Switch workspaces from the sidebar.
      </p>
    </section>

    <!-- Editor defaults -->
    <section>
      <h3
        class="font-label-sm-bold text-text-muted uppercase tracking-widest text-[10px] mb-3"
      >
        Editor
      </h3>
      <div class="grid grid-cols-2 gap-4">
        <label class="flex flex-col gap-1.5">
          <span class="text-text-muted text-[11px] font-label-sm-bold"
            >Font family</span
          >
          <input
            bind:value={draft.editor.font_family}
            oninput={touch}
            type="text"
            class="bg-bg-surface border border-border-zinc rounded-lg px-3 py-2 text-text-primary text-[13px] font-body-md outline-none focus:border-accent-primary-start transition-colors"
          />
        </label>
        <label class="flex flex-col gap-1.5">
          <span class="text-text-muted text-[11px] font-label-sm-bold"
            >Monospace font</span
          >
          <input
            bind:value={draft.editor.mono_font_family}
            oninput={touch}
            type="text"
            class="bg-bg-surface border border-border-zinc rounded-lg px-3 py-2 text-text-primary text-[13px] font-body-md outline-none focus:border-accent-primary-start transition-colors"
          />
        </label>
        <label class="flex flex-col gap-1.5">
          <span class="text-text-muted text-[11px] font-label-sm-bold"
            >Font size (px)</span
          >
          <input
            bind:value={draft.editor.font_size_px}
            oninput={touch}
            type="number"
            min="8"
            max="48"
            class="bg-bg-surface border border-border-zinc rounded-lg px-3 py-2 text-text-primary text-[13px] font-body-md outline-none focus:border-accent-primary-start transition-colors"
          />
        </label>
        <label class="flex flex-col gap-1.5">
          <span class="text-text-muted text-[11px] font-label-sm-bold"
            >Line height</span
          >
          <input
            bind:value={draft.editor.line_height}
            oninput={touch}
            type="number"
            step="0.1"
            min="1"
            max="3"
            class="bg-bg-surface border border-border-zinc rounded-lg px-3 py-2 text-text-primary text-[13px] font-body-md outline-none focus:border-accent-primary-start transition-colors"
          />
        </label>
        <label class="flex flex-col gap-1.5">
          <span class="text-text-muted text-[11px] font-label-sm-bold"
            >Tab width (spaces)</span
          >
          <input
            bind:value={draft.editor.tab_indent_spaces}
            oninput={touch}
            type="number"
            min="1"
            max="8"
            class="bg-bg-surface border border-border-zinc rounded-lg px-3 py-2 text-text-primary text-[13px] font-body-md outline-none focus:border-accent-primary-start transition-colors"
          />
        </label>
        <label class="flex flex-col gap-1.5">
          <span class="text-text-muted text-[11px] font-label-sm-bold"
            >Auto-save delay (ms)</span
          >
          <input
            bind:value={draft.editor.auto_save_delay_ms}
            oninput={touch}
            type="number"
            min="0"
            step="100"
            class="bg-bg-surface border border-border-zinc rounded-lg px-3 py-2 text-text-primary text-[13px] font-body-md outline-none focus:border-accent-primary-start transition-colors"
          />
        </label>
      </div>
      <label class="flex items-center gap-2.5 mt-4 cursor-pointer select-none">
        <input
          bind:checked={draft.editor.focus_highlight_ancestors}
          onchange={touch}
          type="checkbox"
          class="w-4 h-4 accent-[#10b981] cursor-pointer"
        />
        <span class="text-text-primary text-[13px] font-body-md">
          Highlight ancestor blocks when a nested item is focused
        </span>
      </label>
    </section>

    <!-- Hotkeys -->
    <section>
      <h3
        class="font-label-sm-bold text-text-muted uppercase tracking-widest text-[10px] mb-3"
      >
        Hotkeys
      </h3>
      <div class="space-y-2">
        {#if hotkeyEntries.length === 0}
          <p class="text-text-muted text-[12px] font-body-md italic">
            No hotkeys configured.
          </p>
        {:else}
          {#each hotkeyEntries as [key, value] (key)}
            <label class="flex items-center gap-3">
              <span
                class="text-text-muted text-[12px] font-label-sm w-48 truncate"
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
                class="flex-1 bg-bg-surface border border-border-zinc rounded-lg px-3 py-1.5 text-text-primary text-[12px] font-mono outline-none focus:border-accent-primary-start transition-colors"
              />
            </label>
          {/each}
        {/if}
      </div>
    </section>

    <!-- Error banner -->
    {#if settings.error}
      <div
        class="flex items-start gap-2 p-3 rounded-lg bg-error/10 border border-error/30 text-error text-[12px] font-body-md"
      >
        <span class="material-symbols-outlined text-[18px]">error</span>
        <span class="flex-1">{settings.error}</span>
      </div>
    {/if}

    <!-- Actions -->
    <div
      class="flex items-center justify-end gap-2 pt-2 border-t border-border-muted"
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
