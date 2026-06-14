<script lang="ts">
  // Appearance tab. The theme engine (Sprint 5–8) is not yet built, so the
  // controls are present but disabled with a clear "coming soon" state. The
  // tab structure is here so the picker mounts without rework once themes land.
</script>

<div class="p-6 max-w-2xl">
  <section>
    <h3 class="font-label-sm-bold text-text-muted uppercase tracking-widest text-[10px] mb-3">
      Theme
    </h3>
    <div class="flex items-center gap-3 bg-bg-surface border border-border-muted rounded-lg px-4 py-3 opacity-60">
      <span class="material-symbols-outlined text-text-muted text-[20px]">palette</span>
      <div class="flex-1">
        <div class="text-text-primary text-[13px] font-body-md">Cyber Forest</div>
        <div class="text-text-muted text-[11px] font-label-sm">Active theme</div>
      </div>
      <span
        class="text-[10px] text-text-muted bg-bg-panel border border-border-muted rounded px-1.5 py-0.5 uppercase tracking-wider"
      >
        Soon
      </span>
    </div>

    <div class="flex items-center justify-between mt-3 opacity-60">
      <span class="text-text-primary text-[13px] font-body-md">Dark mode</span>
      <button
        disabled
        aria-label="Dark mode (disabled)"
        class="text-text-muted border-none bg-transparent cursor-not-allowed p-1.5 rounded"
      >
        <span class="material-symbols-outlined text-[22px]">toggle_off</span>
      </button>
    </div>
  </section>

  <div class="mt-8 flex items-start gap-2 p-3 rounded-lg bg-bg-panel border border-border-muted">
    <span class="material-symbols-outlined text-accent-primary-start/70 text-[18px]">info</span>
    <p class="text-text-muted text-[12px] font-body-md">
      The theme engine (picker, dark/light toggle, custom theme import) lands in
      a dedicated sprint. Silt currently ships the built-in <span class="text-text-primary"
        >Cyber Forest</span
      > palette.
    </p>
  </div>
</div>
