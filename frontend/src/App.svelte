<script lang="ts">
  import { onMount } from 'svelte'
  import {
    IsVaultInitialized,
    InitializeVault
  } from '../wailsjs/go/main/App.js'

  let isInitialized = $state(false)
  let loading = $state(true)

  onMount(async () => {
    try {
      isInitialized = await IsVaultInitialized()
    } catch (e) {
      console.error('Startup check failed:', e)
    } finally {
      loading = false
    }
  })

  async function handleSelectFolder() {
    try {
      const success = await InitializeVault()
      if (success) {
        isInitialized = true
      }
    } catch (e) {
      alert('Failed to initialize vault: ' + e)
    }
  }
</script>

<main class="w-full h-full flex flex-col">
  {#if loading}
    <div class="onboarding-container">
      <div class="text-zinc-500 animate-pulse text-lg">
        Initializing Notes# Core...
      </div>
    </div>
  {:else if !isInitialized}
    <div class="onboarding-container">
      <div class="onboarding-card">
        <img
          src="./assets/logo.svg"
          alt="notes# Logo"
          class="onboarding-logo animate-spin-slow"
        />
        <h1 class="onboarding-title">notes#</h1>
        <p class="onboarding-description">
          A local-first hybrid journal and task manager. Plain-text Markdown on
          your drive, real-time index in memory.
        </p>
        <button class="onboarding-btn" onclick={handleSelectFolder}>
          Initialize Workspace Folder
        </button>
      </div>
    </div>
  {:else}
    <div class="onboarding-container">
      <div class="onboarding-card border-active">
        <img
          src="./assets/logo.svg"
          alt="notes# Logo"
          class="onboarding-logo"
        />
        <h1 class="onboarding-title">notes# Vault Ready</h1>
        <div
          class="text-xs bg-[#161619] border border-[#27272a] px-3 py-1 rounded text-sky-400 font-mono"
        >
          Sprint 1 Foundation Active
        </div>
        <p class="onboarding-description">
          Workspace has been successfully initialized and indexed. Ready for
          Sprint 2 UI integrations.
        </p>
      </div>
    </div>
  {/if}
</main>

<style>
  .border-active {
    border-color: var(--border-active) !important;
    box-shadow:
      0 20px 40px rgba(0, 0, 0, 0.6),
      0 0 10px rgba(16, 185, 129, 0.1) !important;
  }
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
