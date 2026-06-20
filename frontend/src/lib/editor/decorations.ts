// Decoration provider registry (#110). Plugins register functions that return
// plain decoration specs; a TipTap extension applies them as ProseMirror
// decorations (read-only overlays — highlight ranges, lint markers, spell-
// underline). Decorations are transient: recomputed each render, never
// persisted.

export interface DecorationSpec {
  /** Start position (inclusive, 0-based doc offset). */
  from: number
  /** End position (exclusive). */
  to: number
  /** CSS class(es) applied to the decorated range. */
  class?: string
  /** Inline decoration (default true). false = block-level widget. */
  inline?: boolean
}

export type DecorationProvider = (doc: {
  content?: any[]
  [k: string]: any
}) => DecorationSpec[]

const providers = new Map<
  string,
  { pluginID: string; fn: DecorationProvider }
>()

/** Register a decoration provider. Returns an unregister function. */
export function registerDecorationProvider(
  id: string,
  pluginID: string,
  fn: DecorationProvider
): () => void {
  const key = `${pluginID}:${id}`
  providers.set(key, { pluginID, fn })
  return () => providers.delete(key)
}

/** Unregister every provider for a plugin (cleanup on disable/uninstall). */
export function unregisterPluginDecorations(pluginID: string): void {
  for (const key of [...providers.keys()]) {
    if (providers.get(key)?.pluginID === pluginID) providers.delete(key)
  }
}

/** Compute all decorations for a doc by calling every registered provider. */
export function computeDecorations(doc: {
  content?: any[]
  [k: string]: any
}): DecorationSpec[] {
  const out: DecorationSpec[] = []
  for (const { fn } of providers.values()) {
    try {
      const specs = fn(doc)
      if (Array.isArray(specs)) out.push(...specs)
    } catch (err) {
      // A provider throwing must never break the editor.
      // eslint-disable-next-line no-console
      console.error('[silt decorations] provider threw:', err)
    }
  }
  return out
}

/** Test-only: clear all providers. */
export function resetDecorationsForTests(): void {
  providers.clear()
}
