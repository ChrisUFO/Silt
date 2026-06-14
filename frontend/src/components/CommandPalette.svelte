<script lang="ts">
  import { onMount } from 'svelte'

  interface Command {
    id: string
    name: string
    description: string
    icon: string
    shortcut: string
  }

  interface Props {
    onSelect: (commandId: string) => void
    onClose: () => void
  }

  let { onSelect, onClose }: Props = $props()

  let selectedIdx = $state(0)

  const commands: Command[] = [
    {
      id: 'todo',
      name: 'Task',
      description: 'Create task checkbox',
      icon: 'check_box',
      shortcut: '[]'
    },
    {
      id: 'h1',
      name: 'Heading 1',
      description: 'Large section header',
      icon: 'format_size',
      shortcut: '#'
    },
    {
      id: 'today',
      name: 'Today',
      description: "Insert today's date",
      icon: 'calendar_today',
      shortcut: 'D'
    },
    {
      id: 'embed',
      name: 'Embed Block',
      description: 'Insert a block embed',
      icon: 'link',
      shortcut: 'E'
    }
  ]

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
  style="backdrop-filter: blur(12px) saturate(140%); background: color-mix(in srgb, var(--bg-panel) 85%, transparent);"
>
  <div
    class="px-3 py-1.5 text-[10px] text-text-muted font-label-sm-bold uppercase tracking-widest border-b border-border-muted mb-1 select-none"
  >
    Commands
  </div>
  <div class="flex flex-col">
    {#each commands as cmd, idx}
      <button
        onclick={() => onSelect(cmd.id)}
        class="flex items-center gap-3 px-4 py-2 w-full text-left transition-colors font-body-md border-none focus:outline-none cursor-pointer"
        class:bg-accent-primary-glow={idx === selectedIdx}
        class:text-accent-primary-start={idx === selectedIdx}
        class:text-text-primary={idx !== selectedIdx}
      >
        <span class="material-symbols-outlined text-[18px] select-none"
          >{cmd.icon}</span
        >
        <div class="flex-1 flex flex-col min-w-0">
          <span class="font-label-sm-bold text-label-sm">{cmd.name}</span>
          <span class="text-[10px] text-text-muted truncate"
            >{cmd.description}</span
          >
        </div>
        <span class="text-[10px] text-text-muted select-none"
          >{cmd.shortcut}</span
        >
      </button>
    {/each}
  </div>
</div>
