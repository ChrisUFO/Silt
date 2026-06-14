import type { PluginContext, TaskStatus } from './sdk'
import {
  PluginRawQuery,
  PluginMutateBlock,
  PluginUpdateBlockState
} from '../../wailsjs/go/main/App.js'

/**
 * Build a PluginContext bound to the currently active location. Plugins read
 * the active notebook/section/page from this object and use the query/mutation
 * hooks to talk to the Go backend.
 */
export function makePluginContext(
  activeNotebook: string,
  activeSection: string,
  activePage: string
): PluginContext {
  return {
    activeNotebook,
    activeSection,
    activePage,
    sqliteQuery: (sql, params) =>
      PluginRawQuery(sql, params ?? []).then(
        (rows) => (rows as Record<string, unknown>[]) ?? []
      ),
    mutateBlock: (id, text) => PluginMutateBlock(id, text),
    updateBlockState: (id, status: TaskStatus) =>
      PluginUpdateBlockState(id, status)
  }
}
