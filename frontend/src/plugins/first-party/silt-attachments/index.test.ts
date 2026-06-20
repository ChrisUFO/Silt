// silt-attachments plugin test (#101). Verifies the plugin module exports the
// correct shape (manifest, component, onVaultOpen) and that the /attach slash
// command is registered with the expected metadata. The handleAttach flow
// (picker → addAttachment → insert) is tested via the mock SDK methods.
import { describe, expect, it, beforeEach, vi } from 'vitest'

const mocks = vi.hoisted(() => ({
  pickOpenFile: vi.fn(),
  addAttachment: vi.fn(),
  registerSlashCommand: vi.fn(() => () => {}),
  activeNotebook: 'Work'
}))

vi.mock('../../../wailsjs/go/main/App.js', () => ({
  AddAttachment: mocks.addAttachment,
  OpenAttachment: vi.fn(),
  DeleteAttachment: vi.fn()
}))

// The plugin module imports Attachments.svelte; stub it so the Svelte compiler
// doesn't interfere with the unit test.
vi.mock('./Attachments.svelte', () => ({
  default: () => {}
}))

import plugin from './index'

describe('silt-attachments plugin (#101)', () => {
  beforeEach(() => {
    mocks.pickOpenFile.mockReset()
    mocks.addAttachment.mockReset()
    mocks.registerSlashCommand.mockReset()
    mocks.registerSlashCommand.mockReturnValue(() => {})
    mocks.addAttachment.mockResolvedValue('attachments/test.pdf')
  })

  it('exports a valid manifest', () => {
    expect(plugin.manifest.id).toBe('silt-attachments')
    expect(plugin.manifest.name).toBe('Attachments')
    expect(plugin.manifest.version).toBeTruthy()
    expect(plugin.component).toBeTruthy()
  })

  it('registers the /attach slash command on vault open', () => {
    const ctx = {
      activeNotebook: mocks.activeNotebook,
      pickOpenFile: mocks.pickOpenFile,
      addAttachment: mocks.addAttachment,
      registerSlashCommand: mocks.registerSlashCommand
    } as any
    plugin.onVaultOpen?.(ctx)
    expect(mocks.registerSlashCommand).toHaveBeenCalledTimes(1)
    const cmd = (mocks.registerSlashCommand.mock.calls as any[])[0]?.[0] as any
    expect(cmd.id).toBe('attach')
    expect(cmd.label).toBe('Attach File')
    expect(typeof cmd.onSelect).toBe('function')
  })

  it('the onSelect handler calls the attachment flow when invoked', async () => {
    mocks.pickOpenFile.mockResolvedValue('/fake/path/report.pdf')
    mocks.addAttachment.mockResolvedValue('attachments/report.pdf')
    const fakeEditor = {
      commands: {
        insertContent: vi.fn(),
        focus: vi.fn()
      },
      isDestroyed: false,
      state: { selection: { to: 0 } }
    }
    const ctx = {
      activeNotebook: 'Work',
      pickOpenFile: mocks.pickOpenFile,
      addAttachment: mocks.addAttachment,
      registerSlashCommand: mocks.registerSlashCommand
    } as any
    plugin.onVaultOpen?.(ctx)
    const cmd = (mocks.registerSlashCommand.mock.calls as any[])[0]?.[0] as any
    // onSelect fires handleAttach asynchronously; flush the microtask queue.
    cmd.onSelect(fakeEditor, 0)
    await new Promise((r) => setTimeout(r, 50))
    expect(mocks.pickOpenFile).toHaveBeenCalledWith('*')
    expect(mocks.addAttachment).toHaveBeenCalledWith(
      '/fake/path/report.pdf',
      'Work'
    )
    expect(fakeEditor.commands.insertContent).toHaveBeenCalledWith(
      expect.objectContaining({
        type: 'embedBlock',
        attrs: expect.objectContaining({
          embedType: 'attachment',
          src: 'attachments/report.pdf',
          openable: true,
          pluginID: 'silt-attachments'
        })
      })
    )
  })

  it('does nothing when the picker is cancelled', async () => {
    mocks.pickOpenFile.mockResolvedValue('')
    const fakeEditor = {
      commands: { insertContent: vi.fn() },
      isDestroyed: false,
      state: { selection: { to: 0 } }
    }
    const ctx = {
      activeNotebook: 'Work',
      pickOpenFile: mocks.pickOpenFile,
      addAttachment: mocks.addAttachment,
      registerSlashCommand: mocks.registerSlashCommand
    } as any
    plugin.onVaultOpen?.(ctx)
    const cmd = (mocks.registerSlashCommand.mock.calls as any[])[0]?.[0] as any
    await cmd.onSelect(fakeEditor, 0)
    expect(mocks.addAttachment).not.toHaveBeenCalled()
    expect(fakeEditor.commands.insertContent).not.toHaveBeenCalled()
  })
})
