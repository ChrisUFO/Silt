<script lang="ts">
  // PluginMountFallback is the shared error-boundary fallback for first-party
  // plugin component mounts (#357). It renders an inline, operable error card
  // so a plugin component that throws during render cannot trap the user
  // (e.g. inside the focus-trapped Settings dialog). The "Retry" button calls
  // the boundary's reset() to re-mount the component.
  //
  // Reused by every plugin-component mount surface (bespoke settings page,
  // future sidebar/banner mounts) so the fallback UX is consistent and the
  // svelte:boundary pattern has one home.
  interface Props {
    name: string
    error: unknown
    reset: () => void
  }
  let { name, error, reset }: Props = $props()
  const message = $derived(
    error instanceof Error ? error.message : String(error ?? 'Unknown error')
  )
</script>

<div
  role="alert"
  aria-live="assertive"
  class="m-6 p-4 rounded-lg border border-error/40 bg-error/10 text-status-danger font-body-md text-[13px] flex flex-col gap-3"
>
  <div class="flex items-start gap-2">
    <span
      class="material-symbols-outlined text-[18px] mt-0.5"
      aria-hidden="true">error</span
    >
    <div class="flex-1">
      <p class="font-medium">{name} settings failed to load</p>
      <p class="text-status-danger/80 break-words mt-1">{message}</p>
    </div>
  </div>
  <button
    type="button"
    onclick={reset}
    class="self-start px-3 py-1.5 rounded-md bg-error/15 hover:bg-error/25 border border-error/40 transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-primary-start/60"
  >
    Retry
  </button>
</div>
