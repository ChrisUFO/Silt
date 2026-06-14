<script lang="ts">
  import { onMount } from 'svelte'
  import { fade } from 'svelte/transition'
  import { ResolveBlockReference } from '../../wailsjs/go/main/App.js'

  interface Props {
    uuid: string
  }

  let { uuid }: Props = $props()

  let ref = $state<any>(null)
  let loading = $state(true)
  let showHover = $state(false)
  let hoverTimer: any = null

  async function load() {
    loading = true
    try {
      ref = await ResolveBlockReference(uuid)
    } catch (e) {
      ref = { exists: false }
    } finally {
      loading = false
    }
  }

  onMount(() => {
    load()
  })

  function enter() {
    if (hoverTimer) clearTimeout(hoverTimer)
    hoverTimer = setTimeout(() => (showHover = true), 250)
  }

  function leave() {
    if (hoverTimer) clearTimeout(hoverTimer)
    hoverTimer = setTimeout(() => (showHover = false), 150)
  }

  function click() {
    if (ref && ref.exists) {
      window.dispatchEvent(
        new CustomEvent('navigate-to-block', {
          detail: {
            notebook: ref.notebook,
            section: ref.section,
            page: ref.page,
            date: ref.file_date,
            blockId: ref.id
          }
        })
      )
    }
  }
</script>

{#if loading}
  <span class="text-text-muted italic text-[0.85em] mx-0.5">((…))</span>
{:else if !ref?.exists}
  <span
    class="inline-flex items-center align-baseline text-text-muted line-through mx-0.5 text-[0.85em]"
    title="Unresolved block reference"
  >
    (({uuid.slice(0, 8)}…))
  </span>
{:else}
  <div class="inline-block relative">
    <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
    <span
      role="link"
      tabindex="0"
      onclick={click}
      onkeydown={(e) => e.key === 'Enter' && click()}
      onmouseenter={enter}
      onmouseleave={leave}
      class="inline-flex items-center align-baseline gap-1 text-accent-teal-start hover:text-accent-teal-end underline decoration-dotted underline-offset-4 cursor-pointer mx-0.5"
      title={ref.notebook + ' › ' + ref.section + ' › ' + ref.page}
    >
      <span class="material-symbols-outlined text-[0.9em]">link</span>
      <span class="truncate max-w-[18ch]"
        >{ref.clean_text || '(empty block)'}</span
      >
    </span>

    {#if showHover}
      <div
        transition:fade={{ duration: 120 }}
        class="absolute z-50 top-full left-0 mt-1 w-80 max-w-[80vw] glass-palette border border-border-zinc rounded-lg shadow-2xl p-3 text-left"
        style="backdrop-filter: blur(16px) saturate(140%); background: rgba(22, 22, 25, 0.94);"
      >
        <div
          class="flex items-center gap-1 text-[10px] text-text-muted uppercase tracking-widest font-label-sm-bold mb-2"
        >
          <span>{ref.notebook}</span>
          <span class="material-symbols-outlined text-[10px]"
            >chevron_right</span
          >
          <span>{ref.section}</span>
          <span class="material-symbols-outlined text-[10px]"
            >chevron_right</span
          >
          <span class="text-accent-teal-start">{ref.page}</span>
        </div>
        <div
          class="font-body-md text-sm text-text-primary line-clamp-6 whitespace-pre-wrap"
        >
          {ref.clean_text || '(empty block)'}
        </div>
      </div>
    {/if}
  </div>
{/if}
