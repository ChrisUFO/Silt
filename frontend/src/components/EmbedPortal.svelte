<script lang="ts">
  import { onMount, onDestroy, getContext, setContext } from 'svelte'
  import {
    ResolveBlockReference,
    MutateBlock
  } from '../../wailsjs/go/main/App.js'
  import { EventsOn } from '../../wailsjs/runtime/runtime.js'
  import RichText from './RichText.svelte'

  // Per-branch chain of embed UUIDs currently being rendered. Each
  // EmbedPortal inherits its ancestor's chain via Svelte context, checks
  // whether its own UUID is already on it, and then publishes a fresh
  // chain with its UUID appended for its own descendants. Siblings of the
  // same block share the parent chain, so a second sibling sees only the
  // chain above the parent — never its own UUID — and renders normally.
  //
  // This replaces a previous global Set which incorrectly flagged any
  // second mount of the same block as recursive even when the two embeds
  // were siblings rather than an ancestor/descendant pair.
  const EMBED_CHAIN_KEY = Symbol('embed-chain')
  type EmbedChain = { has(uuid: string): boolean }
  const parentChain = getContext<EmbedChain | undefined>(EMBED_CHAIN_KEY)

  interface Props {
    uuid: string
    hostNotebook?: string
    hostSection?: string
    hostPage?: string
    hostFileDate?: string
  }

  let {
    uuid,
    hostNotebook = '',
    hostSection = '',
    hostPage = '',
    hostFileDate = ''
  }: Props = $props()

  // Recursion guard: an embed is recursive only when it appears in its
  // own ancestor chain. Sibling embeds of the same block are not.
  let isRecursive = $state(false)

  let ref = $state<any>(null)
  let loading = $state(true)
  let editing = $state(false)
  let editEl = $state<HTMLDivElement | null>(null)
  let saveTimer: any = null
  let offEvent: (() => void) | null = null
  let persistError = $state('')

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

  async function persist(text: string, attempt = 0) {
    try {
      await MutateBlock(uuid, text)
      persistError = ''
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e)
      // The source block is being edited in another view (focus lock held).
      // Retry shortly instead of silently overwriting or dropping the edit.
      if (msg.includes('being edited') && attempt < 5) {
        saveTimer = setTimeout(() => void persist(text, attempt + 1), 800)
      } else {
        // Exhausted retries or a non-transient error — surface it so the
        // user knows their edit didn't save.
        persistError = msg.includes('being edited')
          ? 'Source block is busy — edit not saved yet'
          : `Save failed: ${msg}`
      }
    }
  }

  function handleInput(e: Event) {
    const text = (e.target as HTMLDivElement).innerText.replace(/[\r\n]+/g, ' ')
    if (ref) ref.clean_text = text
    persistError = ''
    if (saveTimer) clearTimeout(saveTimer)
    saveTimer = setTimeout(() => {
      void persist(text)
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
      void persist(text)
    }
  }

  onMount(() => {
    if (parentChain?.has(uuid)) {
      isRecursive = true
      loading = false
      return
    }
    // Publish a fresh chain to descendants with this UUID appended. The
    // chain object is captured by closure, so when this component unmounts
    // the chain it created simply goes out of scope — no explicit cleanup
    // or global mutation needed.
    setContext(EMBED_CHAIN_KEY, {
      has: (id: string) =>
        id === uuid || (parentChain ? parentChain.has(id) : false)
    } satisfies EmbedChain)
    load()
    // Live sync: refresh when the source block changes anywhere.
    offEvent = EventsOn('block:changed', (ev: any) => {
      if (ev && ev.id === uuid && !editing && !saveTimer) {
        load()
      }
    })
  })

  onDestroy(() => {
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
    class="my-1 border border-accent-primary-start/30 bg-accent-primary-glow/40 rounded-lg p-2 pl-3 relative"
  >
    <div
      class="absolute left-0 top-0 bottom-0 w-[2px] bg-accent-primary-start/40 rounded-l"
    ></div>
    <div
      class="flex items-center gap-1 text-[9px] uppercase tracking-widest font-label-sm-bold text-text-muted mb-1"
    >
      <span
        class="material-symbols-outlined text-[10px] text-accent-primary-start"
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
    {#if persistError}
      <div class="text-[10px] text-error mt-1 flex items-center gap-1">
        <span class="material-symbols-outlined text-[11px]">error</span>
        {persistError}
      </div>
    {/if}
  </div>
{/if}
