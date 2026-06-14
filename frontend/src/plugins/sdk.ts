// Silt Plugin SDK — the contract every plugin (first- or third-party) uses.
// Mirrors SPECS.md §8.2.

export type TaskStatus = 'TODO' | 'DOING' | 'DONE'

export interface PluginContext {
  activeNotebook: string
  activeSection: string
  activePage: string
  /** Read-only SQL against the in-memory index (SELECT/WITH only). */
  sqliteQuery: (
    sql: string,
    params?: unknown[]
  ) => Promise<Record<string, unknown>[]>
  /** Rewrite a block's body text by UUID (preserves task syntax + UUID). */
  mutateBlock: (id: string, text: string) => Promise<boolean>
  /** Transition a task block's status. */
  updateBlockState: (id: string, status: TaskStatus) => Promise<boolean>
}

export interface PluginManifest {
  id: string
  name: string
  version: string
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
