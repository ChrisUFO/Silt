import { describe, it, expect, beforeEach, vi } from 'vitest'
import { renderMermaid, resetMermaidForTests } from './useMermaid'

// Mock the dynamically-imported mermaid module. vi.mock intercepts both static
// and dynamic imports, so useMermaid's `import('mermaid')` resolves here.
const mocks = vi.hoisted(() => ({
  parse: vi.fn(async (_t: string) => undefined),
  render: vi.fn(async (_id: string, _t: string) => ({
    svg: '<svg>diagram</svg>'
  })),
  initialize: vi.fn()
}))

vi.mock('mermaid', () => ({
  default: {
    initialize: mocks.initialize,
    parse: mocks.parse,
    render: mocks.render
  }
}))

describe('useMermaid (#190)', () => {
  beforeEach(() => {
    resetMermaidForTests()
    mocks.parse.mockClear()
    mocks.render.mockClear()
    mocks.initialize.mockClear()
    mocks.parse.mockResolvedValue(undefined)
    mocks.render.mockResolvedValue({ svg: '<svg>diagram</svg>' })
  })

  it('renders a valid diagram to an SVG', async () => {
    const res = await renderMermaid('graph TD; A-->B', 'default')
    expect(res.error).toBeNull()
    expect(res.svg).toBe('<svg>diagram</svg>')
    expect(mocks.parse).toHaveBeenCalledWith('graph TD; A-->B')
    expect(mocks.render).toHaveBeenCalled()
  })

  it('initializes mermaid once per theme with securityLevel strict', async () => {
    await renderMermaid('graph TD; A-->B', 'dark')
    await renderMermaid('graph TD; A-->B', 'dark')
    expect(mocks.initialize).toHaveBeenCalledTimes(1)
    expect(mocks.initialize).toHaveBeenCalledWith(
      expect.objectContaining({
        securityLevel: 'strict',
        startOnLoad: false,
        theme: 'dark'
      })
    )
    // Switching theme re-initializes (and invalidates the cache).
    await renderMermaid('graph TD; A-->B', 'default')
    expect(mocks.initialize).toHaveBeenCalledTimes(2)
  })

  it('returns a readable error for invalid source instead of throwing', async () => {
    mocks.parse.mockRejectedValueOnce(new Error('Parse error: bad syntax'))
    const res = await renderMermaid('not valid mermaid', 'default')
    expect(res.svg).toBe('')
    expect(res.error).toContain('bad syntax')
  })

  it('memoises by (theme, source) — second call is a cache hit', async () => {
    await renderMermaid('graph TD; A-->B', 'default')
    await renderMermaid('graph TD; A-->B', 'default')
    expect(mocks.render).toHaveBeenCalledTimes(1)
  })

  it('stamps a unique id per call so identical sources do not collide', async () => {
    // Two blocks with identical source must not share an id (invalid DOM) — the
    // cached raw SVG is re-stamped on every call.
    mocks.render.mockResolvedValue({
      svg: '<svg id="silt-mermaid-999"><marker id="silt-mermaid-999-arrow"/></svg>'
    })
    const a = await renderMermaid('unique-src', 'default')
    const b = await renderMermaid('unique-src', 'default')
    const idA = a.svg.match(/silt-mermaid-\d+/)?.[0]
    const idB = b.svg.match(/silt-mermaid-\d+/)?.[0]
    expect(idA).toBeTruthy()
    expect(idB).toBeTruthy()
    expect(idA).not.toBe(idB)
    // The stale cached id (999) is gone; internal refs share the new id.
    expect(a.svg).not.toContain('999')
    expect(a.svg.match(/silt-mermaid-\d+/g)?.every((x) => x === idA)).toBe(true)
  })

  it('renders nothing for empty source', async () => {
    const res = await renderMermaid('   ', 'default')
    expect(res.svg).toBe('')
    expect(res.error).toBeNull()
    expect(mocks.render).not.toHaveBeenCalled()
  })
})
