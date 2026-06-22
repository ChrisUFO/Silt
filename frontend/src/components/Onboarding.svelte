<script lang="ts">
  import logo from '../assets/logo.svg'

  interface Props {
    /** True while the startup IPC sequence is in flight (show spinner). */
    loading: boolean
    /** True once startup finished and the vault is known to be initialized. */
    initialized: boolean
    /** Click handler for the "Initialize Workspace" button. */
    onSelectFolder: () => void
  }

  let { loading, initialized, onSelectFolder }: Props = $props()
</script>

{#if loading}
  <div class="onboarding-container">
    <div class="text-text-muted animate-pulse text-lg font-headline-md">
      Initializing Silt Core…
    </div>
  </div>
{:else if !initialized}
  <!-- First run onboarding -->
  <div class="onboarding-container select-none">
    <div class="onboarding-card">
      <img
        src={logo}
        alt="Silt Logo"
        class="onboarding-logo animate-spin-slow"
      />
      <h1 class="onboarding-title font-headline-lg">Silt</h1>
      <p class="onboarding-description font-body-md">
        Capture ideas. Connect them. Get work done. A fast, private workspace
        for your notes and tasks.
      </p>
      <button
        class="onboarding-btn font-label-sm-bold"
        onclick={onSelectFolder}
      >
        Initialize Workspace Folder
      </button>
    </div>
  </div>
{/if}

<style>
  .animate-spin-slow {
    animation: spin 8s linear infinite;
  }
  @keyframes spin {
    from {
      transform: rotate(0deg);
    }
    to {
      transform: rotate(360deg);
    }
  }
</style>
