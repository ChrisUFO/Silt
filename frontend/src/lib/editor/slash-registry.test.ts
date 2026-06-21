// Slash-command registry tests (#110, #158).
import { describe, expect, it, beforeEach, vi } from 'vitest'

// Mock the grants module so the registry-internal gate can be controlled
// per-test without hitting wailsjs IPC (#158).
vi.mock('../../plugins/grants.svelte', () => ({
  isGranted: vi.fn(() => true),
  initGrants: vi.fn(),
  refreshGrants: vi.fn(),
  resetGrantsForTests: vi.fn(),
  setGrantsForTests: vi.fn()
}))

import {
  registerSlashCommand,
  unregisterSlashCommand,
  unregisterPluginSlashCommands,
  getSlashCommands,
  resetSlashRegistryForTests
} from './slash-registry'
import { isGranted } from '../../plugins/grants.svelte'

describe('slash-command registry (#110, #158)', () => {
  beforeEach(() => {
    resetSlashRegistryForTests()
    vi.mocked(isGranted).mockReturnValue(true)
  })

  it('registers and retrieves a command', () => {
    registerSlashCommand({ id: 'todo', label: 'Task', icon: 'check_box' })
    const cmds = getSlashCommands()
    expect(cmds).toHaveLength(1)
    expect(cmds[0].label).toBe('Task')
  })

  it('built-ins + plugin commands are sorted (built-ins first)', () => {
    registerSlashCommand({ id: 'todo', label: 'Task' })
    registerSlashCommand({ id: 'h1', label: 'Heading' })
    registerSlashCommand({
      id: 'attach-plugin:attach',
      label: 'Attach File',
      pluginID: 'attach-plugin'
    })
    const cmds = getSlashCommands()
    // Built-ins first (alphabetical), then plugin commands.
    expect(cmds[0].id).toBe('h1')
    expect(cmds[1].id).toBe('todo')
    expect(cmds[2].id).toBe('attach-plugin:attach')
    expect(cmds[2].pluginID).toBe('attach-plugin')
  })

  it('unregister removes a single command', () => {
    registerSlashCommand({ id: 'todo', label: 'Task' })
    unregisterSlashCommand('todo')
    expect(getSlashCommands()).toHaveLength(0)
  })

  it('unregisterPluginSlashCommands removes all commands for a plugin', () => {
    registerSlashCommand({ id: 'todo', label: 'Task' })
    registerSlashCommand({ id: 'p:cmd1', label: 'One', pluginID: 'p' })
    registerSlashCommand({ id: 'p:cmd2', label: 'Two', pluginID: 'p' })
    unregisterPluginSlashCommands('p')
    const cmds = getSlashCommands()
    expect(cmds).toHaveLength(1)
    expect(cmds[0].id).toBe('todo')
  })

  it('re-registering the same id replaces the entry', () => {
    registerSlashCommand({ id: 'todo', label: 'Old' })
    registerSlashCommand({ id: 'todo', label: 'New' })
    expect(getSlashCommands()).toHaveLength(1)
    expect(getSlashCommands()[0].label).toBe('New')
  })

  it('rejects a command without id or label', () => {
    expect(() => registerSlashCommand({ id: '', label: 'X' })).toThrow()
    expect(() => registerSlashCommand({ id: 'x', label: '' } as any)).toThrow()
  })

  // --- #158: registry-internal capability gate -------------------------------

  it('refuses plugin commands without editor-schema grant', () => {
    vi.mocked(isGranted).mockReturnValue(false)
    registerSlashCommand({
      id: 'ungranted:cmd',
      label: 'Blocked',
      pluginID: 'ungranted'
    })
    expect(getSlashCommands()).toHaveLength(0)
  })

  it('built-in commands (no pluginID) bypass the gate even when ungranted', () => {
    vi.mocked(isGranted).mockReturnValue(false)
    registerSlashCommand({ id: 'builtin', label: 'Built-in' })
    expect(getSlashCommands()).toHaveLength(1)
    expect(getSlashCommands()[0].id).toBe('builtin')
  })
})

describe('formatting slash commands (#168)', () => {
  beforeEach(() => {
    resetSlashRegistryForTests()
    vi.mocked(isGranted).mockReturnValue(true)
  })

  it('registers all formatting commands with correct metadata', () => {
    // Re-register the built-in formatting commands (module-level registrations
    // are cleared by resetSlashRegistryForTests).
    const formatCmds = [
      { id: 'bold', label: 'Bold', icon: 'format_bold', shortcut: 'Ctrl+B' },
      { id: 'italic', label: 'Italic', icon: 'format_italic', shortcut: 'Ctrl+I' },
      { id: 'underline', label: 'Underline', icon: 'format_underlined', shortcut: 'Ctrl+U' },
      { id: 'strike', label: 'Strikethrough', icon: 'format_strikethrough', shortcut: 'Ctrl+Shift+X' },
      { id: 'code', label: 'Inline code', icon: 'code', shortcut: 'Ctrl+E' },
      { id: 'highlight', label: 'Highlight', icon: 'highlight', shortcut: 'Ctrl+Shift+H' },
      { id: 'subscript', label: 'Subscript', icon: 'subscript', shortcut: 'Ctrl+,' },
      { id: 'superscript', label: 'Superscript', icon: 'superscript', shortcut: 'Ctrl+.' },
      { id: 'link', label: 'Link', icon: 'link', shortcut: 'Ctrl+K' },
      { id: 'clear-formatting', label: 'Clear formatting', icon: 'format_clear', shortcut: 'Ctrl+\\' }
    ]
    for (const cmd of formatCmds) {
      registerSlashCommand(cmd)
    }

    const cmds = getSlashCommands()
    expect(cmds).toHaveLength(formatCmds.length)

    const bold = cmds.find((c) => c.id === 'bold')
    expect(bold?.label).toBe('Bold')
    expect(bold?.shortcut).toBe('Ctrl+B')
    expect(bold?.icon).toBe('format_bold')

    const clear = cmds.find((c) => c.id === 'clear-formatting')
    expect(clear?.label).toBe('Clear formatting')

    const sub = cmds.find((c) => c.id === 'subscript')
    expect(sub?.shortcut).toBe('Ctrl+,')
  })
})
