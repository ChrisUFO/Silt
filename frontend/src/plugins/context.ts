import type { PluginContext, SqliteQueryResult, TaskStatus } from './sdk'
import { localToday } from './sdk'
import {
  PluginRawQuery,
  PluginMutateBlock,
  PluginUpdateBlockState,
  PluginUpdateTaskMeta,
  GetPluginSettingsForNotebook,
  PluginCreateBlock,
  PluginDeleteBlock,
  PluginMoveBlock,
  PluginApplyBlocks,
  PluginCreatePage,
  PluginCreateSection,
  PluginCreateNotebook,
  PluginDeletePage,
  PluginRenamePage,
  PluginResolveNotebookRoot,
  PluginReadFile,
  PluginWriteFile,
  PluginDeleteFile,
  PluginListDir,
  PluginScratchDir,
  PluginOpenInNativeHandler,
  PluginOpenUrl,
  PluginPickOpenFile,
  PluginPickSaveFile,
  PluginClipboardReadText,
  PluginClipboardWriteText,
  PluginNotify,
  PluginFetch,
  PluginRegisterSurface,
  RegisterPluginSession,
  UnregisterPluginSession,
  AddAttachment,
  OpenAttachment,
  DeleteAttachment,
  PluginResolveAsset,
  PluginVaultScratchDir,
  PluginReadPluginAsset,
  PluginListNavigation,
  GetNetworkAudit,
  ClearNetworkAudit
} from '../../wailsjs/go/main/App.js'
import { getActiveLocation } from './location.svelte'
import { subscribe } from './events'
import {
  registerSlashCommand,
  unregisterSlashCommand
} from '../lib/editor/slash-registry'
import { registerDecorationProvider } from '../lib/editor/decorations'
import { loadedPlugins } from './store.svelte'
import { registerSurface, unregisterSurface } from './surfaces'

// getPluginSchemaDefault reads a plugin's declarative settings schema from the
// live registry and returns the default for key, or undefined (#103).
function getPluginSchemaDefault(pluginID: string, key: string): unknown {
  const reg = loadedPlugins.plugins.get(pluginID)
  const schema = reg?.manifest?.settings
  if (!schema) return undefined
  const field = schema.find((f) => f.key === key)
  return field?.default
}

/**
 * Build a PluginContext whose `activeNotebook/Section/Page` are live reactive
 * getters backed by the module-scoped $state in location.svelte.ts (#69).
 *
 * A plugin that caches `ctx` and reads `ctx.activeNotebook` at query time
 * always sees the live value. A plugin that destructures `const { activeNotebook }
 * = ctx` in `init()` gets a stale snapshot — that is an inherent limitation of
 * destructuring, documented in docs/PLUGIN_DEVELOPMENT.md.
 *
 * The `sqliteQuery` / `mutateBlock` / `updateBlockState` / `updateTaskMeta`
 * closures are stateless Wails bindings — they do not depend on the active
 * location (SQL queries include the location as explicit parameters).
 *
 * `pluginID` is captured in the `getPluginSettings` closure so a plugin does
 * not need to pass its own id when resolving its per-active-notebook settings
 * (#133); the loader passes the manifest id at context construction.
 */
export function makePluginContext(
  pluginID: string,
  sessionToken?: string
): PluginContext {
  const loc = getActiveLocation()
  // ctxSqliteQuery is the per-instance closure used by the query helpers
  // (queryByTag, fullTextSearch, etc.). It threads pluginID + sessionToken
  // through PluginRawQuery so the Go side can verify the caller's identity
  // (#236).
  const ctxSqliteQuery = (
    sql: string,
    params: unknown[]
  ): Promise<SqliteQueryResult> =>
    PluginRawQuery(pluginID, sessionToken ?? '', sql, params ?? []).then(
      (res) => ({
        rows: (res?.rows as Record<string, unknown>[]) ?? [],
        truncated: !!res?.truncated
      })
    )
  return {
    get activeNotebook() {
      return loc.notebook
    },
    get activeSection() {
      return loc.section
    },
    get activePage() {
      return loc.page
    },
    // Local-day anchor (#118): the webview's local timezone is the OS
    // timezone, identical to the Go backend's time.Local, so this is
    // resolved in-process (no IPC). Plugins compare against it instead of
    // SQLite's UTC date('now') to avoid off-by-one near local midnight.
    get today() {
      return localToday()
    },
    // The Go side returns PluginRawQueryResult{Rows, Truncated}. Surface the
    // structured shape (not just Rows) so plugins can warn on truncation;
    // a missing/empty Rows slice is normalised to [] for the caller's
    // convenience (Wails sometimes hands back undefined for an empty
    // top-level struct, especially before the vault is open).
    sqliteQuery: (sql, params) =>
      PluginRawQuery(pluginID, sessionToken ?? '', sql, params ?? []).then(
        (res) => {
          const out: SqliteQueryResult = {
            rows: (res?.rows as Record<string, unknown>[]) ?? [],
            truncated: !!res?.truncated
          }
          return out
        }
      ),
    mutateBlock: (id, text) =>
      PluginMutateBlock(pluginID, sessionToken ?? '', id, text),
    updateBlockState: (id, status: TaskStatus) =>
      PluginUpdateBlockState(pluginID, sessionToken ?? '', id, status),
    // Pin/progress are file-resident user intent (ARCHITECTURE §0). The
    // Go side uses int sentinels for the tri-state pin (#123):
    //   -2 = clear the [pin::] token, -1 = no change,
    //    0 = [pin:: false], 1 = [pin:: true]
    // and -1/0..100 for progress. The SDK wrapper translates the ergonomic
    // boolean|null / number API to those sentinels.
    updateTaskMeta: (id, meta) =>
      PluginUpdateTaskMeta(
        pluginID,
        sessionToken ?? '',
        id,
        meta.pinned === undefined
          ? -1
          : meta.pinned === null
            ? -2
            : meta.pinned
              ? 1
              : 0,
        meta.progress === undefined ? -1 : meta.progress
      ),
    // Per-active-notebook settings resolution (#133). pluginID is captured
    // at context construction; the live activeNotebook is read at call time
    // so switching notebooks re-resolves on the next call. The Go side
    // merges vault defaults with any co-located linked-notebook override.
    getPluginSettings: () =>
      GetPluginSettingsForNotebook(pluginID, loc.notebook).then(
        (settings) => settings ?? {}
      ),
    // Resolve a single setting with schema-default fallback (#103). The schema
    // is read from the live plugin registry (manifest); the value from the
    // merged per-notebook settings.
    getSetting: (key) =>
      GetPluginSettingsForNotebook(pluginID, loc.notebook).then((settings) => {
        const merged = settings ?? {}
        if (key in merged) return merged[key]
        // Fall back to the schema default if the plugin declared one.
        const schema = getPluginSchemaDefault(pluginID, key)
        return schema
      }),
    // v2 typed event bus (#106). Delegates to the module-scoped bus so
    // subscriptions are auto-cleaned on disable/uninstall/vault-close.
    on: (event, cb) => subscribe(pluginID, event, cb),

    // --- Expanded content API (#104) — query helpers built on sqliteQuery ---
    queryByTag: (path) =>
      ctxSqliteQuery(
        `SELECT b.* FROM blocks b JOIN tags t ON t.block_id = b.id WHERE t.raw_path = ? OR t.raw_path LIKE ? ORDER BY b.notebook, b.section, b.page, b.line_number`,
        [path, path + '/%']
      ),
    queryByDateRange: (start, end) =>
      ctxSqliteQuery(
        `SELECT b.id, b.notebook, b.section, b.page, b.clean_content, t.status, t.due_date, t.start_date
         FROM blocks b JOIN tasks t ON t.block_id = b.id
         WHERE (t.due_date BETWEEN ? AND ?) OR (t.start_date BETWEEN ? AND ?)
         ORDER BY t.due_date ASC`,
        [start, end, start, end]
      ),
    fullTextSearch: (query) =>
      ctxSqliteQuery(
        `SELECT b.id, b.notebook, b.section, b.page, b.clean_content, snippet(blocks_fts, -1, '<mark>', '</mark>', '…', 12) as snippet
         FROM blocks_fts f JOIN blocks b ON b.id = f.rowid
         WHERE blocks_fts MATCH ? ORDER BY rank LIMIT 50`,
        [query]
      ),
    getBacklinks: (id) =>
      ctxSqliteQuery(
        `SELECT b.id, b.notebook, b.section, b.page, b.clean_content
         FROM blocks b WHERE b.raw_content LIKE ? ORDER BY b.notebook, b.section, b.page`,
        ['%((' + id + ')%']
      ),
    getEmbeds: (id) =>
      ctxSqliteQuery(
        `SELECT b.id, b.notebook, b.section, b.page, b.clean_content
         FROM blocks b WHERE b.raw_content LIKE ? ORDER BY b.notebook, b.section, b.page`,
        ['%{{embed:' + id + '}}%']
      ),

    // --- Block CRUD (#104) — gated by content-mutate (#156) -----------------
    // Same atomic-write path as mutateBlock.
    createBlock: (opts) =>
      PluginCreateBlock(
        pluginID,
        sessionToken ?? '',
        opts.after ?? '',
        opts.notebook ?? '',
        opts.section ?? '',
        opts.page ?? '',
        opts.type,
        opts.text
      ),
    deleteBlock: (uuid) =>
      PluginDeleteBlock(pluginID, sessionToken ?? '', uuid).then(() => true),
    moveBlock: (uuid, opts) =>
      PluginMoveBlock(
        pluginID,
        sessionToken ?? '',
        uuid,
        opts.after ?? '',
        opts.notebook ?? '',
        opts.section ?? '',
        opts.page ?? ''
      ).then(() => true),

    // --- Batch block ops (#104) — gated by content-mutate (#156) ------------
    applyBlocks: (ops) =>
      PluginApplyBlocks(
        pluginID,
        sessionToken ?? '',
        ops.map((op) => ({
          kind: op.kind,
          afterId: op.after ?? '',
          type: op.type ?? '',
          text: op.text ?? '',
          blockId: op.blockId ?? '',
          notebook: op.notebook ?? '',
          section: op.section ?? '',
          page: op.page ?? ''
        }))
      ).then(() => true),

    // --- Page / section / notebook CRUD (#104) ------------------------------
    // Session-token verified (#236).
    createPage: (notebook, section, page, date) =>
      PluginCreatePage(
        pluginID,
        sessionToken ?? '',
        notebook,
        section,
        page,
        date ?? ''
      ),
    createSection: (notebook, section) =>
      PluginCreateSection(pluginID, sessionToken ?? '', notebook, section).then(
        () => true
      ),
    createNotebook: (name) =>
      PluginCreateNotebook(pluginID, sessionToken ?? '', name).then(() => true),
    deletePage: (notebook, section, page) =>
      PluginDeletePage(
        pluginID,
        sessionToken ?? '',
        notebook,
        section,
        page
      ).then(() => true),
    renamePage: (notebook, section, oldName, newName) =>
      PluginRenamePage(
        pluginID,
        sessionToken ?? '',
        notebook,
        section,
        oldName,
        newName
      ).then(() => true),

    // --- Plugin file I/O (#108) — capability-gated --------------------------
    // Session-token verified (#236).
    readFile: (notebook, relPath) =>
      PluginReadFile(pluginID, sessionToken ?? '', notebook, relPath).then(
        (res) => {
          // Wails encodes []byte as a base64 string over the IPC boundary.
          const b64 = (res?.bytes as unknown as string) ?? ''
          return base64ToUint8(b64)
        }
      ),
    writeFile: (notebook, relPath, data) =>
      PluginWriteFile(
        pluginID,
        sessionToken ?? '',
        notebook,
        relPath,
        data as unknown as never
      ).then(() => true),
    deleteFile: (notebook, relPath) =>
      PluginDeleteFile(pluginID, sessionToken ?? '', notebook, relPath).then(
        () => true
      ),
    listDir: (notebook, relPath) =>
      PluginListDir(pluginID, sessionToken ?? '', notebook, relPath).then(
        (r) => r ?? []
      ),
    notebookRoot: (notebook) =>
      PluginResolveNotebookRoot(pluginID, sessionToken ?? '', notebook),
    scratchDir: (notebook) =>
      PluginScratchDir(pluginID, sessionToken ?? '', notebook),
    vaultScratchDir: () => PluginVaultScratchDir(pluginID, sessionToken ?? ''),
    resolveAsset: (notebook, relPath) =>
      PluginResolveAsset(pluginID, sessionToken ?? '', notebook, relPath),
    readPluginAsset: (relPath) =>
      PluginReadPluginAsset(pluginID, sessionToken ?? '', relPath),
    getNavigationTree: () =>
      PluginListNavigation(pluginID, sessionToken ?? '').then(
        (tree) => tree ?? { notebooks: [] }
      ),
    // --- OS integration (#114) — capability-gated ---------------------------
    // Session-token verified (#236).
    openInNativeHandler: (notebook, relPath) =>
      PluginOpenInNativeHandler(
        pluginID,
        sessionToken ?? '',
        notebook,
        relPath
      ).then(() => true),
    openUrl: (url) =>
      PluginOpenUrl(pluginID, sessionToken ?? '', url).then(() => true),
    pickOpenFile: (filterPattern) =>
      PluginPickOpenFile(pluginID, sessionToken ?? '', filterPattern ?? '*'),
    pickSaveFile: (defaultFilename) =>
      PluginPickSaveFile(pluginID, sessionToken ?? '', defaultFilename ?? ''),
    clipboardRead: () => PluginClipboardReadText(pluginID, sessionToken ?? ''),
    clipboardWrite: (text) =>
      PluginClipboardWriteText(pluginID, sessionToken ?? '', text).then(
        () => true
      ),
    notify: (opts) =>
      PluginNotify(pluginID, sessionToken ?? '', opts.title, opts.body).then(
        () => true
      ),

    // --- Network / fetch (#115) — capability-gated --------------------------
    fetch: (url, opts) =>
      PluginFetch(pluginID, sessionToken ?? '', {
        url,
        method: opts?.method ?? '',
        headers: opts?.headers ?? {},
        body: opts?.body ?? '',
        timeout: opts?.timeoutMs ?? 0
      }).then((res) => ({
        status: res?.status ?? 0,
        headers: res?.headers ?? {},
        body: res?.body ?? '',
        ok: !!res?.ok,
        truncated: !!res?.truncated
      })),

    // --- Editor extension points (#110) — plugin slash commands -------------
    // The capability gate lives INSIDE the registry now (#158): the registry
    // checks isGranted(pluginID, 'editor-schema') from the trusted Go grant
    // cache. The SDK closure just delegates.
    registerSlashCommand: (cmd) => {
      const namespacedId = `${pluginID}:${cmd.id}`
      registerSlashCommand({
        id: namespacedId,
        label: cmd.label,
        description: cmd.description,
        icon: cmd.icon,
        pluginID,
        onSelect: cmd.onSelect
      })
      return () => unregisterSlashCommand(namespacedId)
    },

    // --- Editor decorations (#110) — read-only overlays --------------------
    // Capability gate lives INSIDE the registry (#158).
    provideDecorations: (id, provider) => {
      return registerDecorationProvider(id, pluginID, provider as any)
    },

    // --- Rendered UI surfaces (#117) — capability-gated (#154 Go-side gate) -
    registerSurface: (surface) => {
      // The Go-side gate is the enforcement point (#154). The frontend
      // registry adds the surface only after Go approves. The capability
      // check is ALSO mirrored inside surfaces.ts (Phase 2 / #158) for
      // defense in depth.
      const id = `${pluginID}:${surface.id}`
      let cleanup: (() => void) | null = null
      PluginRegisterSurface(
        pluginID,
        sessionToken ?? '',
        surface.id,
        surface.kind,
        surface.label
      )
        .then(() => {
          cleanup = registerSurface({
            id,
            pluginID,
            kind: surface.kind,
            label: surface.label,
            icon: surface.icon,
            html: surface.html
          })
        })
        .catch((err) => {
          // eslint-disable-next-line no-console
          console.warn(
            `[silt] plugin ${pluginID} surface "${surface.id}" registration denied:`,
            err
          )
        })
      return () => {
        cleanup?.()
        unregisterSurface(id)
      }
    },

    // --- Attachments (#101) ------------------------------------------------
    addAttachment: (srcPath, notebook) =>
      AddAttachment(srcPath, notebook ?? loc.notebook),
    openAttachment: (nb, relPath) =>
      OpenAttachment(nb, relPath).then(() => true),
    deleteAttachment: (nb, relPath) =>
      DeleteAttachment(nb, relPath).then(() => true)
  }
}

// ctxSqliteQuery is no longer a module-level helper: each makePluginContext
// call constructs its own closure (above, inside the returned object) that
// captures pluginID + sessionToken so the Go side can verify the caller's
// identity (#236). The query helpers (queryByTag, fullTextSearch, etc.) use
// that per-instance closure.

// base64ToUint8 decodes a base64 string into a Uint8Array (Wails transports
// []byte as base64 over the IPC boundary).
function base64ToUint8(b64: string): Uint8Array {
  if (!b64) return new Uint8Array(0)
  const bin = atob(b64)
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}
