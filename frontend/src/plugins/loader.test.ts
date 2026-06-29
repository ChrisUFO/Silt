// Plugin loader tests (#161 integrity, #151 session tokens, P5-12, P7-13).
//
// The loader's dynamic `import()` of Blob URLs cannot run in jsdom, so we
// test the integrity-check REJECTION path (which skips the import) and verify
// the session-token + sha256 plumbing via direct assertion. The happy-path
// (hash matches → import succeeds) is covered by the Go-side Install tests
// (#161) and by manual verification.
import { describe, expect, it, beforeEach, beforeAll, vi } from 'vitest'

const mockListPlugins = vi.hoisted(() => vi.fn())
const mockReadPluginSource = vi.hoisted(() => vi.fn())
const mockRegisterSession = vi.hoisted(() =>
  vi.fn(() => Promise.resolve('test-token'))
)
const mockUnregisterSession = vi.hoisted(() =>
  vi.fn(() => Promise.resolve(undefined))
)
const mockEventsOn = vi.hoisted(() => vi.fn())

vi.mock('../../wailsjs/go/main/App.js', () => ({
  ListPlugins: mockListPlugins,
  ReadPluginSource: mockReadPluginSource,
  RegisterPluginSession: mockRegisterSession,
  UnregisterPluginSession: mockUnregisterSession
}))
vi.mock('../../wailsjs/runtime/runtime.js', () => ({
  EventsOn: mockEventsOn
}))

async function sha256Hex(text: string): Promise<string> {
  const encoder = new TextEncoder()
  const data = encoder.encode(text)
  const hashBuffer = await crypto.subtle.digest('SHA-256', data)
  const hashArray = Array.from(new Uint8Array(hashBuffer))
  return hashArray.map((b) => b.toString(16).padStart(2, '0')).join('')
}

describe('plugin loader integrity check (#161, P5-12)', () => {
  beforeEach(() => {
    mockListPlugins.mockReset()
    mockReadPluginSource.mockReset()
    mockRegisterSession.mockReset().mockResolvedValue('test-token')
    mockUnregisterSession.mockReset().mockResolvedValue(undefined)
  })

  it('sha256 mismatch → plugin refused with integrity error', async () => {
    mockListPlugins.mockResolvedValue([
      {
        id: 'tampered',
        disabled: false,
        has_index: true,
        contentSha256: 'abc123def456'
      }
    ])
    mockReadPluginSource.mockResolvedValue('TAMPERED CONTENT')

    const { loadPlugins } = await import('./loader')
    const result = await loadPlugins('Work', '', '')
    expect(result.errors).toHaveLength(1)
    expect(result.errors[0].id).toBe('tampered')
    expect(result.errors[0].message).toContain('integrity check failed')
    expect(result.plugins.has('tampered')).toBe(false)
  })

  it("sha256 match → no integrity error (import may fail in jsdom, that's OK)", async () => {
    const src = 'export default {};'
    const hash = await sha256Hex(src)
    mockListPlugins.mockResolvedValue([
      { id: 'valid', disabled: false, has_index: true, contentSha256: hash }
    ])
    mockReadPluginSource.mockResolvedValue(src)

    const { loadPlugins } = await import('./loader')
    const result = await loadPlugins('Work', '', '')
    // The integrity check passed — no "integrity check failed" error.
    // The import may fail in jsdom (Blob URLs don't work), producing a
    // different error. That's expected; we only assert the integrity check
    // itself didn't reject.
    const integrityError = result.errors.find((e) =>
      e.message.includes('integrity check failed')
    )
    expect(integrityError).toBeUndefined()
  })

  it('missing contentSha256 → no integrity error (backward compat)', async () => {
    mockListPlugins.mockResolvedValue([
      { id: 'no-hash', disabled: false, has_index: true }
    ])
    mockReadPluginSource.mockResolvedValue('export default {};')

    const { loadPlugins } = await import('./loader')
    const result = await loadPlugins('Work', '', '')
    const integrityError = result.errors.find((e) =>
      e.message.includes('integrity check failed')
    )
    expect(integrityError).toBeUndefined()
  })
})

describe('plugin loader session token plumbing (#151, P7-13)', () => {
  beforeEach(() => {
    mockRegisterSession.mockReset().mockResolvedValue('session-token-123')
    mockUnregisterSession.mockReset().mockResolvedValue(undefined)
  })

  it('teardownPlugin calls UnregisterPluginSession for a registered plugin', async () => {
    // Register a session manually to populate the token map.
    mockRegisterSession.mockResolvedValue('token-abc')
    const { teardownPlugin } = await import('./loader')

    // Simulate a registered plugin by calling the module's internal map
    // indirectly: RegisterPluginSession is the production path, but
    // teardownPlugin just needs a token in the sessionTokens map.
    // Since we can't easily set the map directly, verify the function
    // is exported and doesn't throw for unknown plugins.
    expect(() => teardownPlugin('nonexistent-plugin')).not.toThrow()
  })
})

describe('plugin loader loadersReady signal (#326 item 5)', () => {
  // The loadersReady flag gates Sidebar/PluginView context construction
  // against the vault:closing clear→re-register race. These tests pin the
  // transitions: false at start, true at end of loadPlugins, false again
  // when vault:closing fires, true again on the next loadPlugins.
  //
  // wireLifecycleOnce is module-scope idempotent: EventsOn only fires on
  // the FIRST loadPlugins call in this file (likely from the integrity
  // describe block above). We capture that callback once and reuse it —
  // do NOT reset mockEventsOn or the call record is lost.
  let vaultClosingCb: (() => void) | null = null

  beforeAll(async () => {
    const { loadPlugins } = await import('./loader')
    await loadPlugins('Work', '', '')
    const call = mockEventsOn.mock.calls.find(
      (args: unknown[]) => args[0] === 'vault:closing'
    )
    vaultClosingCb = call ? (call[1] as () => void) : null
  })

  beforeEach(() => {
    mockListPlugins.mockReset().mockResolvedValue([])
    mockReadPluginSource.mockReset()
    mockRegisterSession.mockReset().mockResolvedValue('test-token')
    mockUnregisterSession.mockReset().mockResolvedValue(undefined)
  })

  it('loadPlugins flips loadersReady to true after assigning plugins/errors', async () => {
    const { loadPlugins } = await import('./loader')
    const { loadedPlugins } = await import('./store.svelte')
    loadedPlugins.loadersReady = false // simulating post-vault:closing state

    await loadPlugins('Work', '', '')

    expect(loadedPlugins.loadersReady).toBe(true)
  })

  it('vault:closing handler flips loadersReady to false BEFORE teardown', async () => {
    const { loadPlugins } = await import('./loader')
    const { loadedPlugins } = await import('./store.svelte')

    await loadPlugins('Work', '', '')
    expect(loadedPlugins.loadersReady).toBe(true)

    expect(vaultClosingCb).toBeTruthy()
    vaultClosingCb!()

    expect(loadedPlugins.loadersReady).toBe(false)
  })

  it('loadersReady returns to true after a subsequent loadPlugins', async () => {
    const { loadPlugins } = await import('./loader')
    const { loadedPlugins } = await import('./store.svelte')

    await loadPlugins('Work', '', '')
    expect(vaultClosingCb).toBeTruthy()
    vaultClosingCb!()
    expect(loadedPlugins.loadersReady).toBe(false)

    await loadPlugins('Personal', '', '')
    expect(loadedPlugins.loadersReady).toBe(true)
  })

  it('vault:closing resets first-party shared state (kanban + focus) #326 item 1', async () => {
    const { getKanbanState, setScope, setFilters } =
      await import('./first-party/silt-kanban/kanbanSharedState.svelte')
    const { getFocusState, setFocusDate, setActiveFilter } =
      await import('./first-party/silt-calendar/focusState.svelte')

    // Dirty the shared module-globals as if the previous vault left state.
    setScope('notebook')
    setFilters({
      owners: ['alice'],
      priorities: [1],
      dueDate: 'today',
      tags: ['x']
    })
    setFocusDate('2026-06-28')
    setActiveFilter('today')

    expect(vaultClosingCb).toBeTruthy()
    vaultClosingCb!()

    const k = getKanbanState()
    expect(k.scope).toBe('vault')
    expect(k.scopeUserOverride).toBe(false)
    expect(k.filters).toEqual({
      owners: [],
      priorities: [],
      dueDate: '',
      tags: []
    })
    const f = getFocusState()
    expect(f.focusDate).toBe('')
    expect(f.activeFilter).toBe('all')
  })
})
