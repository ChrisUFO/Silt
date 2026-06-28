import { describe, it, expect, beforeEach, vi } from 'vitest'
import { renderKatex, resetKatexForTests } from './useKatex'

// Mock the dynamically-imported katex module + its CSS side-effect import.
const renderToString = vi.fn(
  (_tex: string, _opts: Record<string, unknown>) =>
    '<span class="katex">x</span>'
)

vi.mock('katex', () => ({
  default: { renderToString }
}))
// The CSS side-effect import resolves to an empty module under test.
vi.mock('katex/dist/katex.min.css', () => ({}))

describe('useKatex (#191)', () => {
  beforeEach(() => {
    resetKatexForTests()
    renderToString.mockClear()
    renderToString.mockReturnValue('<span class="katex">x</span>')
  })

  it('renders valid LaTeX to KaTeX HTML', async () => {
    const res = await renderKatex('a^2', false)
    expect(res.error).toBeNull()
    expect(res.html).toContain('katex')
    expect(renderToString).toHaveBeenCalledWith(
      'a^2',
      expect.objectContaining({ displayMode: false, throwOnError: false })
    )
  })

  it('passes displayMode through for block math', async () => {
    await renderKatex('\\sum x', true)
    expect(renderToString).toHaveBeenCalledWith(
      '\\sum x',
      expect.objectContaining({ displayMode: true })
    )
  })

  it('returns nothing for empty latex', async () => {
    const res = await renderKatex('', false)
    expect(res.html).toBe('')
    expect(renderToString).not.toHaveBeenCalled()
  })

  it('never throws — a catastrophic failure becomes an error string', async () => {
    renderToString.mockImplementation(() => {
      throw new Error('boom')
    })
    const res = await renderKatex('a', false)
    expect(res.html).toBe('')
    expect(res.error).toBe('boom')
  })
})
