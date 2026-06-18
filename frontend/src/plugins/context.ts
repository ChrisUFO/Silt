import type { PluginContext, SqliteQueryResult, TaskStatus } from './sdk'
import { localToday } from './sdk'
import {
  PluginRawQuery,
  PluginMutateBlock,
  PluginUpdateBlockState,
  PluginUpdateTaskMeta,
  GetPluginSettingsForNotebook
} from '../../wailsjs/go/main/App.js'
import { getActiveLocation } from './location.svelte'

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
export function makePluginContext(pluginID: string): PluginContext {
  const loc = getActiveLocation()
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
      PluginRawQuery(sql, params ?? []).then((res) => {
        const out: SqliteQueryResult = {
          rows: (res?.rows as Record<string, unknown>[]) ?? [],
          truncated: !!res?.truncated
        }
        return out
      }),
    mutateBlock: (id, text) => PluginMutateBlock(id, text),
    updateBlockState: (id, status: TaskStatus) =>
      PluginUpdateBlockState(id, status),
    // Pin/progress are file-resident user intent (ARCHITECTURE §0). The
    // Go side uses int sentinels for the tri-state pin (#123):
    //   -2 = clear the [pin::] token, -1 = no change,
    //    0 = [pin:: false], 1 = [pin:: true]
    // and -1/0..100 for progress. The SDK wrapper translates the ergonomic
    // boolean|null / number API to those sentinels.
    updateTaskMeta: (id, meta) =>
      PluginUpdateTaskMeta(
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
      )
  }
}
