// Slash-command registry (#110). Refactors the previously hardcoded
// CommandPalette command list into an extensible registry so plugins can
// contribute `/`-menu entries alongside the built-ins.
//
// The registry is module-scoped (a Map<id, SlashCommand>). Built-ins register
// at boot; plugins register via ctx.registerSlashCommand. CommandPalette reads
// the union (grouped: Built-ins / Plugins). When a command is selected, the
// editor calls its handler with the live editor + cursor position.
//
// Capability gate (#158): plugin commands (with a pluginID) are checked
// against the trusted Go-provided grant cache before registration. Built-in
// commands (no pluginID) bypass the gate. This closes the advisory-gap: a
// plugin importing registerSlashCommand directly still hits the gate.

import { isGranted } from '../../plugins/grants.svelte'

export interface SlashCommand {
  /** Unique id. Plugin commands are namespaced as `<pluginID>:<id>`. */
  id: string
  /** Display label in the menu. */
  label: string
  description?: string
  icon?: string
  /** Optional keyboard shortcut hint shown right-aligned (built-ins only). */
  shortcut?: string
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
 *
 * Capability gate (#158): if the command has a pluginID, the registry checks
 * isGranted(pluginID, 'editor-schema') from the trusted Go grant cache. An
 * ungranted plugin's command is silently dropped (warn). Built-in commands
 * (no pluginID) bypass the gate.
 */
export function registerSlashCommand(cmd: SlashCommand): void {
  if (!cmd.id || !cmd.label) {
    throw new Error('SlashCommand requires id + label')
  }
  if (cmd.pluginID && !isGranted(cmd.pluginID, 'editor-schema')) {
    // eslint-disable-next-line no-console
    console.warn(
      `[silt] plugin ${cmd.pluginID} cannot register slash commands without the editor-schema capability`
    )
    return
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
  icon: 'check_box',
  shortcut: '[]'
})
registerSlashCommand({
  id: 'h1',
  label: 'Heading 1',
  description: 'Large section header',
  icon: 'format_size',
  shortcut: '#'
})
registerSlashCommand({
  id: 'today',
  label: 'Today',
  description: "Insert today's date",
  icon: 'calendar_today',
  shortcut: 'D'
})
registerSlashCommand({
  id: 'embed',
  label: 'Embed Block',
  description: 'Insert a block embed',
  icon: 'link',
  shortcut: 'E'
})
registerSlashCommand({
  id: 'template',
  label: 'Template',
  description: 'Insert a page template at cursor',
  icon: 'content_copy',
  shortcut: 'T'
})

// --- Inline formatting commands (#168) ------------------------------------
// Metadata-only built-ins; the editor dispatches them by id via
// handleSlashSelect. Each toggles its mark on the current selection.
registerSlashCommand({
  id: 'bold',
  label: 'Bold',
  description: 'Make the selection bold',
  icon: 'format_bold',
  shortcut: 'Ctrl+B'
})
registerSlashCommand({
  id: 'italic',
  label: 'Italic',
  description: 'Make the selection italic',
  icon: 'format_italic',
  shortcut: 'Ctrl+I'
})
registerSlashCommand({
  id: 'underline',
  label: 'Underline',
  description: 'Underline the selection',
  icon: 'format_underlined',
  shortcut: 'Ctrl+U'
})
registerSlashCommand({
  id: 'strike',
  label: 'Strikethrough',
  description: 'Cross out the selection',
  icon: 'format_strikethrough',
  shortcut: 'Ctrl+Shift+X'
})
registerSlashCommand({
  id: 'code',
  label: 'Inline code',
  description: 'Format as inline code',
  icon: 'code',
  shortcut: 'Ctrl+E'
})
registerSlashCommand({
  id: 'highlight',
  label: 'Highlight',
  description: 'Highlight the selection',
  icon: 'highlight',
  shortcut: 'Ctrl+Shift+H'
})
registerSlashCommand({
  id: 'subscript',
  label: 'Subscript',
  description: 'Lower the selection below the line',
  icon: 'subscript',
  shortcut: 'Ctrl+,'
})
registerSlashCommand({
  id: 'superscript',
  label: 'Superscript',
  description: 'Raise the selection above the line',
  icon: 'superscript',
  shortcut: 'Ctrl+.'
})
registerSlashCommand({
  id: 'link',
  label: 'Link',
  description: 'Add a hyperlink to the selection',
  icon: 'link',
  shortcut: 'Ctrl+K'
})
registerSlashCommand({
  id: 'clear-formatting',
  label: 'Clear formatting',
  description: 'Remove all formatting from the selection',
  icon: 'format_clear',
  shortcut: 'Ctrl+\\'
})

// --- Heading / block-type commands (#169) ---------------------------------
registerSlashCommand({
  id: 'h2',
  label: 'Heading 2',
  description: 'Convert the block to an H2',
  icon: 'format_size',
  shortcut: 'Ctrl+Alt+2'
})
registerSlashCommand({
  id: 'h3',
  label: 'Heading 3',
  description: 'Convert the block to an H3',
  icon: 'format_size',
  shortcut: 'Ctrl+Alt+3'
})
registerSlashCommand({
  id: 'note',
  label: 'Plain note',
  description: 'Convert the block to a plain note (strip header / task)',
  icon: 'notes',
  shortcut: 'Ctrl+Alt+0'
})
registerSlashCommand({
  id: 'task',
  label: 'Task',
  description: 'Convert the block to a task',
  icon: 'check_box',
  shortcut: 'Ctrl+Alt+4'
})

// --- Text alignment commands (#173) ---------------------------------------
registerSlashCommand({
  id: 'align-left',
  label: 'Align left',
  description: 'Align the current block to the left',
  icon: 'format_align_left',
  shortcut: 'Ctrl+Shift+L'
})
registerSlashCommand({
  id: 'align-center',
  label: 'Align center',
  description: 'Center the current block',
  icon: 'format_align_center',
  shortcut: 'Ctrl+Shift+E'
})
registerSlashCommand({
  id: 'align-right',
  label: 'Align right',
  description: 'Align the current block to the right',
  icon: 'format_align_right',
  shortcut: 'Ctrl+Shift+R'
})
registerSlashCommand({
  id: 'align-justify',
  label: 'Align justify',
  description: 'Justify the current block',
  icon: 'format_align_justify',
  shortcut: 'Ctrl+Shift+J'
})

// --- Quote / blockquote (#188) --------------------------------------------
registerSlashCommand({
  id: 'quote',
  label: 'Quote',
  description: 'Toggle a blockquote on the current block',
  icon: 'format_quote',
  shortcut: 'Ctrl+Shift+9'
})

// --- Callouts / admonitions (#180) ----------------------------------------
// `/callout` opens a variant picker; the per-variant commands insert directly.
registerSlashCommand({
  id: 'callout',
  label: 'Callout',
  description: 'Insert a callout (pick a variant)',
  icon: 'info'
})
registerSlashCommand({
  id: 'callout-note',
  label: 'Callout: Note',
  description: 'Insert a note callout',
  icon: 'info'
})
registerSlashCommand({
  id: 'callout-info',
  label: 'Callout: Info',
  description: 'Insert an info callout',
  icon: 'campaign'
})
registerSlashCommand({
  id: 'callout-tip',
  label: 'Callout: Tip',
  description: 'Insert a tip callout',
  icon: 'lightbulb'
})
registerSlashCommand({
  id: 'callout-warning',
  label: 'Callout: Warning',
  description: 'Insert a warning callout',
  icon: 'warning'
})
registerSlashCommand({
  id: 'callout-danger',
  label: 'Callout: Danger',
  description: 'Insert a danger callout',
  icon: 'error'
})
registerSlashCommand({
  id: 'callout-success',
  label: 'Callout: Success',
  description: 'Insert a success callout',
  icon: 'check_circle'
})

// --- Code blocks (#189) ---------------------------------------------------
registerSlashCommand({
  id: 'code-block',
  label: 'Code block',
  description: 'Insert a fenced code block with syntax highlighting',
  icon: 'code_blocks'
})

// --- Block math (#191) ----------------------------------------------------
registerSlashCommand({
  id: 'math',
  label: 'Math equation',
  description: 'Insert a centered LaTeX equation ($$…$$) rendered with KaTeX',
  icon: 'functions'
})

// --- Foldable details (#183) ----------------------------------------------
registerSlashCommand({
  id: 'details',
  label: 'Foldable section',
  description: 'Insert a collapsible <details> section',
  icon: 'unfold_more',
  shortcut: 'Ctrl+Shift+.'
})

// --- GFM tables (#172) ----------------------------------------------------
registerSlashCommand({
  id: 'table',
  label: 'Table',
  description: 'Insert a 3×3 table',
  icon: 'table_view'
})
registerSlashCommand({
  id: 'table-5x4',
  label: 'Table (5×4)',
  description: 'Insert a 5-row, 4-column table',
  icon: 'grid_on'
})
registerSlashCommand({
  id: 'table-custom',
  label: 'Custom table…',
  description: 'Insert a table with custom dimensions',
  icon: 'grid_view'
})

// --- Color commands (#170) ------------------------------------------------
registerSlashCommand({
  id: 'text-color',
  label: 'Text color',
  description: 'Pick a text color for the selection',
  icon: 'palette'
})
registerSlashCommand({
  id: 'background-color',
  label: 'Background color',
  description: 'Pick a background color for the selection',
  icon: 'format_color_fill'
})
registerSlashCommand({
  id: 'remove-color',
  label: 'Remove text color',
  description: 'Remove the text color from the selection',
  icon: 'format_color_reset'
})
registerSlashCommand({
  id: 'remove-background',
  label: 'Remove background color',
  description: 'Remove the background color from the selection',
  icon: 'format_color_reset'
})
