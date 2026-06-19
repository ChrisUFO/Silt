// Slash-command registry tests (#110).
import { describe, expect, it, beforeEach } from 'vitest'
import {
  registerSlashCommand,
  unregisterSlashCommand,
  unregisterPluginSlashCommands,
  getSlashCommands,
  resetSlashRegistryForTests
} from './slash-registry'

describe('slash-command registry (#110)', () => {
  beforeEach(() => {
    resetSlashRegistryForTests()
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
})
