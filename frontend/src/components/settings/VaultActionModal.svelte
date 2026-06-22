<script lang="ts">
  import { onMount } from 'svelte'
  import {
    PickVaultDestination,
    MoveVault,
    CopyVault,
    SwitchVault
  } from '../../../wailsjs/go/main/App.js'

  // CopyVault's runtime return shape (matches main.CopyResult in
  // wailsjs/go/models.ts). Declared locally so this component does not depend
  // on the generated `main` namespace import path.
  interface CopyResultShape {
    files_copied: number
    bytes_copied: number
    skipped_index: boolean
    skipped_symlinks: number
  }

  interface Props {
    mode: 'move' | 'copy'
    currentPath: string
    onClose: () => void
  }

  let { mode, currentPath, onClose }: Props = $props()

  let destination = $state('')
  let removeOld = $state(false)
  // For the destructive "delete original" branch, require a second explicit
  // confirmation so a single stray click can't orphan a vault.
  let confirmDelete = $state(false)
  let busy = $state(false)
  let error = $state('')
  let done = $state<null | {
    path: string
    files: number
    bytes: number
    skippedSymlinks: number
  }>(null)

  let dialogEl = $state<HTMLDivElement | null>(null)
  let previouslyFocused: HTMLElement | null = null
  let destBtn = $state<HTMLButtonElement | null>(null)
  const FOCUSABLE =
    'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

  function focusableElements(): HTMLElement[] {
    if (!dialogEl) return []
    return Array.from(dialogEl.querySelectorAll<HTMLElement>(FOCUSABLE))
  }

  onMount(() => {
    previouslyFocused = document.activeElement as HTMLElement
    queueMicrotask(() => destBtn?.focus())
    return () => previouslyFocused?.focus?.()
  })

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      if (busy) return // don't interrupt an in-flight move/copy
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
      // Wrap at the boundaries AND when focus is outside the dialog's
      // focusable set (e.g. it landed on the dialog container itself or was
      // momentarily lost) — otherwise Tab/Shift+Tab would escape the trap.
      if (e.shiftKey && (active === first || !dialogEl.contains(active))) {
        e.preventDefault()
        last.focus()
      } else if (
        !e.shiftKey &&
        (active === last || !dialogEl.contains(active))
      ) {
        e.preventDefault()
        first.focus()
      }
    }
  }

  async function chooseDestination() {
    error = ''
    try {
      const picked = await PickVaultDestination()
      if (picked) destination = picked
    } catch (e) {
      error = errMsg(e)
    }
  }

  let canCommit = $derived(
    !busy &&
      destination !== '' &&
      destination !== currentPath &&
      // Move's delete-original branch needs the nested confirm checked.
      !(mode === 'move' && removeOld && !confirmDelete)
  )

  let primaryLabel = $derived(mode === 'move' ? 'Move vault' : 'Copy vault')

  function fmtBytes(n: number): string {
    if (n < 1024) return `${n} B`
    if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
    return `${(n / (1024 * 1024)).toFixed(1)} MB`
  }

  async function commit() {
    if (!canCommit) return
    busy = true
    error = ''
    try {
      if (mode === 'move') {
        await MoveVault(destination, removeOld)
        // The backend emits vault:moved; App.svelte resets nav and closes
        // settings. Close locally too so the modal unmounts even if the
        // event is handled after this returns.
        onClose()
      } else {
        const res = (await CopyVault(destination)) as CopyResultShape
        done = {
          path: destination,
          files: res.files_copied,
          bytes: res.bytes_copied,
          // Symlinks aren't followed (filepath.WalkDir), so a symlinked
          // notebook is absent from the copy — surface the count so the user
          // knows the copy is incomplete.
          skippedSymlinks: res.skipped_symlinks ?? 0
        }
      }
    } catch (e) {
      error = errMsg(e)
    } finally {
      busy = false
    }
  }

  async function switchToCopy() {
    if (!done) return
    busy = true
    error = ''
    try {
      // SwitchVault emits vault:moved; App.svelte resets nav + closes settings.
      await SwitchVault(done.path)
      onClose()
    } catch (e) {
      error = errMsg(e)
    } finally {
      busy = false
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
    aria-labelledby="vault-action-title"
    tabindex="-1"
    class="relative z-10 w-full max-w-lg glass-palette border border-border-zinc rounded-xl shadow-2xl p-6"
    style="backdrop-filter: blur(16px) saturate(140%); background: color-mix(in srgb, var(--color-panel) 94%, transparent);"
  >
    <div class="flex items-start gap-3 mb-4">
      <span
        class="material-symbols-outlined text-accent-primary-start text-[24px] mt-0.5"
      >
        {mode === 'move' ? 'drive_file_move' : 'content_copy'}
      </span>
      <div class="flex-1 min-w-0">
        <h2
          id="vault-action-title"
          class="font-headline-md text-headline-md text-text-primary"
        >
          {mode === 'move' ? 'Move vault' : 'Copy vault'}
        </h2>
        <p class="text-text-muted text-[12px] font-body-md mt-1">
          {#if mode === 'move'}
            Relocate this vault to a new folder. Your notes, config, themes,
            templates, and plugins come along; the search index is rebuilt at
            the new location.
          {:else}
            Create a full copy of this vault at a new folder. The copy is a
            separate workspace — this vault stays active. Its search index is
            rebuilt when first opened.
          {/if}
        </p>
      </div>
    </div>

    {#if !done}
      <!-- Destination picker -->
      <div class="mb-4">
        <span class="text-text-muted text-[11px] font-label-sm-bold"
          >Destination</span
        >
        <div
          class="flex items-center gap-2 mt-1.5 bg-surface border border-border-zinc rounded-lg px-3 py-2"
        >
          <span class="material-symbols-outlined text-text-muted text-[18px]"
            >folder</span
          >
          <span
            class="text-text-primary text-[13px] font-body-md truncate flex-1"
          >
            {destination || 'No folder selected'}
          </span>
          <button
            type="button"
            bind:this={destBtn}
            onclick={chooseDestination}
            disabled={busy}
            class="flex-shrink-0 px-2.5 py-1 rounded-lg bg-hover border border-border-zinc text-text-primary hover:border-accent-primary-start text-[12px] font-label-sm-bold transition-colors cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
          >
            Choose…
          </button>
        </div>
        <p class="text-text-muted text-[11px] font-label-sm mt-1.5">
          The destination must be an empty folder on a local drive.
        </p>
      </div>

      {#if mode === 'move'}
        <label class="flex items-start gap-2.5 mb-2 cursor-pointer select-none">
          <input
            bind:checked={removeOld}
            disabled={busy}
            type="checkbox"
            class="w-4 h-4 mt-0.5 accent-[#10b981] cursor-pointer"
          />
          <span class="text-text-primary text-[12px] font-body-md">
            Delete the original vault after a successful move
          </span>
        </label>
        {#if removeOld}
          <label
            class="flex items-center gap-2.5 mb-3 ml-6 cursor-pointer select-none"
          >
            <input
              bind:checked={confirmDelete}
              disabled={busy}
              type="checkbox"
              class="w-4 h-4 accent-[#f43f5e] cursor-pointer"
            />
            <span class="text-status-warn text-[11px] font-label-sm">
              I understand the original folder will be permanently deleted.
            </span>
          </label>
        {/if}
      {/if}

      <p class="text-text-muted text-[11px] font-label-sm mb-4">
        <span class="material-symbols-outlined text-[14px] align-middle mr-0.5"
          >link_off</span
        >
        Linked notebooks are external folders and are not affected.
      </p>

      <!-- Live region: status / errors -->
      <div aria-live="polite" class="min-h-[20px]">
        {#if busy}
          <div
            class="flex items-center gap-2 text-accent-primary-start text-[12px] font-body-md"
          >
            <span class="material-symbols-outlined text-[16px] animate-spin"
              >progress_activity</span
            >
            <span>{mode === 'move' ? 'Moving vault…' : 'Copying vault…'}</span>
          </div>
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
      <div
        class="flex items-center justify-end gap-2 pt-4 mt-2 border-t border-border-muted"
      >
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
      <!-- Copy success -->
      <div
        role="status"
        class="flex items-start gap-2 p-3 rounded-lg bg-accent-primary-start/10 border border-accent-primary-start/30 text-accent-primary-start text-[12px] font-body-md"
      >
        <span class="material-symbols-outlined text-[18px]">check_circle</span>
        <div class="flex-1">
          <p>Copied {done.files} files ({fmtBytes(done.bytes)}).</p>
          <p class="text-text-muted text-[11px] font-label-sm mt-1 truncate">
            {done.path}
          </p>
          {#if done.skippedSymlinks > 0}
            <p class="text-status-warn text-[11px] font-label-sm mt-1">
              {done.skippedSymlinks} symlink(s) skipped — not included in the copy.
            </p>
          {/if}
        </div>
      </div>
      <div
        class="flex items-center justify-end gap-2 pt-4 mt-4 border-t border-border-muted"
      >
        <button
          onclick={onClose}
          disabled={busy}
          class="px-4 py-2 rounded-lg text-text-muted hover:text-text-primary font-label-sm-bold transition-colors border-none bg-transparent cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
        >
          Done
        </button>
        <button
          onclick={switchToCopy}
          disabled={busy}
          class="px-4 py-2 rounded-lg bg-accent-primary-start/20 border border-accent-primary-start/40 text-accent-primary-start font-label-sm-bold hover:brightness-110 transition-all cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {busy ? 'Switching…' : 'Switch to this vault'}
        </button>
      </div>
    {/if}
  </div>
</div>
