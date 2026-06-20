<script lang="ts">
  import { onMount } from 'svelte'
  import {
    getSlashCommands,
    type SlashCommand
  } from '../lib/editor/slash-registry'

  interface Props {
    onSelect: (commandId: string) => void
    onClose: () => void
  }

  let { onSelect, onClose }: Props = $props()

  let selectedIdx = $state(0)

  // The command list is the union of built-ins + plugin-registered commands,
  // sourced from the slash-command registry (#110). Plugin commands carry
  // their own onSelect handler; built-ins are dispatched by id.
  const commands: SlashCommand[] = getSlashCommands()

  function handleKeyDown(e: KeyboardEvent) {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      e.stopPropagation()
      selectedIdx = (selectedIdx + 1) % commands.length
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      e.stopPropagation()
      selectedIdx = (selectedIdx - 1 + commands.length) % commands.length
    } else if (e.key === 'Enter') {
      e.preventDefault()
      e.stopPropagation()
      onSelect(commands[selectedIdx].id)
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
  class="absolute top-8 left-6 w-64 glass-palette border border-border-zinc rounded shadow-2xl z-[100] overflow-hidden py-2 scale-100 origin-top-left transition-transform"
  style="backdrop-filter: blur(12px) saturate(140%); background: color-mix(in srgb, var(--color-panel) 85%, transparent);"
>
  {#each commands as cmd, idx}
    {#if cmd.pluginID && (idx === 0 || !commands[idx - 1].pluginID)}
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
</div>
