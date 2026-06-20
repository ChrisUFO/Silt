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
  /**
   * Resolve a SINGLE setting key with schema-default fallback (#103). Reads
   * the merged per-active-notebook settings and falls back to the schema's
   * default when the key is absent. Returns undefined if neither a stored
   * value nor a default exists.
   */
  getSetting: (key: string) => Promise<any | undefined>
  /**
   * Subscribe to a typed host event (#106). Returns an unsubscribe function;
   * the host also auto-cleans every subscription on plugin disable/uninstall/
   * vault close, so a plugin cannot leak listeners across reloads. The
   * recommended debounce pattern for high-frequency events (esp. block:changed)
   * is the plugin's responsibility.
   *
   * Initial event set:
   *   - 'block:changed'            → BlockChangedEvent
   *   - 'config:changed'           → SystemConfig (full config snapshot)
   *   - 'active-notebook:changed'  → ActiveNotebookChangedEvent
   *   - 'selection:changed'        → SelectionChangedEvent
   */
  on: <E extends PluginEventName>(
    event: E,
    cb: (payload: PluginEventPayload<E>) => void
  ) => () => void

  // --- Expanded content API (#104) --------------------------------------

  /** Query helpers: typed wrappers over sqliteQuery (read-only, no grant). */
  queryByTag: (path: string) => Promise<SqliteQueryResult>
  queryByDateRange: (start: string, end: string) => Promise<SqliteQueryResult>
  fullTextSearch: (query: string) => Promise<SqliteQueryResult>
  getBacklinks: (uuid: string) => Promise<SqliteQueryResult>
  getEmbeds: (uuid: string) => Promise<SqliteQueryResult>

  /**
   * Block CRUD (#104). These reuse the same atomic-write + re-index path as
   * the core editor (no capability grant — consistent with mutateBlock).
   * createBlock returns the new block's UUID.
   */
  createBlock: (opts: {
    type: 'TASK' | 'NOTE' | 'HEADER'
    text: string
    after?: string
    notebook?: string
    section?: string
    page?: string
  }) => Promise<string>
  deleteBlock: (uuid: string) => Promise<boolean>
  moveBlock: (
    uuid: string,
    opts: { after?: string; notebook?: string; section?: string; page?: string }
  ) => Promise<boolean>

  /** Page / section / notebook CRUD (sandboxed wrappers over App methods). */
  createPage: (
    notebook: string,
    section: string,
    page: string,
    date?: string
  ) => Promise<string>
  createSection: (notebook: string, section: string) => Promise<boolean>
  createNotebook: (name: string) => Promise<boolean>
  deletePage: (
    notebook: string,
    section: string,
    page: string
  ) => Promise<boolean>
  renamePage: (
    notebook: string,
    section: string,
    oldName: string,
    newName: string
  ) => Promise<boolean>

  // --- Plugin file I/O (#108) — capability-gated (read-files / write-files) ---

  /**
   * Read a file within a notebook (relative path, traversal-guarded). Returns
   * the file bytes as a Uint8Array. Gated by read-files.
   */
  readFile: (notebook: string, relPath: string) => Promise<Uint8Array>
  /**
   * Write a file within a notebook atomically (temp+fsync+rename, same lock
   * path as note writes). Restricted to attachments/ + plugin scratch dirs.
   * Gated by write-files.
   */
  writeFile: (
    notebook: string,
    relPath: string,
    data: Uint8Array
  ) => Promise<boolean>
  /** Delete a file within a notebook. Gated by write-files. */
  deleteFile: (notebook: string, relPath: string) => Promise<boolean>
  /** List the immediate children of a directory within a notebook. Gated by read-files. */
  listDir: (notebook: string, relPath: string) => Promise<string[]>
  /** Resolve a notebook's absolute root dir (in-vault or linked per #100). Gated by read-files. */
  notebookRoot: (notebook: string) => Promise<string>
  /** Get (and lazily create) this plugin's per-notebook scratch dir. Gated by write-files. */
  scratchDir: (notebook: string) => Promise<string>
  /** Get (and lazily create) this plugin's vault-scoped scratch dir (caches). Gated by write-files. */
  vaultScratchDir: () => Promise<string>
  /** Resolve a relative asset path against a notebook root. Gated by read-files. */
  resolveAsset: (notebook: string, relPath: string) => Promise<string>
  /** Get the navigation tree (notebook > section > page). Read-only. */
  getNavigationTree: () => Promise<{
    notebooks: Array<{
      name: string
      sections: Array<{ name: string; pages: Array<{ name: string }> }>
    }>
  }>

  // --- OS integration (#114) — capability-gated ---------------------------

  /** Open a notebook file in the OS native handler. Gated by os-open. */
  openInNativeHandler: (notebook: string, relPath: string) => Promise<boolean>
  /** Open a URL (http/https/mailto only) in the system browser. Gated by os-open. */
  openUrl: (url: string) => Promise<boolean>
  /** Native open-file picker (user-driven; returns the chosen path or ""). */
  pickOpenFile: (filterPattern?: string) => Promise<string>
  /** Native save-file picker (user-driven; returns the chosen path or ""). */
  pickSaveFile: (defaultFilename?: string) => Promise<string>
  /** Read the system clipboard (text). Gated by os-clipboard. */
  clipboardRead: () => Promise<string>
  /** Write text to the system clipboard. Gated by os-clipboard. */
  clipboardWrite: (text: string) => Promise<boolean>
  /** Show a desktop notification. Gated by os-notify. */
  notify: (opts: { title: string; body: string }) => Promise<boolean>

  // --- Network / fetch (#115) — capability-gated ---------------------------

  /**
   * HTTP fetch through the Go-side proxy (CORS-free, with timeout/size/
   * redirect caps). Host + status are audit-logged (never the body). Gated by
   * the network capability.
   */
  fetch: (
    url: string,
    opts?: {
      method?: string
      headers?: Record<string, string>
      body?: string
      timeoutMs?: number
    }
  ) => Promise<{
    status: number
    headers: Record<string, string>
    body: string
    ok: boolean
  }>

  // --- Editor extension points (#110) ------------------------------------

  /**
   * Register a slash-menu command (#110). The command appears in the `/` menu
   * alongside built-ins; when selected, `onSelect` is called with the live
   * TipTap editor instance + cursor position. The id is namespaced as
   * `<this plugin's id>:<id>` to avoid collisions. Returns an unregister fn.
   * Registration is user-driven (a menu item) so it is not capability-gated;
   * the handler's own privileged calls route through the normal gates.
   */
  registerSlashCommand: (cmd: {
    id: string
    label: string
    description?: string
    icon?: string
    onSelect: (editor: unknown, pos: number) => void
  }) => () => void

  /**
   * Register a read-only decoration provider (#110). The provider is called
   * on each editor render with the current doc and returns an array of
   * decoration specs (from/to/class). Decorations are transient — never
   * persisted. Returns an unregister function.
   */
  provideDecorations: (
    id: string,
    provider: (
      doc: unknown
    ) => Array<{ from: number; to: number; class?: string }>
  ) => () => void

  // --- Rendered UI surfaces (#117) — capability-gated ---------------------

  /**
   * Register a rendered UI surface (#117). The surface HTML runs in a sandboxed
   * iframe (srcdoc, allow-scripts but not allow-same-origin); a postMessage
   * bridge proxies this PluginContext into the iframe. Theme tokens are
   * injected so the surface matches the active theme. Gated by ui-surface.
   * Returns an unregister function.
   */
  registerSurface: (surface: {
    id: string
    kind:
      | 'sidebar-panel'
      | 'modal'
      | 'status-bar-item'
      | 'command-palette-entry'
      | 'settings-panel'
    label: string
    icon?: string
    html: string
  }) => () => void

  // --- Attachments (#101) -------------------------------------------------

  /**
   * Copy a source file (absolute path) into the notebook's attachments/
   * directory and return the relative link path. Collision-safe (counter
   * suffix on duplicate names). Resolves against the notebook's actual root
   * (#100, in-vault or linked). #101.
   */
  addAttachment: (srcPath: string, notebook?: string) => Promise<string>
  /** Open an attachment in the OS native handler. #101. */
  openAttachment: (notebook: string, relPath: string) => Promise<boolean>
  /** Delete an attachment file (unlink-only; orphan GC is separate). #101. */
  deleteAttachment: (notebook: string, relPath: string) => Promise<boolean>
}

// --- v2 SDK typed event bus (#106) ---------------------------------------

/** Names of the host events a plugin may subscribe to via ctx.on. */
export type PluginEventName =
  | 'block:changed'
  | 'config:changed'
  | 'active-notebook:changed'
  | 'selection:changed'
  | 'editor:save'

/** Payload of the 'block:changed' event — mirrors Go parser.BlockChangedEvent. */
export interface BlockChangedEvent {
  id: string
  notebook: string
  section: string
  page: string
  file_date: string
}

/** Payload of the 'active-notebook:changed' event (#106). Emitted when the
 *  navigator focus moves between notebook/section/page. */
export interface ActiveNotebookChangedEvent {
  notebook: string
  section: string
  page: string
}

/** Payload of the 'selection:changed' event from the TipTap editor (#106/#110). */
export interface SelectionChangedEvent {
  notebook: string
  section: string
  page: string
  /** Block id at the selection anchor, when inside a known block. */
  blockId?: string
}

/** Maps an event name to its typed payload (single source of truth). */
export type PluginEventPayload<E extends PluginEventName> = {
  'block:changed': BlockChangedEvent
  'config:changed': Record<string, unknown>
  'active-notebook:changed': ActiveNotebookChangedEvent
  'selection:changed': SelectionChangedEvent
  'editor:save': ActiveNotebookChangedEvent
}[E]

/** A capability id from the v2 SDK capability taxonomy (#113). */
export type Capability =
  | 'read-files'
  | 'write-files'
  | 'network'
  | 'os-open'
  | 'os-clipboard'
  | 'os-notify'
  | 'ui-surface'
  | 'editor-schema'

/** A capability scope qualifier (#113). 'granted' is the default whole-scope. */
export type CapabilityQualifier = 'granted' | 'notebook' | 'vault'

export interface PluginManifest {
  id: string
  name: string
  version: string
  author?: string
  description?: string
  icon?: string
  minSiltVersion?: string
  /**
   * The v2 SDK capability declaration (#113): capability id → true | scope
   * qualifier. Surfaced to the user at install; granted on first use.
   * Absent for plugins that use only the read-only SDK.
   */
  capabilities?: Record<string, true | CapabilityQualifier>
  /**
   * Declarative settings schema (#103). Settings → Plugins renders the form
   * generically from this; no plugin hand-rolls its settings panel. Each field
   * declares a type, a default, and optional validation. Resolution precedence
   * is user-global → vault → notebook (notebook-attached overrides via #100's
   * co-located config). Plugins read the merged value via ctx.getSetting(key).
   */
  settings?: SettingSchema[]
}

/** A single declarative settings field (#103). */
export interface SettingSchema {
  /** The settings key (stored under plugin_settings.<pluginID>.<key>). */
  key: string
  /** Human-readable label shown in the generated form. */
  label: string
  /** Field type — drives the generated input control. */
  type: 'string' | 'number' | 'bool' | 'select' | 'color' | 'keymap' | 'list'
  /** Default value when no setting is stored. */
  default?: unknown
  /** For 'select': the selectable options. */
  options?: string[]
  /** Optional help text under the field. */
  help?: string
  /** For 'string': min/max length validation. */
  minLength?: number
  maxLength?: number
  /** For 'number': min/max range validation. */
  min?: number
  max?: number
}

export interface SiltPlugin {
  manifest: PluginManifest
  /** Called once when the plugin is loaded; receives the host context. */
  init?: (ctx: PluginContext) => void
  /** Called after init once a vault is open and the context is fully usable (#106). */
  onVaultOpen?: (ctx: PluginContext) => void
  /** Called before the active vault tears down (workspace switch / app close) so
   *  the plugin can release watchers/timers. #106. */
  onVaultClose?: () => void
  /** Called during app shutdown, after onVaultClose. Best-effort: IPC may be
   *  tearing down. #106. */
  onShutdown?: () => void
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
  /** v2 lifecycle hooks (#106) — invoked by the host loader. */
  onVaultOpen?: (ctx: PluginContext) => void
  onVaultClose?: () => void
  onShutdown?: () => void
  /** Origin: bundled with the app vs loaded from .system/plugins/. */
  source: 'first-party' | 'disk'
}

export interface LoadedPlugins {
  plugins: Map<string, RegisteredPlugin>
  errors: { id: string; message: string }[]
}
