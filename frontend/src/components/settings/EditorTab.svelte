<script lang="ts">
  import { untrack } from 'svelte'
  import {
    settings,
    saveConfig,
    reloadFromBackend
  } from '../../settings/store.svelte'
  import type { SystemConfig } from '../../settings/store.svelte'
  import type { config } from '../../../wailsjs/go/models.js'
  import { displayFamilyName } from '../../theme/fonts'
  import { themeState } from '../../theme/store.svelte'
  import FontSelect from './FontSelect.svelte'
  import { customDictionary } from '../../lib/editor/spellcheck/customDictionary.svelte'

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

  // Load the custom spellcheck dictionary for the management card (#196). Runs
  // once on mount; the store keeps it in sync with add/remove via the IPC.
  $effect(() => {
    if (settings.config) void customDictionary.load()
  })

  function touch() {
    settings.dirty = true
  }

  function draftUI(): config.UIConfig {
    if (!draft!.ui) draft!.ui = {} as config.UIConfig
    return draft!.ui as config.UIConfig
  }
  function draftUIFormatting(): config.FormattingConfig {
    const ui = draftUI()
    if (!ui.formatting) ui.formatting = {} as config.FormattingConfig
    return ui.formatting as config.FormattingConfig
  }
  function draftEditor(): config.EditorConfig {
    if (!draft!.editor) draft!.editor = {} as config.EditorConfig
    return draft!.editor as config.EditorConfig
  }

  function changed(): boolean {
    if (!draft || !lastSaved) return false
    return (
      JSON.stringify(draft.editor) !== JSON.stringify(lastSaved.editor) ||
      JSON.stringify(draft.ui) !== JSON.stringify(lastSaved.ui)
    )
  }

  let isValid = $derived(
    draft !== null &&
      draft.editor.font_size_px > 0 &&
      draft.editor.tab_indent_spaces > 0 &&
      draft.editor.line_height > 0 &&
      draft.editor.auto_save_delay_ms >= 0
  )

  let themeBodyFont = $derived(themeState.darkTokens['--font-body'] ?? '')
  let themeMonoFont = $derived(themeState.darkTokens['--font-mono'] ?? '')

  function resetFont(field: 'font_family' | 'mono_font_family') {
    if (!draft) return
    draft.editor[field] = ''
    touch()
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

      <!-- Typography Card -->
      <div
        class="bg-surface/20 border border-border-muted rounded-xl p-5 space-y-5"
      >
        <h4
          class="font-label-sm-bold text-text-primary uppercase tracking-wider text-[10px]"
        >
          Typography
        </h4>
        <div class="grid grid-cols-2 gap-4">
          <label class="flex flex-col gap-1.5">
            <span
              class="text-text-muted text-[10px] font-semibold uppercase tracking-wider"
              >Font family</span
            >
            <div class="flex items-center gap-2">
              <FontSelect
                bind:value={draft.editor.font_family}
                category="body"
                themeFont={themeBodyFont}
                label="Font family"
                onchange={touch}
              />
              {#if themeBodyFont}
                <button
                  type="button"
                  onclick={() => resetFont('font_family')}
                  title="Reset to theme default ({displayFamilyName(
                    themeBodyFont
                  )})"
                  aria-label="Reset body font to theme default"
                  class="flex-shrink-0 px-2.5 py-2 rounded-lg bg-surface border border-border-zinc text-text-muted hover:text-text-primary hover:border-accent-primary-start transition-colors cursor-pointer"
                >
                  <span class="material-symbols-outlined text-[18px]"
                    >restart_alt</span
                  >
                </button>
              {/if}
            </div>
          </label>

          <label class="flex flex-col gap-1.5">
            <span
              class="text-text-muted text-[10px] font-semibold uppercase tracking-wider"
              >Monospace font</span
            >
            <div class="flex items-center gap-2">
              <FontSelect
                bind:value={draft.editor.mono_font_family}
                category="mono"
                themeFont={themeMonoFont}
                label="Monospace font"
                onchange={touch}
              />
              {#if themeMonoFont}
                <button
                  type="button"
                  onclick={() => resetFont('mono_font_family')}
                  title="Reset to theme default ({displayFamilyName(
                    themeMonoFont
                  )})"
                  aria-label="Reset monospace font to theme default"
                  class="flex-shrink-0 px-2.5 py-2 rounded-lg bg-surface border border-border-zinc text-text-muted hover:text-text-primary hover:border-accent-primary-start transition-colors cursor-pointer"
                >
                  <span class="material-symbols-outlined text-[18px]"
                    >restart_alt</span
                  >
                </button>
              {/if}
            </div>
          </label>

          <label class="flex flex-col gap-1.5">
            <span
              class="text-text-muted text-[10px] font-semibold uppercase tracking-wider"
              >Font size (px)</span
            >
            <input
              bind:value={draft.editor.font_size_px}
              oninput={touch}
              type="number"
              min="8"
              max="48"
              class="bg-surface border border-border-zinc rounded-lg px-3 py-2 text-text-primary text-[13px] font-body-md outline-none focus:border-accent-primary-start transition-colors"
            />
          </label>

          <label class="flex flex-col gap-1.5">
            <span
              class="text-text-muted text-[10px] font-semibold uppercase tracking-wider"
              >Line height</span
            >
            <input
              bind:value={draft.editor.line_height}
              oninput={touch}
              type="number"
              step="0.1"
              min="1"
              max="3"
              class="bg-surface border border-border-zinc rounded-lg px-3 py-2 text-text-primary text-[13px] font-body-md outline-none focus:border-accent-primary-start transition-colors"
            />
          </label>

          <label class="flex flex-col gap-1.5">
            <span
              class="text-text-muted text-[10px] font-semibold uppercase tracking-wider"
              >Tab width (spaces)</span
            >
            <input
              bind:value={draft.editor.tab_indent_spaces}
              oninput={touch}
              type="number"
              min="1"
              max="8"
              class="bg-surface border border-border-zinc rounded-lg px-3 py-2 text-text-primary text-[13px] font-body-md outline-none focus:border-accent-primary-start transition-colors"
            />
          </label>
        </div>
      </div>

      <!-- Preferences Card -->
      <div
        class="bg-surface/20 border border-border-muted rounded-xl p-5 space-y-5"
      >
        <h4
          class="font-label-sm-bold text-text-primary uppercase tracking-wider text-[10px]"
        >
          Writing Preferences
        </h4>
        <div class="space-y-4">
          <label class="flex flex-col gap-1.5 max-w-xs">
            <span
              class="text-text-muted text-[10px] font-semibold uppercase tracking-wider"
              >Auto-save delay (ms)</span
            >
            <input
              bind:value={draft.editor.auto_save_delay_ms}
              oninput={touch}
              type="number"
              min="0"
              step="100"
              class="bg-surface border border-border-zinc rounded-lg px-3 py-2 text-text-primary text-[13px] font-body-md outline-none focus:border-accent-primary-start transition-colors"
            />
          </label>

          <div class="grid grid-cols-2 gap-3 pt-2">
            <label class="flex items-center gap-2.5 cursor-pointer select-none">
              <input
                bind:checked={draft.editor.focus_highlight_ancestors}
                onchange={touch}
                type="checkbox"
                class="w-4 h-4 accent-[#10b981] cursor-pointer"
              />
              <span class="text-text-primary text-[13px] font-body-md">
                Highlight ancestor blocks on focus
              </span>
            </label>

            <label class="flex items-center gap-2.5 cursor-pointer select-none">
              <input
                checked={draft.ui?.show_format_toolbar !== false}
                onchange={(e: Event) => {
                  draftUI().show_format_toolbar = (
                    e.currentTarget as HTMLInputElement
                  ).checked
                  touch()
                }}
                type="checkbox"
                class="w-4 h-4 accent-[#10b981] cursor-pointer"
              />
              <span class="text-text-primary text-[13px] font-body-md">
                Show format toolbar
              </span>
            </label>

            <label class="flex items-center gap-2.5 cursor-pointer select-none">
              <input
                checked={draft.ui?.formatting?.typography_enabled !== false}
                onchange={(e: Event) => {
                  draftUIFormatting().typography_enabled = (
                    e.currentTarget as HTMLInputElement
                  ).checked
                  touch()
                }}
                type="checkbox"
                class="w-4 h-4 accent-[#10b981] cursor-pointer"
              />
              <span class="text-text-primary text-[13px] font-body-md">
                Smart typography (em-dash, smart quotes)
              </span>
            </label>

            <label class="flex items-center gap-2.5 cursor-pointer select-none">
              <input
                checked={draft.ui?.formatting?.color_enabled !== false}
                onchange={(e: Event) => {
                  draftUIFormatting().color_enabled = (
                    e.currentTarget as HTMLInputElement
                  ).checked
                  touch()
                }}
                type="checkbox"
                class="w-4 h-4 accent-[#10b981] cursor-pointer"
              />
              <span class="text-text-primary text-[13px] font-body-md">
                Text and background color pickers
              </span>
            </label>

            <label class="flex items-center gap-2.5 cursor-pointer select-none">
              <input
                checked={draft.editor?.show_word_count === true}
                onchange={(e: Event) => {
                  draftEditor().show_word_count = (
                    e.currentTarget as HTMLInputElement
                  ).checked
                  touch()
                }}
                type="checkbox"
                class="w-4 h-4 accent-[#10b981] cursor-pointer"
              />
              <span class="text-text-primary text-[13px] font-body-md">
                Show word count
              </span>
            </label>

            <label class="flex items-center gap-2.5 cursor-pointer select-none">
              <input
                checked={draft.editor?.focus_mode === true}
                onchange={(e: Event) => {
                  draftEditor().focus_mode = (
                    e.currentTarget as HTMLInputElement
                  ).checked
                  touch()
                }}
                type="checkbox"
                class="w-4 h-4 accent-[#10b981] cursor-pointer"
              />
              <span class="text-text-primary text-[13px] font-body-md">
                Focus mode (dim inactive paragraphs)
              </span>
            </label>

            <label class="flex items-center gap-2.5 cursor-pointer select-none">
              <input
                checked={draft.ui?.open_devtools_on_startup === true}
                onchange={(e: Event) => {
                  draftUI().open_devtools_on_startup = (
                    e.currentTarget as HTMLInputElement
                  ).checked
                  touch()
                }}
                type="checkbox"
                class="w-4 h-4 accent-[#10b981] cursor-pointer"
              />
              <span class="text-text-primary text-[13px] font-body-md">
                Open DevTools on startup
              </span>
            </label>

            <label class="flex items-center gap-2.5 cursor-pointer select-none">
              <input
                checked={draft.editor?.spellcheck_enabled !== false}
                onchange={(e: Event) => {
                  draftEditor().spellcheck_enabled = (
                    e.currentTarget as HTMLInputElement
                  ).checked
                  touch()
                }}
                type="checkbox"
                class="w-4 h-4 accent-[#10b981] cursor-pointer"
              />
              <span class="text-text-primary text-[13px] font-body-md">
                Spellcheck (underline misspelled words)
              </span>
            </label>

            <label class="flex items-center gap-2.5 cursor-pointer select-none">
              <input
                checked={draft.editor?.typewriter_mode === true}
                onchange={(e: Event) => {
                  draftEditor().typewriter_mode = (
                    e.currentTarget as HTMLInputElement
                  ).checked
                  touch()
                }}
                type="checkbox"
                class="w-4 h-4 accent-[#10b981] cursor-pointer"
              />
              <span class="text-text-primary text-[13px] font-body-md">
                Typewriter mode (keep active line centered)
              </span>
            </label>
          </div>
        </div>
      </div>

      <!-- Custom spellcheck dictionary (#196). Vault-scoped; edited via the
           atomic config-RMW IPC in app_spellcheck.go. -->
      <div
        class="rounded-xl border border-border-muted bg-surface/5 p-4 space-y-3"
      >
        <div class="flex items-center justify-between">
          <div>
            <h3 class="text-text-primary text-[14px] font-label-sm-bold">
              Custom dictionary
            </h3>
            <p class="text-text-muted text-[12px] font-body-md mt-0.5">
              Words you've added so they aren't flagged. Right-click a
              misspelled word in the editor → "Add to dictionary", or add one
              here.
            </p>
          </div>
        </div>
        <div class="flex items-center gap-2">
          <input
            bind:value={customDictionary.newWord}
            placeholder="Add a word"
            onkeydown={(e: KeyboardEvent) => {
              if (e.key === 'Enter') {
                e.preventDefault()
                void customDictionary.add()
              }
            }}
            class="flex-1 px-2.5 py-1.5 rounded-lg bg-void border border-border-muted text-text-primary text-[13px] font-body-md focus:outline-none focus:border-accent-primary-start/60"
          />
          <button
            type="button"
            onclick={() => void customDictionary.add()}
            disabled={!customDictionary.newWord.trim()}
            class="px-3 py-1.5 rounded-lg bg-accent-primary-start/20 border border-accent-primary-start/40 text-accent-primary-start text-[13px] font-label-sm-bold hover:brightness-110 transition-all cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
          >
            Add
          </button>
        </div>
        <input
          bind:value={customDictionary.filter}
          placeholder="Filter words…"
          class="w-full px-2.5 py-1.5 rounded-lg bg-void border border-border-muted text-text-primary text-[13px] font-body-md focus:outline-none focus:border-accent-primary-start/60"
        />
        {#if customDictionary.error}
          <p class="text-error text-[12px] font-body-md">
            {customDictionary.error}
          </p>
        {/if}
        <div
          class="max-h-48 overflow-y-auto rounded-lg border border-border-muted/60"
        >
          {#if customDictionary.filtered.length === 0}
            <p class="text-text-muted text-[12px] font-body-md p-3 text-center">
              {customDictionary.loading
                ? 'Loading…'
                : customDictionary.words.length === 0
                  ? 'No custom words yet.'
                  : 'No words match the filter.'}
            </p>
          {:else}
            {#each customDictionary.filtered as word (word)}
              <div
                class="flex items-center justify-between px-2.5 py-1.5 hover:bg-surface/20"
              >
                <span class="text-text-primary text-[13px] font-body-md"
                  >{word}</span
                >
                <button
                  type="button"
                  aria-label="Remove {word}"
                  title="Remove"
                  onclick={() => void customDictionary.remove(word)}
                  class="text-text-muted hover:text-error transition-colors cursor-pointer"
                  >✕</button
                >
              </div>
            {/each}
          {/if}
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
