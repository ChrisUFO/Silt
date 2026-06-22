/**
 * Navigation tree types — shared between Sidebar.svelte, SidebarSection.svelte,
 * useDragDrop.ts, and navOrder.ts. Mirrors the `NavigationTree` returned by
 * the `ListNavigation` Wails binding.
 *
 * Single source of truth — previously these were declared 3× with subtly
 * different shapes (SidebarSection had `path?`, useDragDrop used a looser
 * `{name}` for pages). The unified shape is the strictest superset.
 */

export interface NavPage {
  name: string
  count: number
}

export interface NavSection {
  name: string
  path?: string
  pages: NavPage[]
  children?: NavSection[]
}

export interface NavNotebook {
  name: string
  sections: NavSection[]
  source?: string
  root_path?: string
  disconnected?: boolean
}

export interface NavigationTree {
  notebooks: NavNotebook[]
}
