<script lang="ts">
  import { onMount } from 'svelte'
  import {
    getSlashCommands,
    type SlashCommand
  } from '../lib/editor/slash-registry'

  interface Props {
    onSelect: (commandId: string) => void
    onClose: () => void
    query?: string
    style?: string
  }

  let { onSelect, onClose, query = '', style = '' }: Props = $props()

  let selectedIdx = $state(0)
  let containerEl = $state<HTMLDivElement | null>(null)

  // The command list is the union of built-ins + plugin-registered commands,
  // sourced from the slash-command registry (#110).
  const allCommands = getSlashCommands()

  // Filter and rank commands by the query prop reactively
  let filteredCommands = $derived.by(() => {
    const q = query.toLowerCase().trim()
    if (!q) return allCommands

    const scored = allCommands
      .map((cmd, index) => {
        const label = cmd.label.toLowerCase()
        const id = cmd.id.toLowerCase()
        const desc = cmd.description ? cmd.description.toLowerCase() : ''

        let score = 0
        if (label.startsWith(q) || id.startsWith(q)) {
          score = 10
        } else if (label.includes(q) || id.includes(q)) {
          score = 5
        } else if (desc.includes(q)) {
          score = 1
        }

        return { cmd, score, index }
      })
      .filter((item) => item.score > 0)

    // Sort by score descending, then preserve original order
    scored.sort((a, b) => {
      if (b.score !== a.score) {
        return b.score - a.score
      }
      return a.index - b.index
    })

    return scored.map((item) => item.cmd)
  })

  // Reset selection index when query changes
  $effect(() => {
    const _ = query
    selectedIdx = 0
  })

  // Scroll active item into view
  $effect(() => {
    if (containerEl && selectedIdx !== -1) {
      const activeEl = containerEl.querySelector(
        '[data-active-cmd="true"]'
      ) as HTMLElement | null
      if (activeEl && typeof activeEl.scrollIntoView === 'function') {
        activeEl.scrollIntoView({ block: 'nearest' })
      }
    }
  })

  function handleKeyDown(e: KeyboardEvent) {
    if (filteredCommands.length === 0) {
      if (e.key === 'Escape') {
        e.preventDefault()
        e.stopPropagation()
        onClose()
      }
      return
    }

    if (e.key === 'ArrowDown') {
      e.preventDefault()
      e.stopPropagation()
      selectedIdx = (selectedIdx + 1) % filteredCommands.length
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      e.stopPropagation()
      selectedIdx =
        (selectedIdx - 1 + filteredCommands.length) % filteredCommands.length
    } else if (e.key === 'Enter') {
      e.preventDefault()
      e.stopPropagation()
      if (filteredCommands[selectedIdx]) {
        onSelect(filteredCommands[selectedIdx].id)
      }
    } else if (e.key === 'Escape') {
      e.preventDefault()
      e.stopPropagation()
      onClose()
    }
  }

  onMount(() => {
    window.addEventListener('keydown', handleKeyDown, true)
    return () => {
      window.removeEventListener('keydown', handleKeyDown, true)
    }
  })
</script>

<!-- Command Palette Container (Frosted glass) -->
<div
  bind:this={containerEl}
  class="w-64 glass-palette border border-border-zinc rounded shadow-2xl z-[100] overflow-hidden py-2 scale-100 origin-top-left transition-transform custom-scrollbar"
  style="backdrop-filter: blur(12px) saturate(140%); background: color-mix(in srgb, var(--color-panel) 85%, transparent); max-height: 280px; overflow-y: auto; {style}"
>
  {#if filteredCommands.length === 0}
    <div class="px-4 py-3 text-xs text-text-muted text-center select-none">
      No matching commands
    </div>
  {:else}
    {#each filteredCommands as cmd, idx}
      {#if cmd.pluginID && (idx === 0 || !filteredCommands[idx - 1].pluginID)}
        <div
          class="px-3 py-1.5 text-[10px] text-text-muted font-label-sm-bold uppercase tracking-widest border-t border-border-muted mt-1 pt-2 select-none"
        >
          Plugins
        </div>
      {:else if !cmd.pluginID && idx === 0}
        <div
          class="px-3 py-1.5 text-[10px] text-text-muted font-label-sm-bold uppercase tracking-widest border-b border-border-muted mb-1 select-none"
        >
          Commands
        </div>
      {/if}
      <button
        onclick={() => onSelect(cmd.id)}
        class="flex items-center gap-3 px-4 py-2 w-full text-left transition-colors font-body-md border-none focus:outline-none cursor-pointer"
        class:bg-accent-primary-glow={idx === selectedIdx}
        class:text-accent-primary-start={idx === selectedIdx}
        class:text-text-primary={idx !== selectedIdx}
        data-active-cmd={idx === selectedIdx}
        onmouseenter={() => (selectedIdx = idx)}
      >
        <span class="material-symbols-outlined text-[18px] select-none"
          >{cmd.icon ?? 'extension'}</span
        >
        <div class="flex-1 flex flex-col min-w-0">
          <span class="font-label-sm-bold text-label-sm">{cmd.label}</span>
          {#if cmd.description}
            <span class="text-[10px] text-text-muted truncate"
              >{cmd.description}</span
            >
          {/if}
        </div>
        {#if cmd.shortcut}
          <span class="text-[10px] text-text-muted select-none"
            >{cmd.shortcut}</span
          >
        {:else if cmd.pluginID}
          <span class="text-[9px] text-text-muted select-none uppercase"
            >{cmd.pluginID}</span
          >
        {/if}
      </button>
    {/each}
  {/if}
</div>
