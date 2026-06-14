<script lang="ts">
  import { onMount, untrack } from 'svelte'
  import {
    QueryTagHierarchy,
    QueryBlocksByTag
  } from '../../wailsjs/go/main/App.js'
  import TagTreeNode from './TagTreeNode.svelte'

  interface TagNode {
    name: string
    path: string
    count: number
    children: TagNode[]
  }

  interface Props {
    selectedTag?: string
  }

  let { selectedTag = '' }: Props = $props()

  let tree = $state<TagNode[]>([])
  let expanded = $state<Set<string>>(new Set())
  let activeTag = $state('')
  let results = $state<any[]>([])
  let loadingResults = $state(false)
  let query = $state('')

  let filteredTree = $derived.by(() => {
    if (!query.trim()) return tree
    const q = query.toLowerCase()
    const filter = (nodes: TagNode[]): TagNode[] => {
      const out: TagNode[] = []
      for (const n of nodes) {
        const kids = filter(n.children)
        if (
          n.name.toLowerCase().includes(q) ||
          n.path.toLowerCase().includes(q) ||
          kids.length > 0
        ) {
          out.push({ ...n, children: kids })
        }
      }
      return out
    }
    return filter(tree)
  })

  async function loadTree() {
    try {
      tree = (await QueryTagHierarchy()) || []
    } catch (e) {
      console.error('QueryTagHierarchy failed:', e)
      tree = []
    }
  }

  function toggle(path: string) {
    const next = new Set(expanded)
    if (next.has(path)) next.delete(path)
    else next.add(path)
    expanded = next
  }

  async function selectTag(path: string) {
    activeTag = path
    loadingResults = true
    try {
      results = await QueryBlocksByTag(path)
    } catch (e) {
      console.error('QueryBlocksByTag failed:', e)
      results = []
    } finally {
      loadingResults = false
    }
  }

  function openBlock(res: any) {
    window.dispatchEvent(
      new CustomEvent('navigate-to-block', {
        detail: {
          notebook: res.notebook,
          section: res.section,
          page: res.page,
          date: res.file_date,
          blockId: res.id
        }
      })
    )
  }

  onMount(() => {
    loadTree()
    const refresh = () => loadTree()
    window.addEventListener('refresh-navigation', refresh)
    return () => window.removeEventListener('refresh-navigation', refresh)
  })

  // When an inline tag-pill sends navigate-to-tag, select that tag and expand
  // its ancestor chain so it's visible in the tree. Only selectedTag drives
  // this effect; expanded is read via untrack to avoid a re-trigger loop.
  $effect(() => {
    const tag = selectedTag
    if (!tag) return
    const parts = tag.split('/')
    const acc: string[] = []
    const next = new Set(untrack(() => expanded))
    for (const part of parts) {
      acc.push(part)
      next.add(acc.join('/'))
    }
    expanded = next
    void selectTag(tag)
  })
</script>

<div class="flex-1 flex min-h-0">
  <!-- Tag tree -->
  <div class="w-72 border-r border-border-muted flex flex-col min-h-0">
    <div class="p-3 border-b border-border-muted">
      <div class="flex items-center gap-2 mb-2">
        <span class="material-symbols-outlined text-accent-teal-start"
          >label</span
        >
        <h1 class="font-headline-md text-headline-md text-text-primary">
          Tags
        </h1>
      </div>
      <input
        bind:value={query}
        type="text"
        placeholder="Filter tags…"
        class="w-full bg-bg-surface border border-border-zinc rounded-lg px-3 py-1.5 text-text-primary text-[13px] font-body-md outline-none focus:border-accent-teal-start transition-colors"
      />
    </div>
    <div class="flex-1 overflow-y-auto custom-scrollbar p-2">
      {#if filteredTree.length === 0}
        <div class="text-text-muted text-center py-10 font-body-md text-[13px]">
          {#if tree.length === 0}
            No tags yet. Add <span class="text-accent-indigo-start"
              >#tag/path</span
            > to a block.
          {:else}
            No tags match "{query}".
          {/if}
        </div>
      {:else}
        {#each filteredTree as node (node.path)}
          <TagTreeNode
            {node}
            depth={0}
            {expanded}
            {activeTag}
            onToggle={toggle}
            onSelect={selectTag}
          />
        {/each}
      {/if}
    </div>
  </div>

  <!-- Results -->
  <div class="flex-1 flex flex-col min-h-0">
    <div class="px-6 py-3 border-b border-border-muted flex items-center gap-2">
      {#if activeTag}
        <span class="material-symbols-outlined text-accent-indigo-start"
          >label</span
        >
        <span class="text-accent-indigo-start font-label-sm-bold"
          >#{activeTag}</span
        >
        <span class="text-text-muted text-[12px]"
          >· {results.length} block{results.length === 1 ? '' : 's'}</span
        >
      {:else}
        <span class="text-text-muted font-body-md"
          >Select a tag to see its blocks.</span
        >
      {/if}
    </div>
    <div class="flex-1 overflow-y-auto custom-scrollbar">
      {#if !activeTag}
        <div class="text-text-muted text-center py-16 font-body-md">
          Pick a tag on the left.
        </div>
      {:else if loadingResults}
        <div class="text-text-muted text-center py-10 animate-pulse">
          Loading…
        </div>
      {:else if results.length === 0}
        <div class="text-text-muted text-center py-10 font-body-md">
          No blocks tagged.
        </div>
      {:else}
        {#each results as res (res.id)}
          <button
            onclick={() => openBlock(res)}
            class="w-full text-left px-6 py-3 border-b border-border-muted/50 hover:bg-bg-hover transition-colors border-none bg-transparent cursor-pointer flex flex-col gap-1"
          >
            <div
              class="flex items-center gap-1.5 text-[10px] text-text-muted uppercase tracking-widest font-label-sm-bold"
            >
              <span>{res.notebook}</span>
              <span class="material-symbols-outlined text-[10px]"
                >chevron_right</span
              >
              <span>{res.section}</span>
              <span class="material-symbols-outlined text-[10px]"
                >chevron_right</span
              >
              <span>{res.page}</span>
            </div>
            <div class="font-body-md text-sm text-text-primary">
              {res.clean_content}
            </div>
          </button>
        {/each}
      {/if}
    </div>
  </div>
</div>
