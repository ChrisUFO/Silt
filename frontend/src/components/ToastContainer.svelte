<script lang="ts">
  import { notificationsState, dismissNotification } from '../notifications/store.svelte'
  import { onMount } from 'svelte'

  // Toast container (#86). Renders the global notification stack in the
  // bottom-right corner. Each toast is a `role="status"` (info/success) or
  // `role="alert"` (error) with `aria-live="polite"`/`"assertive"`. Frosted-
  // glass styling + token-bound colors. Auto-dismiss is driven by the store
  // (the store schedules the dismiss; this component is purely presentational).
  let list: HTMLDivElement | null = $state(null)

  onMount(() => {
    return () => {
      list = null
    }
  })

  function kindClasses(kind: string): string {
    if (kind === 'error') return 'border-status-danger/40 bg-status-danger/10 text-status-danger'
    if (kind === 'success') return 'border-status-success/40 bg-status-success/10 text-status-success'
    return 'border-border-zinc bg-bg-surface text-text-primary'
  }
</script>

<div
  bind:this={list}
  class="pointer-events-none fixed bottom-4 right-4 z-[200] flex w-[min(360px,calc(100vw-2rem))] flex-col gap-2"
  aria-label="Notifications"
>
  {#each notificationsState.items as n (n.id)}
    <div
      class={kindClasses(n.kind) +
        ' pointer-events-auto flex items-start gap-2 rounded-lg border px-3 py-2 shadow-lg backdrop-blur'}
      role={n.kind === 'error' ? 'alert' : 'status'}
      aria-live={n.kind === 'error' ? 'assertive' : 'polite'}
    >
      <span class="material-symbols-outlined mt-0.5 text-[18px]" aria-hidden="true">
        {n.kind === 'error' ? 'error' : n.kind === 'success' ? 'check_circle' : 'info'}
      </span>
      <div class="min-w-0 flex-1 text-sm">
        <p class="break-words">{n.message}</p>
        {#if n.action}
          <button
            type="button"
            onclick={() => {
              void n.action?.run()
              dismissNotification(n.id)
            }}
            class="mt-1 inline-block rounded border border-current/30 px-2 py-0.5 text-xs font-medium hover:bg-current/10 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-primary-start"
          >
            {n.action.label}
          </button>
        {/if}
      </div>
      <button
        type="button"
        onclick={() => dismissNotification(n.id)}
        aria-label="Dismiss notification"
        class="shrink-0 rounded p-0.5 opacity-70 transition-opacity hover:opacity-100 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-primary-start"
      >
        <span class="material-symbols-outlined text-[16px]" aria-hidden="true">close</span>
      </button>
    </div>
  {/each}
</div>
