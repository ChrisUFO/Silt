import type { PluginContext, SqliteQueryResult, TaskStatus } from './sdk'
import {
  PluginRawQuery,
  PluginMutateBlock,
  PluginUpdateBlockState,
  PluginUpdateTaskMeta
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
 */
export function makePluginContext(): PluginContext {
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
    updateTaskMeta: (id, meta) =>
      PluginUpdateTaskMeta(
        id,
        meta.pinned === undefined ? -1 : meta.pinned ? 1 : 0,
        meta.progress === undefined ? -1 : meta.progress
      )
  }
}
