import {
  AcquireFocusLock,
  ReleaseFocusLock,
  RefreshFocusLock
} from '../../../wailsjs/go/main/App.js'

export interface FocusLockDeps {
  getNotebook: () => string
  getSection: () => string
  getPage: () => string
  getEditor: () => import('svelte-tiptap').Editor | null
  onBlockFocus?: (blockId: string, ancestors: string[]) => void
}

/**
 * Manages the focus-lock lifecycle for a TipTap editor page. The lock prevents
 * concurrent edits to the same file from multiple views (embed portals, kanban
 * detail panels, etc.). A TTL heartbeat keeps the lease alive.
 *
 * Usage:
 *   const lock = new FocusLockManager({ notebook, section, page, getEditor })
 *   await lock.acquire()
 *   lock.startHeartbeat()
 *   // on unmount:
 *   await lock.release()
 */
export class FocusLockManager {
  private hasLock = false
  private heartbeatInterval: ReturnType<typeof setInterval> | null = null
  private deps: FocusLockDeps

  constructor(deps: FocusLockDeps) {
    this.deps = deps
  }

  get locked(): boolean {
    return this.hasLock
  }

  async acquire(): Promise<void> {
    try {
      await AcquireFocusLock(
        this.deps.getNotebook(),
        this.deps.getSection(),
        this.deps.getPage()
      )
      this.hasLock = true
    } catch (e) {
      console.error('FocusLockManager: AcquireFocusLock failed:', e)
    }
  }

  async release(): Promise<void> {
    if (!this.hasLock) return
    this.hasLock = false
    try {
      await ReleaseFocusLock(
        this.deps.getNotebook(),
        this.deps.getSection(),
        this.deps.getPage()
      )
    } catch (e) {
      console.error('FocusLockManager: ReleaseFocusLock failed:', e)
    }
  }

  startHeartbeat(): void {
    if (!this.hasLock) return
    this.stopHeartbeat()
    this.heartbeatInterval = setInterval(() => {
      if (!this.hasLock) return
      RefreshFocusLock(
        this.deps.getNotebook(),
        this.deps.getSection(),
        this.deps.getPage()
      ).catch(() => {
        // Transient IPC error — the next tick retries.
      })
    }, 20000)
  }

  stopHeartbeat(): void {
    if (this.heartbeatInterval !== null) {
      clearInterval(this.heartbeatInterval)
      this.heartbeatInterval = null
    }
  }

  /** Walk the ProseMirror selection to find the active block and its ancestors. */
  notifyFocus(): void {
    const editor = this.deps.getEditor()
    if (!this.deps.onBlockFocus) return
    if (!editor || editor.isDestroyed) return
    const pos = editor.state.selection.$from
    for (let d = pos.depth; d >= 1; d--) {
      const node = pos.node(d)
      if (node && node.attrs && (node.attrs as Record<string, unknown>).id) {
        const blockId = (node.attrs as Record<string, unknown>).id as string
        const ancestors: string[] = [blockId]
        for (let pd = d - 1; pd >= 1; pd--) {
          const pnode = pos.node(pd)
          if (
            pnode &&
            pnode.attrs &&
            (pnode.attrs as Record<string, unknown>).id
          ) {
            ancestors.unshift(
              (pnode.attrs as Record<string, unknown>).id as string
            )
          }
        }
        this.deps.onBlockFocus(blockId, ancestors)
        return
      }
    }
  }
}
