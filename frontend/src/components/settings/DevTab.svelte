<script lang="ts">
  import { settings } from '../../settings/store.svelte'
  import type { config } from '../../../wailsjs/go/models.js'

  let draft = $state<config.SystemConfig | null>(null)

  $effect(() => {
    if (settings.config) {
      draft = structuredClone(settings.config)
    }
  })

  function touch() {
    settings.dirty = true
  }

  function draftUI(): config.UIConfig {
    if (!draft!.ui) draft!.ui = {} as config.UIConfig
    return draft!.ui as config.UIConfig
  }

  function changed(): boolean {
    return settings.dirty
  }

  async function handleSave() {
    if (!draft) return
    try {
      settings.saving = true
      const { SaveSystemConfig } = await import('../../../wailsjs/go/main/App.js')
      await SaveSystemConfig(draft)
      settings.config = draft
      settings.dirty = false
    } catch (e) {
      settings.error = e instanceof Error ? e.message : String(e)
    } finally {
      settings.saving = false
    }
  }

  function handleRevert() {
    if (settings.config) {
      draft = structuredClone(settings.config)
    }
  }
</script>

<div class="p-6 max-w-2xl space-y-6">
  <div
    class="bg-surface/20 border border-border-muted rounded-xl p-5 space-y-4"
  >
    <h4
      class="font-label-sm-bold text-text-primary uppercase tracking-wider text-[10px]"
    >
      Developer Tools
    </h4>

    <div class="space-y-3">
      <label class="flex items-center gap-2.5 cursor-pointer select-none">
        <input
          checked={draft?.ui?.open_devtools_on_startup === true}
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

      <p class="text-text-muted/70 text-[11px] font-body-md leading-relaxed pl-7">
        Opens the Chromium inspector when Silt launches, so you can view console
        errors, inspect the DOM, and debug rendering issues.
        <br />
        Press <kbd
          class="inline-block px-1.5 py-0.5 rounded bg-surface border border-border-muted text-text-primary text-[10px] font-mono"
          >Ctrl+Shift+F12</kbd
        > to open DevTools manually at any time (requires a build with the
        <code>-devtools</code> flag).
      </p>
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

  <div class="flex items-center justify-end gap-2 pt-4 border-t border-border-muted">
    <button
      onclick={handleRevert}
      disabled={!changed()}
      class="px-4 py-2 rounded-lg text-text-muted hover:text-text-primary font-label-sm-bold transition-colors border-none bg-transparent cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
    >
      Revert
    </button>
    <button
      onclick={handleSave}
      disabled={!changed() || settings.saving}
      class="px-4 py-2 rounded-lg bg-accent-primary-start/20 border border-accent-primary-start/40 text-accent-primary-start font-label-sm-bold hover:brightness-110 transition-all cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
    >
      {settings.saving ? 'Saving…' : 'Save changes'}
    </button>
  </div>
</div>
