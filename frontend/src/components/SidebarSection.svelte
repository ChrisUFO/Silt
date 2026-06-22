<script lang="ts">
  // Recursive section renderer for the sidebar tree (#88). Renders one
  // NavigationSection plus its nested Children. Each level tracks its own
  // expanded state via the parent Sidebar's `expandedSections` set (keyed
  // by the section's single-segment display name — the immediate folder
  // name, not the full path).
  //
  // Drag-to-reorder (#68), right-click context menu (#62), and HTML5 DnD
  // handlers are threaded in from the parent Sidebar so every section and
  // page — top-level or deeply nested — retains those capabilities.
  import SidebarSection from './SidebarSection.svelte'

  interface NavPage {
    name: string
    count: number
  }
  interface NavSection {
    name: string
    path?: string
    pages: NavPage[]
    children?: NavSection[]
  }

  interface DropTarget {
    level: string
    name: string
    before: boolean
  }

  interface Props {
    section: NavSection
    depth: number
    activeNotebook: string
    activeSection: string
    activePage: string
    expandedSections: Set<string>
    navOrder: {
      pages: Record<string, string[]>
    }
    dropTarget?: DropTarget | null
    dragItem?: { level: string; name: string; section?: string } | null
    onToggleSection: (name: string) => void
    onSelectPage: (section: string, page: string) => void
    onPinPage: (section: string, page: string) => void
    onSelectSection: (section: string) => void
    onCreatePageInline: (section: string) => void
    onDragStart: (
      e: DragEvent,
      level: string,
      name: string,
      section?: string
    ) => void
    onDragOver: (e: DragEvent, level: string, name: string) => void
    onDragLeave: () => void
    onDrop: (
      e: DragEvent,
      level: string,
      targetName: string,
      notebook?: string,
      section?: string
    ) => void
    onDragEnd: () => void
    onContextMenu: (
      e: MouseEvent,
      level: 'section' | 'page',
      notebook: string,
      section?: string,
      page?: string
    ) => void
  }

  let {
    section,
    depth,
    activeNotebook,
    activeSection,
    activePage,
    expandedSections,
    navOrder,
    dropTarget = null,
    dragItem = null,
    onToggleSection,
    onSelectPage,
    onPinPage,
    onSelectSection,
    onCreatePageInline,
    onDragStart,
    onDragOver,
    onDragLeave,
    onDrop,
    onDragEnd,
    onContextMenu
  }: Props = $props()

  let sectionKey = $derived(section.path || section.name)
  let isExpanded = $derived(expandedSections.has(sectionKey))

  function sortByName<T extends { name: string }>(
    items: T[],
    order: string[] | undefined
  ): T[] {
    if (!order || order.length === 0) return items
    const orderMap = new Map(order.map((n, i) => [n, i]))
    return [...items].sort((a, b) => {
      const ai = orderMap.has(a.name) ? orderMap.get(a.name)! : Infinity
      const bi = orderMap.has(b.name) ? orderMap.get(b.name)! : Infinity
      if (ai !== bi) return ai - bi
      return a.name.localeCompare(b.name)
    })
  }

  let sortedPages = $derived(
    sortByName(
      section.pages,
      navOrder.pages[`${activeNotebook}/${sectionKey}`] ?? []
    )
  )

  function recursivePageCount(sec: NavSection): number {
    let count = sec.pages.length
    if (sec.children) {
      for (const child of sec.children) {
        count += recursivePageCount(child)
      }
    }
    return count
  }

  let totalCount = $derived(recursivePageCount(section))
</script>

<div class="mb-0.5">
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
  <div
    class="group flex items-center gap-1 px-2 py-1.5 cursor-pointer rounded hover:bg-hover transition-colors"
    class:drag-over-top={dropTarget?.level === 'section' &&
      dragItem?.level !== 'page' &&
      dropTarget.name === section.name &&
      dropTarget.before}
    class:drag-over-bottom={dropTarget?.level === 'section' &&
      dragItem?.level !== 'page' &&
      dropTarget.name === section.name &&
      !dropTarget.before}
    class:drag-over-into={dropTarget?.level === 'section' &&
      dragItem?.level === 'page' &&
      dropTarget.name === section.name}
    draggable="true"
    ondragstart={(e) => onDragStart(e, 'section', section.name)}
    ondragover={(e) => onDragOver(e, 'section', section.name)}
    ondragleave={onDragLeave}
    ondrop={(e) => onDrop(e, 'section', section.name, activeNotebook, sectionKey)}
    ondragend={onDragEnd}
    onclick={() => onToggleSection(sectionKey)}
    onkeydown={(e) => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault()
        onToggleSection(sectionKey)
      }
    }}
    oncontextmenu={(e) =>
      onContextMenu(e, 'section', activeNotebook, sectionKey)}
    role="treeitem"
    tabindex="0"
    aria-level={depth + 1}
    aria-expanded={isExpanded}
    aria-selected={activeSection === sectionKey}
  >
    <span
      class="material-symbols-outlined text-text-muted text-[16px] transition-transform"
      class:rotate-90={isExpanded}
    >
      chevron_right
    </span>
    <span class="material-symbols-outlined text-text-muted text-[17px]">
      {section.name ? 'folder' : 'drafts'}
    </span>
    <span
      class="font-label-sm-bold text-label-sm-bold uppercase tracking-wider text-text-primary truncate flex-1"
    >
      {section.name ? section.name : 'Pages (no section)'}
    </span>
    <span
      class="text-[9px] font-label-sm text-text-muted bg-panel border border-border-muted rounded-full px-1.5 py-0.5"
    >
      {totalCount}
    </span>
    <button
      onclick={(e) => {
        e.stopPropagation()
        onSelectSection(sectionKey)
        onCreatePageInline(sectionKey)
      }}
      title="New page in this section"
      class="opacity-0 group-hover:opacity-100 text-text-muted hover:text-accent-primary-start border-none bg-transparent cursor-pointer p-0.5 rounded transition-all"
    >
      <span class="material-symbols-outlined text-[16px]">add</span>
    </button>
  </div>

  {#if isExpanded}
    <div class="ml-4 border-l border-border-muted pl-1 mt-0.5 mb-1.5">
      {#if section.pages.length === 0 && (!section.children || section.children.length === 0)}
        <div
          class="text-text-muted text-[11px] font-body-md py-1.5 px-2 italic"
        >
          No pages. Click + to add one.
        </div>
      {:else}
        {#each sortedPages as pg (pg.name)}
          {@const isActive =
            activeSection === sectionKey && activePage === pg.name}
          <button
            onclick={() => onSelectPage(sectionKey, pg.name)}
            ondblclick={() => onPinPage(sectionKey, pg.name)}
            onauxclick={(e) => {
              // Middle-click (button 1) pins the page — industry-standard parity (#142).
              if (e.button === 1) {
                e.preventDefault()
                onPinPage(sectionKey, pg.name)
              }
            }}
            oncontextmenu={(e) =>
              onContextMenu(e, 'page', activeNotebook, sectionKey, pg.name)}
            draggable="true"
            ondragstart={(e) => onDragStart(e, 'page', pg.name, sectionKey)}
            ondragover={(e) => onDragOver(e, 'page', pg.name)}
            ondragleave={onDragLeave}
            ondrop={(e) =>
              onDrop(e, 'page', pg.name, activeNotebook, sectionKey)}
            ondragend={onDragEnd}
            class="relative w-full text-left pl-4 pr-2 py-1.5 rounded text-[13px] font-body-md transition-colors border-none bg-transparent cursor-pointer flex items-center gap-2"
            class:bg-hover={isActive}
            class:text-accent-primary-start={isActive}
            class:text-text-muted={!isActive}
            class:hover:text-text-primary={!isActive}
            class:drag-over-top={dropTarget?.level === 'page' &&
              dropTarget.name === pg.name &&
              dropTarget.before}
            class:drag-over-bottom={dropTarget?.level === 'page' &&
              dropTarget.name === pg.name &&
              !dropTarget.before}
            role="treeitem"
            aria-level={depth + 2}
            aria-selected={isActive}
          >
            {#if isActive}
              <span
                class="absolute left-0 top-1 bottom-1 w-[2px] bg-accent-primary-start rounded-full"
              ></span>
            {/if}
            <span class="material-symbols-outlined text-[15px]">article</span>
            <span class="truncate flex-1" title={pg.name}>{pg.name}</span>
          </button>
        {/each}
      {/if}
    </div>

    {#if section.children && section.children.length > 0}
      {#each section.children as child (child.name)}
        <SidebarSection
          section={child}
          depth={depth + 1}
          {activeNotebook}
          {activeSection}
          {activePage}
          {expandedSections}
          {navOrder}
          {dropTarget}
          {dragItem}
          {onToggleSection}
          {onSelectPage}
          {onPinPage}
          {onSelectSection}
          {onCreatePageInline}
          {onDragStart}
          {onDragOver}
          {onDragLeave}
          {onDrop}
          {onDragEnd}
          {onContextMenu}
        />
      {/each}
    {/if}
  {/if}
</div>
