<script lang="ts">
  import type { TabEntry } from '../lib/tabs'
  import { fade, fly } from 'svelte/transition'

  interface Props {
    tabs: TabEntry[]
    activeTabId: string
    onSelectTab: (id: string) => void
    onCloseTab: (id: string) => void
    onPromoteTab: (id: string) => void
    onReorderTab: (fromId: string, toId: string, before: boolean) => void
    /** When true (default), show per-tab dirty/save-failed glyphs (#167). */
    showDirtyIndicators?: boolean
  }

  let {
    tabs,
    activeTabId,
    onSelectTab,
    onCloseTab,
    onPromoteTab,
    onReorderTab,
    showDirtyIndicators = true
  }: Props = $props()

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
    let tip = parts.join(' › ')
    if (tab.saveError) tip += ' — save failed'
    else if (tab.dirty) tip += ' — unsaved edits'
    return tip
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
    // Middle-click (button 1) closes the tab — industry-standard parity.
    if (e.button === 1) {
      e.preventDefault()
      onCloseTab(tab.id)
    }
  }

  function handleDblClick(tab: TabEntry): void {
    // Double-click promotes a PREVIEW tab only; pinned tabs are no-ops.
    if (tab.preview) onPromoteTab(tab.id)
  }

  // --- Tab drag-to-reorder (#175) ---
  // dragTabId: the id of the tab being dragged. dropTabTarget: the tab
  // currently under the cursor + whether the drop indicator should show on
  // its left (before) or right (after) edge.
  let dragTabId = $state<string | null>(null)
  let dropTabTarget = $state<{ id: string; before: boolean } | null>(null)

  function handleTabDragStart(e: DragEvent, tab: TabEntry): void {
    // Don't start a drag if the user grabbed the close button — the close
    // span is a mouse-only convenience; dragging from it would be confusing.
    const target = e.target as HTMLElement
    if (target.closest('.tab-close')) {
      e.preventDefault()
      return
    }
    dragTabId = tab.id
    if (e.dataTransfer) {
      e.dataTransfer.effectAllowed = 'move'
      e.dataTransfer.setData('text/plain', tab.id)
    }
  }

  function handleTabDragOver(e: DragEvent, tab: TabEntry): void {
    if (!dragTabId || dragTabId === tab.id) return
    e.preventDefault()
    if (e.dataTransfer) e.dataTransfer.dropEffect = 'move'
    const rect = (e.currentTarget as HTMLElement).getBoundingClientRect()
    const before = e.clientX < rect.left + rect.width / 2
    dropTabTarget = { id: tab.id, before }
  }

  function handleTabDragLeave(e: DragEvent): void {
    const tabEl = e.currentTarget as HTMLElement
    if (e.relatedTarget && tabEl.contains(e.relatedTarget as Node)) return
    dropTabTarget = null
  }

  function handleTabDrop(e: DragEvent, tab: TabEntry): void {
    e.preventDefault()
    e.stopPropagation()
    if (dragTabId && dragTabId !== tab.id && dropTabTarget) {
      onReorderTab(dragTabId, tab.id, dropTabTarget.before)
    }
    dragTabId = null
    dropTabTarget = null
  }

  function handleTabDragEnd(): void {
    dragTabId = null
    dropTabTarget = null
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
        aria-label={tabTooltip(tab)}
        tabindex={i === focusedIndex ? 0 : -1}
        title={tabTooltip(tab)}
        class="tab-button group"
        class:active={tab.id === activeTabId}
        class:preview={tab.preview}
        class:tab-drop-before={dropTabTarget?.id === tab.id &&
          dropTabTarget.before}
        class:tab-drop-after={dropTabTarget?.id === tab.id &&
          !dropTabTarget.before}
        draggable="true"
        ondragstart={(e) => handleTabDragStart(e, tab)}
        ondragover={(e) => handleTabDragOver(e, tab)}
        ondragleave={handleTabDragLeave}
        ondrop={(e) => handleTabDrop(e, tab)}
        ondragend={handleTabDragEnd}
        onclick={() => onSelectTab(tab.id)}
        onfocus={() => (focusedIndex = i)}
        onauxclick={(e) => handleAuxClick(e, tab)}
        ondblclick={() => handleDblClick(tab)}
      >
        {#if showDirtyIndicators && (tab.dirty || tab.saveError)}
          <span
            class="tab-save-state"
            class:error={!!tab.saveError}
            class:dirty={!tab.saveError}
            aria-hidden="true"
          >
            <span class="material-symbols-outlined text-[12px]">
              {tab.saveError ? 'error' : 'circle'}
            </span>
          </span>
        {/if}
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

  /* Preview tabs: hide the close button until hover (industry-standard parity). */
  .preview-close {
    opacity: 0;
  }

  .tab-button:hover .preview-close {
    opacity: 0.5;
  }

  .tab-button:hover .preview-close:hover {
    opacity: 1;
  }

  /* Tab drag-to-reorder drop indicators (#175). A vertical accent line at
     the left/right edge of the hovered tab, matching the sidebar's
     drag-over-top/bottom style for visual consistency. */
  .tab-button.tab-drop-before::before {
    content: '';
    position: absolute;
    left: -1px;
    top: 4px;
    bottom: 4px;
    width: 2px;
    background: var(--color-accent-primary-start, #2dd4bf);
    border-radius: 1px;
    z-index: 1;
  }

  .tab-button.tab-drop-after::after {
    content: '';
    position: absolute;
    right: -1px;
    top: 4px;
    bottom: 4px;
    width: 2px;
    background: var(--color-accent-primary-start, #2dd4bf);
    border-radius: 1px;
    z-index: 1;
  }

  /* Per-tab dirty/save-state indicators (#167). The dirty dot uses
     --text-muted; the error glyph uses --status-danger. Both are icons
     (not color alone) + the tab's title/tooltip carries the state text
     for AT users. */
  .tab-save-state {
    display: flex;
    align-items: center;
    flex-shrink: 0;
    line-height: 1;
  }

  .tab-save-state.dirty {
    color: var(--color-text-muted, #8b8b94);
    animation: dirty-pulse 2s ease-in-out infinite;
  }

  .tab-save-state.error {
    color: var(--color-status-danger, #f43f5e);
  }

  @keyframes dirty-pulse {
    0%, 100% { opacity: 0.6; }
    50% { opacity: 1; }
  }

  @media (prefers-reduced-motion: reduce) {
    .tab-save-state.dirty {
      animation: none;
      opacity: 0.8;
    }
  }
</style>
