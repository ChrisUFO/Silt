<script lang="ts">
  import type { TabEntry } from '../lib/tabs'
  import { fade, fly } from 'svelte/transition'

  interface Props {
    tabs: TabEntry[]
    activeTabId: string
    onSelectTab: (id: string) => void
    onCloseTab: (id: string) => void
    onPromoteTab: (id: string) => void
  }

  let { tabs, activeTabId, onSelectTab, onCloseTab, onPromoteTab }: Props =
    $props()

  // Roving tabindex: the active tab (or the first tab if none active) is the
  // only tab in the tab sequence. Arrow keys move focus between tabs without
  // consuming Tab (which the browser uses to leave the tablist).
  let focusedIndex = $state(0)

  // Keep focusedIndex in bounds and synced to the active tab.
  $effect(() => {
    const idx = tabs.findIndex((t) => t.id === activeTabId)
    if (idx !== -1) {
      focusedIndex = idx
    } else if (tabs.length > 0 && focusedIndex >= tabs.length) {
      focusedIndex = 0
    }
  })

  function tabTooltip(tab: TabEntry): string {
    const parts = [tab.notebook]
    if (tab.section) parts.push(tab.section)
    parts.push(tab.page)
    return parts.join(' › ')
  }

  function handleTablistKeydown(e: KeyboardEvent): void {
    if (tabs.length === 0) return
    switch (e.key) {
      case 'ArrowRight':
        e.preventDefault()
        focusedIndex = (focusedIndex + 1) % tabs.length
        focusTab(focusedIndex)
        break
      case 'ArrowLeft':
        e.preventDefault()
        focusedIndex = (focusedIndex - 1 + tabs.length) % tabs.length
        focusTab(focusedIndex)
        break
      case 'Home':
        e.preventDefault()
        focusedIndex = 0
        focusTab(0)
        break
      case 'End':
        e.preventDefault()
        focusedIndex = tabs.length - 1
        focusTab(focusedIndex)
        break
      case 'Enter':
      case ' ': {
        e.preventDefault()
        const tab = tabs[focusedIndex]
        if (tab) onSelectTab(tab.id)
        break
      }
      case 'Delete': {
        e.preventDefault()
        const tab = tabs[focusedIndex]
        if (tab) onCloseTab(tab.id)
        break
      }
    }
  }

  function focusTab(index: number): void {
    const el = tabRefs[index]
    if (el) el.focus()
  }

  // Refs for each tab button, for roving-tabindex focus management.
  let tabRefs: HTMLButtonElement[] = $state([])

  function handleAuxClick(e: MouseEvent, tab: TabEntry): void {
    // Middle-click (button 1) closes the tab — VS Code parity.
    if (e.button === 1) {
      e.preventDefault()
      onCloseTab(tab.id)
    }
  }

  function handleDblClick(tab: TabEntry): void {
    // Double-click promotes a PREVIEW tab only; pinned tabs are no-ops.
    if (tab.preview) onPromoteTab(tab.id)
  }
</script>

{#if tabs.length > 0}
  <div
    class="tab-strip"
    role="tablist"
    aria-label="Open pages"
    aria-orientation="horizontal"
    tabindex="-1"
    onkeydown={handleTablistKeydown}
  >
    {#each tabs as tab, i (tab.id)}
      <button
        in:fly={{ duration: 150, x: -8 }}
        out:fade={{ duration: 100 }}
        bind:this={tabRefs[i]}
        role="tab"
        id="silt-tab-{tab.id}"
        aria-selected={tab.id === activeTabId}
        aria-controls="silt-tabpanel"
        tabindex={i === focusedIndex ? 0 : -1}
        title={tabTooltip(tab)}
        class="tab-button group"
        class:active={tab.id === activeTabId}
        class:preview={tab.preview}
        onclick={() => onSelectTab(tab.id)}
        onfocus={() => (focusedIndex = i)}
        onauxclick={(e) => handleAuxClick(e, tab)}
        ondblclick={() => handleDblClick(tab)}
      >
        <span class="tab-label" class:italic={tab.preview}>{tab.page}</span>
        <!-- svelte-ignore a11y_click_events_have_key_events -->
        <!-- svelte-ignore a11y_no_static_element_interactions -->
        <!-- Close is keyboard-accessible via the parent tab's Delete and
             Ctrl+W handlers; this span is a mouse-only convenience and
             MUST NOT have role="button" (that would nest interactive
             elements inside the <button role="tab"> — HTML spec violation). -->
        <span
          aria-label="Close tab"
          title="Close tab"
          class="tab-close"
          class:preview-close={tab.preview}
          onclick={(e) => {
            e.stopPropagation()
            onCloseTab(tab.id)
          }}
        >
          <span class="material-symbols-outlined text-[14px]" aria-hidden="true"
            >close</span
          >
        </span>
      </button>
    {/each}
  </div>
{/if}

<style>
  .tab-strip {
    display: flex;
    align-items: stretch;
    height: 36px;
    min-height: 36px;
    background: var(--color-panel, #14161b);
    border-bottom: 1px solid var(--color-border-muted, #2a2d35);
    overflow-x: auto;
    overflow-y: hidden;
    scrollbar-width: thin;
    padding: 0;
    gap: 0;
  }

  .tab-strip::-webkit-scrollbar {
    height: 2px;
  }

  .tab-strip::-webkit-scrollbar-thumb {
    background: var(--color-border-muted, #2a2d35);
  }

  .tab-button {
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 0 8px 0 12px;
    min-width: 100px;
    max-width: 200px;
    height: 100%;
    border: none;
    border-right: 1px solid var(--color-border-muted, #2a2d35);
    background: transparent;
    color: var(--color-text-muted, #8b95a3);
    font-family: var(--font-body, inherit);
    font-size: 12px;
    cursor: pointer;
    transition:
      background-color 120ms ease,
      color 120ms ease;
    white-space: nowrap;
    position: relative;
  }

  .tab-button:hover {
    background: var(--color-hover, #1e2128);
    color: var(--color-text-primary, #e6e6e6);
  }

  .tab-button:focus-visible {
    outline: 2px solid var(--color-accent-primary-start, #2dd4bf);
    outline-offset: -2px;
  }

  .tab-button.active {
    color: var(--color-accent-primary-start, #2dd4bf);
    background: var(--color-void, #0c0c0e);
  }

  /* Active-tab indicator: a thin accent bar at the bottom of the tab. */
  .tab-button.active::after {
    content: '';
    position: absolute;
    bottom: 0;
    left: 0;
    right: 0;
    height: 2px;
    background: var(--color-accent-primary-start, #2dd4bf);
  }

  .tab-label {
    overflow: hidden;
    text-overflow: ellipsis;
    flex: 1;
  }

  .tab-button.preview .tab-label {
    font-style: italic;
  }

  .tab-close {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    height: 18px;
    border-radius: 4px;
    color: inherit;
    opacity: 0.5;
    cursor: pointer;
    transition:
      opacity 120ms ease,
      background-color 120ms ease;
    flex-shrink: 0;
  }

  .tab-close:hover {
    opacity: 1;
    background: var(--color-hover, #1e2128);
  }

  /* Preview tabs: hide the close button until hover (VS Code parity). */
  .preview-close {
    opacity: 0;
  }

  .tab-button:hover .preview-close {
    opacity: 0.5;
  }

  .tab-button:hover .preview-close:hover {
    opacity: 1;
  }
</style>
