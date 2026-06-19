<script lang="ts">
  import { onMount } from 'svelte'
  import { EventsOn } from '../../../wailsjs/runtime/runtime.js'
  import {
    PickVaultExportPath,
    ExportVault,
    PickVaultArchive,
    PickVaultDestination,
    ImportVault
  } from '../../../wailsjs/go/main/App.js'

  // ExportVault's runtime return shape (matches main.ExportResult in
  // wailsjs/go/models.ts). Declared locally so this component does not depend
  // on the generated `main` namespace import path.
  interface ExportResultShape {
    files_archived: number
    bytes_archived: number
    page_file_count: number
    skipped_index: boolean
    skipped_symlinks: number
  }

  interface Props {
    mode: 'export' | 'import'
    currentPath: string
    onClose: () => void
  }

  let { mode, currentPath, onClose }: Props = $props()

  // Export state.
  let exportDest = $state('')
  // Import state: an archive source + an empty destination folder.
  let archivePath = $state('')
  let importDest = $state('')

  let busy = $state(false)
  let error = $state('')
  // Streaming progress: {phase, current, total} from vault:archive:progress.
  let progress = $state<{ phase: string; current: number; total: number } | null>(null)
  // Success screen.
  let done = $state<
    | null
    | {
        kind: 'export' | 'import'
        path: string
        files: number
        bytes: number
        pages: number
        skippedSymlinks: number
      }
  >(null)

  let dialogEl = $state<HTMLDivElement | null>(null)
  let previouslyFocused: HTMLElement | null = null
  let firstBtn = $state<HTMLButtonElement | null>(null)
  const FOCUSABLE =
    'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

  function focusableElements(): HTMLElement[] {
    if (!dialogEl) return []
    return Array.from(dialogEl.querySelectorAll<HTMLElement>(FOCUSABLE))
  }

  onMount(() => {
    previouslyFocused = document.activeElement as HTMLElement
    queueMicrotask(() => firstBtn?.focus())
    return () => previouslyFocused?.focus?.()
  })

  // Streaming progress arrives as a Wails event during the (long) export/
  // import. Subscribed for the lifetime of the modal; the backend stops
  // emitting when the call returns. Re-mounting (re-opening) re-subscribes.
  let offProgress: (() => void) | null = null
  $effect(() => {
    offProgress = EventsOn('vault:archive:progress', (p: any) => {
      if (p && typeof p.current === 'number' && typeof p.total === 'number') {
        progress = { phase: String(p.phase ?? ''), current: p.current, total: p.total }
      }
    })
    return () => {
      offProgress?.()
    }
  })

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      if (busy) return // don't interrupt an in-flight archive/extract
      e.preventDefault()
      e.stopPropagation()
      onClose()
      return
    }
    if (e.key === 'Tab' && dialogEl) {
      const els = focusableElements()
      if (els.length === 0) return
      const active = document.activeElement as HTMLElement | null
      const first = els[0]
      const last = els[els.length - 1]
      if (e.shiftKey && (active === first || !dialogEl.contains(active))) {
        e.preventDefault()
        last.focus()
      } else if (!e.shiftKey && (active === last || !dialogEl.contains(active))) {
        e.preventDefault()
        first.focus()
      }
    }
  }

  async function chooseExportDest() {
    error = ''
    try {
      // filter(Boolean): a trailing slash on currentPath (e.g.
      // "/home/user/MyVault/") would otherwise make .pop() return "" and fall
      // back to "vault" instead of "MyVault".
      const segments = currentPath.split(/[\\/]/).filter(Boolean)
      const defaultName = (segments.pop() || 'vault') + '.silt-vault'
      const picked = await PickVaultExportPath(defaultName)
      if (picked) exportDest = picked
    } catch (e) {
      error = errMsg(e)
    }
  }

  async function chooseArchive() {
    error = ''
    try {
      const picked = await PickVaultArchive()
      if (picked) archivePath = picked
    } catch (e) {
      error = errMsg(e)
    }
  }

  async function chooseImportDest() {
    error = ''
    try {
      const picked = await PickVaultDestination()
      if (picked) importDest = picked
    } catch (e) {
      error = errMsg(e)
    }
  }

  let canCommit = $derived(
    !busy &&
      !done &&
      (mode === 'export'
        ? exportDest !== ''
        : // Import needs both an archive AND an empty destination that is not
          // the currently-open vault (SwitchVault refuses a same-path).
          archivePath !== '' &&
            importDest !== '' &&
            importDest !== currentPath)
  )

  let primaryLabel = $derived(mode === 'export' ? 'Export vault' : 'Import vault')

  function fmtBytes(n: number): string {
    if (n < 1024) return `${n} B`
    if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
    return `${(n / (1024 * 1024)).toFixed(1)} MB`
  }

  let pct = $derived(
    progress && progress.total > 0
      ? Math.min(100, Math.round((progress.current / progress.total) * 100))
      : 0
  )

  let progressLabel = $derived(
    progress
      ? mode === 'export'
        ? `Archiving ${progress.current} of ${progress.total} files…`
        : `Extracting ${progress.current} of ${progress.total} files…`
      : ''
  )

  async function commit() {
    if (!canCommit) return
    busy = true
    error = ''
    progress = null
    try {
      if (mode === 'export') {
        const res = (await ExportVault(exportDest)) as ExportResultShape
        done = {
          kind: 'export',
          path: exportDest,
          files: res.files_archived,
          bytes: res.bytes_archived,
          pages: res.page_file_count ?? 0,
          skippedSymlinks: res.skipped_symlinks ?? 0
        }
      } else {
        const res = (await ImportVault(archivePath, importDest)) as {
          files_extracted: number
          bytes_extracted: number
          page_file_count?: number
        }
        // ImportVault reuses SwitchVault, which emits vault:moved; App.svelte
        // resets nav and closes settings. Build a success card anyway in case
        // the event is handled after this returns.
        done = {
          kind: 'import',
          path: importDest,
          files: res.files_extracted,
          bytes: res.bytes_extracted,
          pages: res.page_file_count ?? 0,
          skippedSymlinks: 0
        }
      }
    } catch (e) {
      error = errMsg(e)
    } finally {
      busy = false
      progress = null
    }
  }

  function errMsg(e: unknown): string {
    return e instanceof Error ? e.message : String(e)
  }
</script>

<svelte:window on:keydown={handleKeydown} />

<div class="fixed inset-0 z-[210] flex items-center justify-center p-6">
  <button
    type="button"
    tabindex="-1"
    aria-label="Close"
    title="Close"
    onclick={() => !busy && onClose()}
    class="absolute inset-0 h-full w-full bg-[#000]/40 backdrop-blur-[2px] border-none cursor-default p-0"
  ></button>
  <div
    bind:this={dialogEl}
    role="dialog"
    aria-modal="true"
    aria-labelledby="vault-archive-title"
    tabindex="-1"
    class="relative z-10 w-full max-w-lg glass-palette border border-border-zinc rounded-xl shadow-2xl p-6"
    style="backdrop-filter: blur(16px) saturate(140%); background: rgba(22, 22, 25, 0.94);"
  >
    <div class="flex items-start gap-3 mb-4">
      <span class="material-symbols-outlined text-accent-primary-start text-[24px] mt-0.5">
        {mode === 'export' ? 'archive' : 'unarchive'}
      </span>
      <div class="flex-1 min-w-0">
        <h2 id="vault-archive-title" class="font-headline-md text-headline-md text-text-primary">
          {mode === 'export' ? 'Export vault' : 'Import vault'}
        </h2>
        <p class="text-text-muted text-[12px] font-body-md mt-1">
          {#if mode === 'export'}
            Back up or migrate this vault as a single portable <strong>.silt-vault</strong>
            archive. Your notes, config, themes, templates, and plugins are bundled; the
            search index is rebuilt when the archive is imported.
          {:else}
            Restore a <strong>.silt-vault</strong> archive into a new empty folder and open
            it. The archive is checksum-verified before anything is written.
          {/if}
        </p>
      </div>
    </div>

    {#if !done}
      <!-- Pickers -->
      {#if mode === 'export'}
        <div class="mb-4">
          <span class="text-text-muted text-[11px] font-label-sm-bold">Archive file</span>
          <div
            class="flex items-center gap-2 mt-1.5 bg-bg-surface border border-border-zinc rounded-lg px-3 py-2"
          >
            <span class="material-symbols-outlined text-text-muted text-[18px]">archive</span>
            <span class="text-text-primary text-[13px] font-body-md truncate flex-1">
              {exportDest || 'No file selected'}
            </span>
            <button
              type="button"
              bind:this={firstBtn}
              onclick={chooseExportDest}
              disabled={busy}
              class="flex-shrink-0 px-2.5 py-1 rounded-lg bg-bg-hover border border-border-zinc text-text-primary hover:border-accent-primary-start text-[12px] font-label-sm-bold transition-colors cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
            >
              Choose…
            </button>
          </div>
          <p class="text-text-muted text-[11px] font-label-sm mt-1.5">
            Pick where to save the <strong>.silt-vault</strong> archive.
          </p>
        </div>
      {:else}
        <div class="mb-4">
          <span class="text-text-muted text-[11px] font-label-sm-bold">Archive</span>
          <div
            class="flex items-center gap-2 mt-1.5 bg-bg-surface border border-border-zinc rounded-lg px-3 py-2"
          >
            <span class="material-symbols-outlined text-text-muted text-[18px]">archive</span>
            <span class="text-text-primary text-[13px] font-body-md truncate flex-1">
              {archivePath || 'No archive selected'}
            </span>
            <button
              type="button"
              bind:this={firstBtn}
              onclick={chooseArchive}
              disabled={busy}
              class="flex-shrink-0 px-2.5 py-1 rounded-lg bg-bg-hover border border-border-zinc text-text-primary hover:border-accent-primary-start text-[12px] font-label-sm-bold transition-colors cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
            >
              Choose…
            </button>
          </div>
        </div>
        <div class="mb-4">
          <span class="text-text-muted text-[11px] font-label-sm-bold">Destination folder</span>
          <div
            class="flex items-center gap-2 mt-1.5 bg-bg-surface border border-border-zinc rounded-lg px-3 py-2"
          >
            <span class="material-symbols-outlined text-text-muted text-[18px]">folder</span>
            <span class="text-text-primary text-[13px] font-body-md truncate flex-1">
              {importDest || 'No folder selected'}
            </span>
            <button
              type="button"
              onclick={chooseImportDest}
              disabled={busy}
              class="flex-shrink-0 px-2.5 py-1 rounded-lg bg-bg-hover border border-border-zinc text-text-primary hover:border-accent-primary-start text-[12px] font-label-sm-bold transition-colors cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
            >
              Choose…
            </button>
          </div>
          <p class="text-text-muted text-[11px] font-label-sm mt-1.5">
            The destination must be an empty folder on a local drive.
          </p>
        </div>
      {/if}

      <p class="text-text-muted text-[11px] font-label-sm mb-4">
        <span class="material-symbols-outlined text-[14px] align-middle mr-0.5">link_off</span>
        Linked notebooks are external folders and are never included in the archive.
      </p>

      <!-- Live region: streaming progress + errors -->
      <div aria-live="polite" class="min-h-[20px]">
        {#if busy && progress}
          <div class="mb-1 flex items-center justify-between text-accent-primary-start text-[12px] font-body-md">
            <span class="flex items-center gap-2">
              <span class="material-symbols-outlined text-[16px] animate-spin">progress_activity</span>
              <span>{progressLabel}</span>
            </span>
            <span>{pct}%</span>
          </div>
          <div
            role="progressbar"
            aria-valuenow={pct}
            aria-valuemin={0}
            aria-valuemax={100}
            aria-label={progressLabel}
            class="h-1.5 w-full rounded-full bg-bg-hover overflow-hidden"
          >
            <div
              class="h-full bg-accent-primary-start transition-[width] duration-150"
              style="width: {pct}%"
            ></div>
          </div>
          <p class="text-text-muted text-[11px] font-label-sm mt-1.5">
            This can't be cancelled mid-write — please wait for it to finish.
          </p>
        {:else if busy}
          <div
            class="flex items-center gap-2 text-accent-primary-start text-[12px] font-body-md"
          >
            <span class="material-symbols-outlined text-[16px] animate-spin"
              >progress_activity</span
            >
            <span>{mode === 'export' ? 'Preparing archive…' : 'Verifying archive…'}</span>
          </div>
          <p class="text-text-muted text-[11px] font-label-sm mt-1.5">
            This can't be cancelled mid-write — please wait for it to finish.
          </p>
        {/if}
      </div>
      {#if error}
        <div
          role="alert"
          class="flex items-start gap-2 mt-2 p-3 rounded-lg bg-error/10 border border-error/30 text-error text-[12px] font-body-md"
        >
          <span class="material-symbols-outlined text-[18px]">error</span>
          <span class="flex-1">{error}</span>
        </div>
      {/if}

      <!-- Actions -->
      <div class="flex items-center justify-end gap-2 pt-4 mt-2 border-t border-border-muted">
        <button
          onclick={onClose}
          disabled={busy}
          class="px-4 py-2 rounded-lg text-text-muted hover:text-text-primary font-label-sm-bold transition-colors border-none bg-transparent cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
        >
          Cancel
        </button>
        <button
          onclick={commit}
          disabled={!canCommit}
          class="px-4 py-2 rounded-lg bg-accent-primary-start/20 border border-accent-primary-start/40 text-accent-primary-start font-label-sm-bold hover:brightness-110 transition-all cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {primaryLabel}
        </button>
      </div>
    {:else}
      <!-- Success -->
      <div
        role="status"
        class="flex items-start gap-2 p-3 rounded-lg bg-accent-primary-start/10 border border-accent-primary-start/30 text-accent-primary-start text-[12px] font-body-md"
      >
        <span class="material-symbols-outlined text-[18px]">check_circle</span>
        <div class="flex-1">
          {#if done.kind === 'export'}
            <p>Archived {done.files} files ({fmtBytes(done.bytes)}).</p>
            <p class="text-text-muted text-[11px] font-label-sm mt-1 truncate">
              {done.path}
            </p>
            {#if done.skippedSymlinks > 0}
              <p class="text-status-warn text-[11px] font-label-sm mt-1">
                {done.skippedSymlinks} symlink(s) skipped — not included in the archive.
              </p>
            {/if}
          {:else}
            <p>
              Imported {done.files} files ({fmtBytes(done.bytes)}). The vault is now open.
            </p>
            <p class="text-text-muted text-[11px] font-label-sm mt-1 truncate">
              {done.path}
            </p>
          {/if}
        </div>
      </div>
      <div class="flex items-center justify-end gap-2 pt-4 mt-4 border-t border-border-muted">
        <button
          onclick={onClose}
          disabled={busy}
          class="px-4 py-2 rounded-lg text-text-muted hover:text-text-primary font-label-sm-bold transition-colors border-none bg-transparent cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
        >
          Done
        </button>
      </div>
    {/if}
  </div>
</div>
