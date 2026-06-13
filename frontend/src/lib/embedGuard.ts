// Module-scoped set shared across all EmbedPortal instances in the render
// tree, so an embed whose UUID is already being rendered up the ancestor
// chain is detected as recursive (prevents A→B→A loops).
export const embedRenderStack = new Set<string>()
