// Component coverage for the export/import archive modal (#143). The IPC
// layer is mocked via vi.hoisted + vi.mock over the wailsjs binding module
// (the canonical pattern — see AppearanceTab.test.ts / VaultActionModal.test.ts).
// The vault:archive:progress event is mocked via the wailsjs runtime module.
// No real IPC in a test.

import { describe, expect, it, afterEach, vi } from 'vitest'
import { tick } from 'svelte'
import {
  render,
  screen,
  cleanup,
  fireEvent,
  waitFor
} from '@testing-library/svelte'
import VaultArchiveModal from './VaultArchiveModal.svelte'

const mocks = vi.hoisted(() => ({
  PickVaultExportPath: vi.fn(),
  ExportVault: vi.fn(),
  PickVaultArchive: vi.fn(),
  PickVaultDestination: vi.fn(),
  ImportVault: vi.fn()
}))

// Capture the progress handler so a test can emit synthetic events.
const runtimeMocks = vi.hoisted(() => {
  let progressHandler: ((p: unknown) => void) | null = null
  return {
    EventsOn: vi.fn((_name: string, cb: (p: unknown) => void) => {
      progressHandler = cb
      return () => {
        progressHandler = null
      }
    }),
    emit: (p: unknown) => progressHandler?.(p)
  }
})

vi.mock('../../../wailsjs/go/main/App.js', () => mocks)
vi.mock('../../../wailsjs/runtime/runtime.js', () => ({
  EventsOn: runtimeMocks.EventsOn,
  EventsOff: vi.fn(),
  EventsEmit: vi.fn()
}))

describe('VaultArchiveModal', () => {
  afterEach(() => {
    cleanup()
    vi.clearAllMocks()
  })

  it('export mode: renders a dialog with the primary action disabled until a destination is chosen', async () => {
    render(VaultArchiveModal, {
      mode: 'export',
      currentPath: '/old/vault',
      onClose: () => {}
    })
    await tick()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    const primary = screen.getByRole('button', { name: 'Export vault' })
    expect(primary).toBeDisabled()
  })

  it('export mode: choosing a file then committing calls ExportVault and shows success', async () => {
    mocks.PickVaultExportPath.mockResolvedValue('/backups/vault.silt-vault')
    mocks.ExportVault.mockResolvedValue({
      files_archived: 12,
      bytes_archived: 4096,
      page_file_count: 5,
      skipped_index: true,
      skipped_symlinks: 0
    })
    render(VaultArchiveModal, {
      mode: 'export',
      currentPath: '/old/vault',
      onClose: () => {}
    })
    await tick()
    await fireEvent.click(screen.getByRole('button', { name: 'Choose…' }))
    await tick()
    const primary = await screen.findByRole('button', { name: 'Export vault' })
    await waitFor(() => expect(primary).not.toBeDisabled())
    await fireEvent.click(primary)
    await waitFor(() => expect(mocks.ExportVault).toHaveBeenCalledWith('/backups/vault.silt-vault'))
    // Success card surfaces the file count + path.
    expect(await screen.findByText(/Archived 12 files/)).toBeInTheDocument()
    expect(screen.getByText('/backups/vault.silt-vault')).toBeInTheDocument()
  })

  it('export mode: an error from ExportVault renders in an alert', async () => {
    mocks.PickVaultExportPath.mockResolvedValue('/x/vault.silt-vault')
    mocks.ExportVault.mockRejectedValue(new Error('disk full'))
    render(VaultArchiveModal, {
      mode: 'export',
      currentPath: '/old/vault',
      onClose: () => {}
    })
    await tick()
    await fireEvent.click(screen.getByRole('button', { name: 'Choose…' }))
    await tick()
    const primary = await screen.findByRole('button', { name: 'Export vault' })
    await waitFor(() => expect(primary).not.toBeDisabled())
    await fireEvent.click(primary)
    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toContain('disk full')
  })

  it('import mode: primary disabled until both archive and destination are chosen', async () => {
    render(VaultArchiveModal, {
      mode: 'import',
      currentPath: '/old/vault',
      onClose: () => {}
    })
    await tick()
    const primary = screen.getByRole('button', { name: 'Import vault' })
    expect(primary).toBeDisabled()
    // Choose archive only — still disabled.
    mocks.PickVaultArchive.mockResolvedValue('/a/vault.silt-vault')
    const chooseBtns = screen.getAllByRole('button', { name: 'Choose…' })
    await fireEvent.click(chooseBtns[0])
    await tick()
    expect(primary).toBeDisabled()
    // Now choose destination — enabled.
    mocks.PickVaultDestination.mockResolvedValue('/new/empty')
    await fireEvent.click(chooseBtns[1])
    await tick()
    await waitFor(() => expect(primary).not.toBeDisabled())
  })

  it('import mode: committing calls ImportVault with archive + destination', async () => {
    mocks.PickVaultArchive.mockResolvedValue('/a/vault.silt-vault')
    mocks.PickVaultDestination.mockResolvedValue('/new/empty')
    mocks.ImportVault.mockResolvedValue({
      files_extracted: 7,
      bytes_extracted: 2048,
      page_file_count: 3
    })
    render(VaultArchiveModal, {
      mode: 'import',
      currentPath: '/old/vault',
      onClose: () => {}
    })
    await tick()
    const chooseBtns = screen.getAllByRole('button', { name: 'Choose…' })
    await fireEvent.click(chooseBtns[0])
    await fireEvent.click(chooseBtns[1])
    await tick()
    const primary = await screen.findByRole('button', { name: 'Import vault' })
    await waitFor(() => expect(primary).not.toBeDisabled())
    await fireEvent.click(primary)
    await waitFor(() =>
      expect(mocks.ImportVault).toHaveBeenCalledWith('/a/vault.silt-vault', '/new/empty')
    )
    expect(await screen.findByText(/Imported 7 files/)).toBeInTheDocument()
  })

  it('renders a determinate progress bar when a vault:archive:progress event arrives', async () => {
    mocks.PickVaultExportPath.mockResolvedValue('/x/vault.silt-vault')
    // Hold the export promise so we can assert the in-flight progress UI.
    let resolveExport!: (v: unknown) => void
    mocks.ExportVault.mockImplementation(
      () => new Promise((resolve) => (resolveExport = resolve))
    )
    render(VaultArchiveModal, {
      mode: 'export',
      currentPath: '/old/vault',
      onClose: () => {}
    })
    await tick()
    await fireEvent.click(screen.getByRole('button', { name: 'Choose…' }))
    await tick()
    await fireEvent.click(await screen.findByRole('button', { name: 'Export vault' }))
    // Emit a synthetic progress event.
    runtimeMocks.emit({ phase: 'export', current: 3, total: 10 })
    await tick()
    const bar = await screen.findByRole('progressbar')
    expect(bar.getAttribute('aria-valuenow')).toBe('30')
    expect(screen.getByText(/Archiving 3 of 10 files/)).toBeInTheDocument()
    resolveExport({
      files_archived: 10,
      bytes_archived: 100,
      page_file_count: 4,
      skipped_index: true,
      skipped_symlinks: 0
    })
    await tick()
  })

  it('Cancel invokes onClose when not busy', async () => {
    const onClose = vi.fn()
    render(VaultArchiveModal, {
      mode: 'export',
      currentPath: '/old/vault',
      onClose
    })
    await tick()
    await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(onClose).toHaveBeenCalledTimes(1)
  })
})
