<script lang="ts">
  import BlockReferenceChip from './BlockReferenceChip.svelte'
  import EmbedPortal from './EmbedPortal.svelte'

  interface Props {
    text: string
    notebook: string
    section: string
    page: string
    fileDate: string
  }

  let { text, notebook, section, page, fileDate }: Props = $props()

  // One combined scanner for refs ((uuid)), embeds {{embed:uuid}}, and tags.
  const TOKEN =
    /(\(\([0-9a-f-]{36}\)\))|(\{\{embed:[0-9a-f-]{36}\}\})|(\B#[a-zA-Z][a-zA-Z0-9_/-]*)/g

  interface Segment {
    type: 'text' | 'tag' | 'ref' | 'embed'
    value: string
    uuid?: string
  }

  let segments = $derived.by(() => {
    const segs: Segment[] = []
    let last = 0
    let m: RegExpExecArray | null
    TOKEN.lastIndex = 0
    while ((m = TOKEN.exec(text)) !== null) {
      if (m.index > last) {
        segs.push({ type: 'text', value: text.slice(last, m.index) })
      }
      if (m[1]) {
        segs.push({ type: 'ref', value: m[1], uuid: m[1].slice(2, -2) })
      } else if (m[2]) {
        segs.push({
          type: 'embed',
          value: m[2],
          uuid: m[2].replace(/^{{embed:/, '').replace(/}}$/, '')
        })
      } else if (m[3]) {
        // Strip the leading '#' and any trailing '/'/'-' (mirrors Go ExtractTags).
        segs.push({
          type: 'tag',
          value: m[3].replace(/^#/, '').replace(/[/-]+$/, '')
        })
      }
      last = m.index + m[0].length
    }
    if (last < text.length) {
      segs.push({ type: 'text', value: text.slice(last) })
    }
    return segs
  })

  function clickTag(tagPath: string) {
    window.dispatchEvent(
      new CustomEvent('navigate-to-tag', { detail: tagPath })
    )
  }
</script>

{#each segments as seg, i (i)}
  {#if seg.type === 'text'}
    <span>{seg.value}</span>
  {:else if seg.type === 'tag'}
    <button
      onclick={() => clickTag(seg.value)}
      class="inline-flex items-center align-baseline bg-accent-indigo-glow border border-accent-indigo-start/30 text-accent-indigo-start rounded px-1.5 py-0 mx-0.5 text-[0.85em] font-label-sm hover:brightness-110 transition-all cursor-pointer border"
      title={'#' + seg.value}
    >
      <span class="material-symbols-outlined text-[0.9em] mr-0.5">label</span
      >{seg.value}
    </button>
  {:else if seg.type === 'ref' && seg.uuid}
    <BlockReferenceChip uuid={seg.uuid} />
  {:else if seg.type === 'embed' && seg.uuid}
    <EmbedPortal
      uuid={seg.uuid}
      hostNotebook={notebook}
      hostSection={section}
      hostPage={page}
      hostFileDate={fileDate}
    />
  {/if}
{/each}
