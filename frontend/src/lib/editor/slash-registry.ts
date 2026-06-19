// Slash-command registry (#110). Refactors the previously hardcoded
// CommandPalette command list into an extensible registry so plugins can
// contribute `/`-menu entries alongside the built-ins.
//
// The registry is module-scoped (a Map<id, SlashCommand>). Built-ins register
// at boot; plugins register via ctx.registerSlashCommand. CommandPalette reads
// the union (grouped: Built-ins / Plugins). When a command is selected, the
// editor calls its handler with the live editor + cursor position.
//
// Registration is user-driven (a menu item) so it is not capability-gated; the
// handler's own privileged calls (file I/O, network, etc.) route through the
// normal capability gates.

export interface SlashCommand {
  /** Unique id. Plugin commands are namespaced as `<pluginID>:<id>`. */
  id: string
  /** Display label in the menu. */
  label: string
  description?: string
  icon?: string
  /** The plugin id that registered this command, or undefined for built-ins. */
  pluginID?: string
  /**
   * Invoked when the user selects the command from the slash menu. Receives
   * the live TipTap editor instance and the cursor position. For built-ins
   * this may be undefined (the editor dispatches by id instead); for plugin
   * commands it is required.
   */
  onSelect?: (editor: any, pos: number) => void
}

const registry = new Map<string, SlashCommand>()

/**
 * Register a slash command. A plugin command's id is namespaced as
 * `<pluginID>:<id>` to avoid collisions with built-ins. Re-registering the
 * same namespaced id replaces the prior entry (idempotent on reload).
 */
export function registerSlashCommand(cmd: SlashCommand): void {
  if (!cmd.id || !cmd.label) {
    throw new Error('SlashCommand requires id + label')
  }
  registry.set(cmd.id, cmd)
}

/** Unregister a command by id (used on plugin disable/uninstall). */
export function unregisterSlashCommand(id: string): void {
  registry.delete(id)
}

/** Unregister every command for a plugin (by pluginID prefix). */
export function unregisterPluginSlashCommands(pluginID: string): void {
  for (const id of [...registry.keys()]) {
    if (registry.get(id)?.pluginID === pluginID) {
      registry.delete(id)
    }
  }
}

/** Get the full command list (built-ins + plugins), sorted for display. */
export function getSlashCommands(): SlashCommand[] {
  return [...registry.values()].sort((a, b) => {
    // Built-ins first (alphabetical), then plugins (alphabetical).
    const aPlugin = a.pluginID ? 1 : 0
    const bPlugin = b.pluginID ? 1 : 0
    if (aPlugin !== bPlugin) return aPlugin - bPlugin
    return a.label.localeCompare(b.label)
  })
}

/** Test-only: clear the registry. */
export function resetSlashRegistryForTests(): void {
  registry.clear()
}

// --- Built-in slash commands ---------------------------------------------
// Registered once at module load. The handlers are NOT set here — the editor
// dispatches built-in ids via its own handler (handleSlashSelect), so the
// registry entries are metadata-only for built-ins. Plugin entries carry
// their own onSelect handler.

registerSlashCommand({
  id: 'todo',
  label: 'Task',
  description: 'Create task checkbox',
  icon: 'check_box'
})
registerSlashCommand({
  id: 'h1',
  label: 'Heading 1',
  description: 'Large section header',
  icon: 'format_size'
})
registerSlashCommand({
  id: 'today',
  label: 'Today',
  description: "Insert today's date",
  icon: 'calendar_today'
})
registerSlashCommand({
  id: 'embed',
  label: 'Embed Block',
  description: 'Insert a block embed',
  icon: 'link'
})
registerSlashCommand({
  id: 'template',
  label: 'Template',
  description: 'Insert a page template at cursor',
  icon: 'content_copy'
})
