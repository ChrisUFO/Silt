// Silt Plugin SDK — the contract every plugin (first- or third-party) uses.
// Mirrors SPECS.md §8.2.

export type TaskStatus = 'TODO' | 'DOING' | 'DONE'

/**
 * Result envelope returned by `PluginContext.sqliteQuery`. The shape
 * mirrors the Go-side `PluginRawQueryResult` struct: the row slice plus
 * a `truncated` flag the plugin can surface when the result hit the
 * Go-side `maxPluginQueryRows` cap (defense-in-depth memory safeguard).
 *
 * The split is intentional — silently truncating a vault-scope Kanban
 * query is exactly the kind of data-loss surprise a first-party plugin
 * shouldn't hide from the user. Plugins that don't care (Agenda,
 * Calendar) can simply destructure `rows` and ignore `truncated`.
 */
export interface SqliteQueryResult {
  rows: Record<string, unknown>[]
  truncated: boolean
}

export interface PluginContext {
  /**
   * The active notebook. This is a LIVE reactive getter (#69): reading it
   * inside a Svelte reactive context (template, $derived, $effect) tracks
   * navigation changes automatically. Do NOT destructure in init() — that
   * captures a stale snapshot. Read it at query/render time instead.
   */
  activeNotebook: string
  /** Active section — same reactive semantics as activeNotebook. */
  activeSection: string
  /** Active page — same reactivity as activeNotebook. */
  activePage: string
  /**
   * Read-only SQL against the in-memory index (SELECT/WITH only). Returns
   * the row slice plus a `truncated` flag; see `SqliteQueryResult`.
   */
  sqliteQuery: (sql: string, params?: unknown[]) => Promise<SqliteQueryResult>
  /** Rewrite a block's body text by UUID (preserves task syntax + UUID). */
  mutateBlock: (id: string, text: string) => Promise<boolean>
  /** Transition a task block's status. */
  updateBlockState: (id: string, status: TaskStatus) => Promise<boolean>
  /**
   * Update per-task metadata (pin, progress). Both fields are optional;
   * pass undefined to skip a field. Pin and progress are file-resident
   * user intent — the call round-trips through the markdown file.
   */
  updateTaskMeta: (
    id: string,
    meta: { pinned?: boolean; progress?: number }
  ) => Promise<boolean>
}

export interface PluginManifest {
  id: string
  name: string
  version: string
  author?: string
  description?: string
  icon?: string
  minSiltVersion?: string
}

export interface SiltPlugin {
  manifest: PluginManifest
  /** Called once when the plugin is loaded; receives the host context. */
  init?: (ctx: PluginContext) => void
}

// A renderable, registered plugin. First-party plugins supply a compiled
// Svelte component; on-disk (third-party) plugins supply one via the loader
// host when possible.
export interface RegisteredPlugin {
  manifest: PluginManifest
  /** Svelte component rendered for the plugin's view. */
  component: any
  /** Optional init hook invoked with the live PluginContext. */
  init?: (ctx: PluginContext) => void
  /** Origin: bundled with the app vs loaded from .system/plugins/. */
  source: 'first-party' | 'disk'
}

export interface LoadedPlugins {
  plugins: Map<string, RegisteredPlugin>
  errors: { id: string; message: string }[]
}
