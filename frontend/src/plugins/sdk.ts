// Silt Plugin SDK — the contract every plugin (first- or third-party) uses.
// Mirrors SPECS.md §8.2.

export type TaskStatus = 'TODO' | 'DOING' | 'DONE'

/**
 * Today's date in the user's LOCAL timezone as YYYY-MM-DD.
 *
 * Plugins compare against this instead of SQLite's `date('now')`, which is
 * UTC and produces off-by-one results for the "today"/"overdue"/"this week"
 * quick-picks near local midnight (#118). The webview's local timezone is
 * the OS timezone (same machine as the Go backend's `time.Local`), so this
 * is computed in-process — no IPC round-trip, and it stays in sync with the
 * system clock on every read.
 */
export function localToday(): string {
  const d = new Date()
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${day}`
}

/**
 * Add `n` days to a YYYY-MM-DD string and return the resulting YYYY-MM-DD.
 * Used for date-range bounds like "this week" (today + 7). Operates in the
 * local timezone via Date arithmetic so month/year boundaries roll over
 * correctly. Pure + deterministic → trivially unit-testable.
 */
export function plusDaysISO(iso: string, n: number): string {
  // Parse as local Y/M/D (not UTC) to avoid off-by-one from Date's UTC
  // default parsing of date-only strings.
  const [y, m, d] = iso.split('-').map(Number)
  const date = new Date(y, (m ?? 1) - 1, d ?? 1)
  date.setDate(date.getDate() + n)
  const yy = date.getFullYear()
  const mm = String(date.getMonth() + 1).padStart(2, '0')
  const dd = String(date.getDate()).padStart(2, '0')
  return `${yy}-${mm}-${dd}`
}

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
   * Today's date in the user's LOCAL timezone as YYYY-MM-DD. Read this
   * instead of SQLite's `date('now')` (UTC) so date comparisons match the
   * local day (#118). A plain getter returning a fresh value on each read.
   */
  today: string
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
   *
   * Pin is tri-state (#123): `true`→`[pin:: true]`, `false`→`[pin:: false]`
   * (explicit unpinned, preserved across round-trips), `null`→clears the
   * token entirely. `undefined` leaves the pin unchanged.
   */
  updateTaskMeta: (
    id: string,
    meta: { pinned?: boolean | null; progress?: number }
  ) => Promise<boolean>
  /**
   * Resolve this plugin's settings map for the ACTIVE notebook, applying the
   * co-located per-notebook override layer (#133). For a vault notebook (or
   * no active notebook), returns the vault-scoped config.yaml entry for this
   * plugin. For a linked notebook, returns the deep-merge of the vault entry
   * with the linked notebook's co-located `<root>/.system/config.yaml` entry
   * (linked wins per-key). The co-located file is READ-ONLY / user-authored;
   * Silt persists plugin settings to the vault config via updatePluginSetting.
   *
   * Re-read on every call so an external edit (vault or co-located) is
   * reflected immediately; the `linked-config:changed` event drives reactive
   * refreshes for active UIs.
   */
  getPluginSettings: () => Promise<Record<string, any>>
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
