<script lang="ts">
  import type { RegisteredPlugin } from './sdk'

  interface Props {
    plugin: RegisteredPlugin
  }

  let { plugin }: Props = $props()
</script>

<div class="flex-1 p-8 flex flex-col select-none">
  <div class="flex items-center gap-3 mb-3">
    <span class="material-symbols-outlined text-accent-teal-start text-[28px]"
      >extension</span
    >
    <div>
      <h1 class="font-headline-lg text-headline-lg text-text-primary">
        {plugin.manifest.name}
      </h1>
      <p class="text-text-muted text-[12px] font-body-md">
        third-party plugin · v{plugin.manifest.version}
      </p>
    </div>
  </div>
  <p class="text-text-muted font-body-md max-w-2xl">
    This plugin is loaded and has access to the Silt plugin SDK (<code
      >sqliteQuery</code
    >, <code>mutateBlock</code>,
    <code>updateBlockState</code>). A dedicated UI surface for third-party
    plugins ships in a follow-up; first-party plugins render their own views.
  </p>
</div>
