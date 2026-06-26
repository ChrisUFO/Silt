<script lang="ts">
  import { onMount } from 'svelte'
  import { GetAppVersion } from '../../../wailsjs/go/main/App.js'
  import { BrowserOpenURL } from '../../../wailsjs/runtime/runtime.js'
  import logo from '../../assets/logo.svg'
  import {
    updateState,
    loadSettings,
    checkNow,
    downloadAndInstall,
    setAutoCheck
  } from '../../updates/store.svelte'

  let version = $state('…')

  onMount(async () => {
    try {
      version = await GetAppVersion()
    } catch {
      version = 'unknown'
    }
    await loadSettings()
  })

  function formatLastChecked(iso: string): string {
    if (!iso) return 'Never'
    const t = Date.parse(iso)
    if (Number.isNaN(t)) return 'Never'
    return new Date(t).toLocaleString()
  }

  // The changelog excerpt: the first few content-bearing lines of the release
  // notes. GitHub release notes typically open with section headers (## New,
  // ## Fixes, …) which carry no signal in a raw-text preview, so heading-only
  // lines are filtered out first; if the whole excerpt is headings, fall back
  // to the leading lines so the panel is never empty.
  function notesExcerpt(notes: string): string {
    const lines = notes.split('\n').filter((l) => l.trim())
    const content = lines.filter((l) => !/^\s{0,3}#{1,6}\s/.test(l))
    const picked = content.length > 0 ? content : lines
    return picked.slice(0, 6).join('\n')
  }

  async function onCheck() {
    await checkNow()
  }

  async function onInstall() {
    if (!updateState.assetUrl) return
    await downloadAndInstall(updateState.assetUrl)
  }

  // External links keep real <a> semantics (links list, ctrl/middle-click,
  // keyboard focus) but route through the Wails bridge so they open in the
  // OS browser rather than the webview. preventDefault stops the webview
  // navigation; the href stays for a11y and shortcut support.
  function openExternal(e: MouseEvent): void {
    e.preventDefault()
    const url = (e.currentTarget as HTMLAnchorElement).href
    if (url) BrowserOpenURL(url)
  }
</script>

<div class="p-6 max-w-2xl">
  <div class="flex items-center gap-4 mb-6">
    <img src={logo} alt="Silt" class="w-14 h-14" />
    <div>
      <h3 class="font-headline-md text-headline-md text-text-primary font-bold">
        Silt
      </h3>
      <p class="text-text-muted text-[12px] font-label-sm">Version {version}</p>
    </div>
  </div>

  <p class="text-text-primary text-[13px] font-body-md mb-6">
    Capture ideas. Connect them. Get work done. A fast, private workspace for
    your notes and tasks.
  </p>

  <!-- Updates (#312) -->
  <section class="mb-6">
    <h3
      class="font-label-sm-bold text-text-muted uppercase tracking-widest text-[10px] mb-3"
    >
      Updates
    </h3>

    <div
      class="bg-surface border border-border-muted rounded-lg px-4 py-3 space-y-3"
    >
      <div class="flex items-center justify-between gap-3">
        <button
          type="button"
          onclick={onCheck}
          disabled={updateState.status === 'checking' ||
            updateState.status === 'downloading' ||
            updateState.status === 'installing'}
          class="font-label-sm-bold text-[12px] px-3 py-1.5 rounded-md bg-accent-primary-start text-surface hover:brightness-110 disabled:opacity-50 disabled:cursor-not-allowed border-none cursor-pointer"
        >
          {updateState.status === 'checking'
            ? 'Checking…'
            : 'Check for updates'}
        </button>
        <span class="text-text-muted text-[11px] font-label-sm">
          Last checked: {formatLastChecked(updateState.lastChecked)}
        </span>
      </div>

      <!-- Live status region: polite so screen readers announce results
           without stealing focus. -->
      <div class="text-[12px] font-body-md" role="status" aria-live="polite">
        {#if updateState.status === 'checking'}
          <p class="text-text-muted">Checking GitHub Releases…</p>
        {:else if updateState.status === 'up-to-date'}
          <p class="text-text-muted">
            <span
              class="material-symbols-outlined text-[16px] align-middle mr-1"
              >check_circle</span
            >
            You're up to date.
          </p>
        {:else if updateState.status === 'available'}
          <div class="space-y-1.5">
            <p class="text-text-primary">
              Silt {updateState.latestVersion} is available.
            </p>
            {#if notesExcerpt(updateState.releaseNotes)}
              <pre
                class="text-text-muted text-[11px] whitespace-pre-wrap bg-bg/40 rounded-md p-2 border border-border-muted"
                style="font-family: var(--editor-mono-font-family, var(--font-mono, monospace))">{notesExcerpt(
                  updateState.releaseNotes
                )}</pre>
            {/if}
            <div class="flex items-center gap-3 pt-1">
              {#if updateState.assetUrl}
                <button
                  type="button"
                  onclick={onInstall}
                  class="font-label-sm-bold text-[12px] px-3 py-1.5 rounded-md bg-accent-primary-start text-surface hover:brightness-110 border-none cursor-pointer"
                >
                  Install update
                </button>
              {/if}
              <a
                href={updateState.releaseUrl}
                onclick={openExternal}
                class="font-label-sm-bold text-[12px] underline text-accent-primary-start hover:brightness-110"
              >
                View full notes
              </a>
            </div>
          </div>
        {:else if updateState.status === 'downloading'}
          <div class="space-y-1.5">
            <p class="text-text-muted">Downloading…</p>
            {#if updateState.downloadProgress !== null && updateState.downloadProgress >= 0}
              <div
                class="w-full h-1.5 rounded-full bg-border-muted overflow-hidden"
                role="progressbar"
                aria-valuenow={updateState.downloadProgress}
                aria-valuemin={0}
                aria-valuemax={100}
              >
                <div
                  class="h-full bg-accent-primary-start transition-[width] duration-150"
                  style="width: {updateState.downloadProgress}%"
                ></div>
              </div>
            {:else}
              <!-- Indeterminate: total unknown. -->
              <div
                class="w-full h-1.5 rounded-full bg-border-muted overflow-hidden"
                role="progressbar"
                aria-label="Downloading update"
              >
                <div
                  class="h-full w-1/3 bg-accent-primary-start animate-pulse"
                ></div>
              </div>
            {/if}
          </div>
        {:else if updateState.status === 'installing'}
          <p class="text-text-muted">
            Installing… Silt will restart when the update is ready.
          </p>
        {/if}
      </div>

      <!-- Errors get the alert role so they are announced assertively. -->
      {#if updateState.status === 'error'}
        <p
          class="text-status-danger text-[12px] font-body-md flex items-center gap-1.5"
          role="alert"
        >
          <span class="material-symbols-outlined text-[16px]">error</span>
          {updateState.error}
        </p>
      {/if}

      <!-- Auto-check toggle: default on. role=switch + aria-checked per the
           a11y rules; Space/Toggle operate natively as a button. Disabled
           while a save is in flight so two rapid flips cannot race. -->
      <label
        class="flex items-center justify-between gap-3 pt-2 border-t border-border-muted {updateState.autoCheckInflight
          ? ''
          : 'cursor-pointer'}"
      >
        <span class="text-text-primary text-[12px] font-body-md">
          Automatically check for updates
        </span>
        <button
          type="button"
          role="switch"
          aria-checked={updateState.autoCheck}
          aria-label="Automatically check for updates"
          disabled={updateState.autoCheckInflight}
          onclick={() => setAutoCheck(!updateState.autoCheck)}
          class="relative w-9 h-5 rounded-full transition-colors border-none {updateState.autoCheckInflight
            ? 'cursor-wait opacity-60'
            : 'cursor-pointer'} {updateState.autoCheck
            ? 'bg-accent-primary-start'
            : 'bg-border-muted'}"
        >
          <span
            class="absolute top-0.5 left-0.5 w-4 h-4 rounded-full bg-surface transition-transform {updateState.autoCheck
              ? 'translate-x-4'
              : ''}"
          ></span>
        </button>
      </label>
    </div>
  </section>

  <section>
    <h3
      class="font-label-sm-bold text-text-muted uppercase tracking-widest text-[10px] mb-2"
    >
      Links
    </h3>
    <ul class="space-y-1.5 text-[13px] font-body-md">
      <li>
        <span class="text-text-muted">Source:</span>
        <a
          href="https://github.com/Chelydra-Labs/Silt"
          onclick={openExternal}
          class="text-accent-primary-start hover:brightness-110"
          >github.com/Chelydra-Labs/Silt</a
        >
      </li>
      <li>
        <span class="text-text-muted">Issues &amp; feedback:</span>
        <a
          href="https://github.com/Chelydra-Labs/Silt/issues"
          onclick={openExternal}
          class="text-accent-primary-start hover:brightness-110"
          >github.com/Chelydra-Labs/Silt/issues</a
        >
      </li>
    </ul>
  </section>

  <p class="text-text-muted text-[11px] font-label-sm mt-8">
    Built with Go, Svelte 5, and Wails.
  </p>

  <p class="text-text-muted text-[11px] font-label-sm mt-1">
    A <a
      href="https://chelydra.dev"
      onclick={openExternal}
      class="text-accent-primary-start hover:brightness-110">Chelydra Labs</a
    > project.
  </p>
</div>
