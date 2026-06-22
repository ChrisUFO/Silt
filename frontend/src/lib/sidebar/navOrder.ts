import { GetNavOrder, SetNavOrder } from '../../../wailsjs/go/main/App.js'

export interface NavOrderState {
  notebooks: string[]
  sections: Record<string, string[]>
  pages: Record<string, string[]>
}

/**
 * Sort items by a custom order, falling back to alphabetical. Pure function.
 */
export function sortByName<T extends { name: string }>(
  items: T[],
  order: string[] | undefined
): T[] {
  if (!order || order.length === 0) {
    return [...items].sort((a, b) => a.name.localeCompare(b.name))
  }
  const orderMap = new Map(order.map((n, i) => [n, i]))
  return [...items].sort((a, b) => {
    const ai = orderMap.get(a.name) ?? Infinity
    const bi = orderMap.get(b.name) ?? Infinity
    if (ai !== bi) return ai - bi
    return a.name.localeCompare(b.name)
  })
}

export interface NavOrderDeps {
  onStateChange: (state: NavOrderState) => void
}

/**
 * Manages nav-order persistence for the sidebar. Loads from and saves to
 * config.yaml via IPC.
 *
 * Usage:
 *   const navOrder = new NavOrderManager({ onStateChange: (s) => navOrderState = s })
 *   await navOrder.load()
 *   await navOrder.persistSectionOrder('Work', ['Journal', 'Projects'])
 */
export class NavOrderManager {
  private state: NavOrderState = {
    notebooks: [],
    sections: {},
    pages: {}
  }
  private deps: NavOrderDeps
  private loadGen = 0

  constructor(deps: NavOrderDeps) {
    this.deps = deps
  }

  get current(): NavOrderState {
    return this.state
  }

  /** Load nav order from config.yaml. No-op on failure (alphabetical fallback). */
  async load(): Promise<void> {
    const gen = ++this.loadGen
    try {
      const order = await GetNavOrder()
      if (gen !== this.loadGen) return
      this.state = {
        notebooks: order.notebooks ?? [],
        sections: Object.fromEntries(Object.entries(order.sections ?? {})),
        pages: Object.fromEntries(Object.entries(order.pages ?? {}))
      }
      this.deps.onStateChange(this.state)
    } catch {
      // Pre-vault or config not loaded — alphabetical fallback.
    }
  }

  /** Persist a new section order for a notebook. */
  async persistSectionOrder(notebook: string, sections: string[]): Promise<void> {
    const snapshot = this.state
    this.state = {
      ...this.state,
      sections: { ...this.state.sections, [notebook]: sections }
    }
    this.deps.onStateChange(this.state)
    try {
      await SetNavOrder(this.state)
    } catch (e) {
      console.error('SetNavOrder failed:', e)
      this.state = snapshot
      this.deps.onStateChange(this.state)
    }
  }

  /** Persist a new page order for a section. */
  async persistPageOrder(sectionKey: string, pages: string[]): Promise<void> {
    const snapshot = this.state
    this.state = {
      ...this.state,
      pages: { ...this.state.pages, [sectionKey]: pages }
    }
    this.deps.onStateChange(this.state)
    try {
      await SetNavOrder(this.state)
    } catch (e) {
      console.error('SetNavOrder failed:', e)
      this.state = snapshot
      this.deps.onStateChange(this.state)
    }
  }
}
