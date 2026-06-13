<script lang="ts">
  import { onMount, onDestroy } from 'svelte'
  import {
    ResolveBlockReference,
    PluginMutateBlock
  } from '../../wailsjs/go/main/App.js'
  import { EventsOn } from '../../wailsjs/runtime/runtime.js'
  import RichText from './RichText.svelte'
  import { embedRenderStack } from '../lib/embedGuard'

  interface Props {
    uuid: string
    hostNotebook: string
    hostSection: string
    hostPage: string
    hostFileDate: string
  }

  let { uuid, hostNotebook, hostSection, hostPage, hostFileDate }: Props =
    $props()

  // Recursion guard: track the chain of embeds currently being rendered.
  // If this uuid is already an ancestor, render a placeholder instead of
  // descending into an infinite embed loop (A embeds B embeds A).
  let isRecursive = $state(false)

  let ref = $state<any>(null)
  let loading = $state(true)
  let editing = $state(false)
  let editEl = $state<HTMLDivElement | null>(null)
  let saveTimer: any = null
  let offEvent: (() => void) | null = null

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

  function handleInput(e: Event) {
    const text = (e.target as HTMLDivElement).innerText.replace(/[\r\n]+/g, ' ')
    if (ref) ref.clean_text = text
    if (saveTimer) clearTimeout(saveTimer)
    saveTimer = setTimeout(() => {
      void PluginMutateBlock(uuid, text)
    }, 500)
  }

  function focusIn() {
    editing = true
  }

  function focusOut() {
    editing = false
    if (saveTimer) {
      clearTimeout(saveTimer)
      saveTimer = null
      const text = editEl?.innerText.replace(/[\r\n]+/g, ' ') ?? ''
      void PluginMutateBlock(uuid, text)
    }
  }

  onMount(() => {
    if (embedRenderStack.has(uuid)) {
      isRecursive = true
      loading = false
      return
    }
    embedRenderStack.add(uuid)
    load()
    // Live sync: refresh when the source block changes anywhere.
    offEvent = EventsOn('block:changed', (ev: any) => {
      if (ev && ev.id === uuid && !editing) {
        load()
      }
    })
  })

  onDestroy(() => {
    embedRenderStack.delete(uuid)
    if (offEvent) offEvent()
    if (saveTimer) clearTimeout(saveTimer)
  })
</script>

{#if isRecursive}
  <span
    class="inline-flex items-center gap-1 mx-0.5 text-[0.8em] text-text-muted italic border border-dashed border-border-muted rounded px-1.5 py-0.5"
    title="Recursive embed — stopped to avoid a loop"
  >
    <span class="material-symbols-outlined text-[0.9em]">block</span>recursive
    embed
  </span>
{:else if loading}
  <span class="text-text-muted italic text-[0.85em] mx-0.5">loading embed…</span
  >
{:else if !ref?.exists}
  <span
    class="inline-flex items-center gap-1 mx-0.5 text-[0.85em] text-text-muted italic"
    title="Embedded block not found"
  >
    <span class="material-symbols-outlined text-[0.9em]">hide_source</span
    >{`{{embed:${uuid.slice(0, 8)}…}}`}
  </span>
{:else}
  <div
    class="my-1 border border-accent-teal-start/30 bg-accent-teal-glow/40 rounded-lg p-2 pl-3 relative"
  >
    <div
      class="absolute left-0 top-0 bottom-0 w-[2px] bg-accent-teal-start/40 rounded-l"
    ></div>
    <div
      class="flex items-center gap-1 text-[9px] uppercase tracking-widest font-label-sm-bold text-text-muted mb-1"
    >
      <span class="material-symbols-outlined text-[10px] text-accent-teal-start"
        >clone</span
      >
      embed · {ref.notebook} › {ref.section} › {ref.page}
    </div>
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div
      bind:this={editEl}
      contenteditable="true"
      role="textbox"
      tabindex="0"
      oninput={handleInput}
      onfocus={focusIn}
      onblur={focusOut}
      class="text-text-primary text-sm leading-relaxed focus:outline-none min-h-[20px] whitespace-pre-wrap break-words"
    >
      {#if editing}
        {ref.clean_text}
      {:else}
        <RichText
          text={ref.clean_text || ''}
          notebook={ref.notebook}
          section={ref.section}
          page={ref.page}
          fileDate={ref.file_date}
        />
      {/if}
    </div>
  </div>
{/if}
