<script lang="ts">
  import { onMount } from 'svelte'
  import { GetAppVersion } from '../../../wailsjs/go/main/App.js'
  import logo from '../../assets/logo.svg'

  let version = $state('…')

  onMount(async () => {
    try {
      version = await GetAppVersion()
    } catch {
      version = 'unknown'
    }
  })
</script>

<div class="p-6 max-w-2xl">
  <div class="flex items-center gap-4 mb-6">
    <img src={logo} alt="Silt" class="w-14 h-14" />
    <div>
      <h3 class="font-headline-md text-headline-md text-text-primary font-bold">Silt</h3>
      <p class="text-text-muted text-[12px] font-label-sm">Version {version}</p>
    </div>
  </div>

  <p class="text-text-primary text-[13px] font-body-md mb-6">
    A local-first hybrid journal and task manager. Plain-text Markdown on your
    drive, real-time index in memory.
  </p>

  <section>
    <h3 class="font-label-sm-bold text-text-muted uppercase tracking-widest text-[10px] mb-2">
      Links
    </h3>
    <ul class="space-y-1.5 text-[13px] font-body-md">
      <li>
        <span class="text-text-muted">Source:</span>
        <span class="text-accent-primary-start">github.com/ChrisUFO/Silt</span>
      </li>
      <li>
        <span class="text-text-muted">Issues &amp; feedback:</span>
        <span class="text-accent-primary-start">github.com/ChrisUFO/Silt/issues</span>
      </li>
    </ul>
  </section>

  <p class="text-text-muted text-[11px] font-label-sm mt-8">
    Built with Go, Svelte 5, and Wails.
  </p>
</div>
