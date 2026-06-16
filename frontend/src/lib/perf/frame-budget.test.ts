import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest'

// Helper: import the module with a controlled URL search string. The
// `PERF_ENABLED` constant is captured at module load time, so each call
// resets the module registry and dynamically imports it.
async function loadWithSearch(search: string) {
  Object.defineProperty(window, 'location', {
    value: { search },
    writable: true,
    configurable: true
  })
  vi.resetModules()
  return await import('./frame-budget')
}

describe('measureFrameBudget (#21)', () => {
  let consoleSpy: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    consoleSpy = vi.spyOn(console, 'log').mockImplementation(() => {})
  })

  afterEach(() => {
    consoleSpy.mockRestore()
    vi.restoreAllMocks()
  })

  it('is a zero-cost pass-through when ?perf=1 is absent', async () => {
    const { measureFrameBudget } = await loadWithSearch('')
    const markSpy = vi.spyOn(performance, 'mark')
    const measureSpy = vi.spyOn(performance, 'measure')

    const result = measureFrameBudget('noop', () => 42)

    expect(result).toBe(42)
    expect(markSpy).not.toHaveBeenCalled()
    expect(measureSpy).not.toHaveBeenCalled()
  })

  it('records marks/measures and clears them after the rAF callback when ?perf=1 is set', async () => {
    const { measureFrameBudget } = await loadWithSearch('?perf=1')

    const clearMarksSpy = vi.spyOn(performance, 'clearMarks')
    const clearMeasuresSpy = vi.spyOn(performance, 'clearMeasures')

    const result = measureFrameBudget('kanban-drop', () => 7)
    expect(result).toBe(7)

    // Marks/measures exist immediately after the synchronous call.
    const marks = performance.getEntriesByType('mark')
    const measures = performance.getEntriesByType('measure')
    expect(marks.some((m) => m.name === 'perf:kanban-drop:start')).toBe(true)
    expect(marks.some((m) => m.name === 'perf:kanban-drop:end')).toBe(true)
    expect(measures.some((m) => m.name === 'perf:kanban-drop')).toBe(true)

    // rAF is async — flush it.
    await new Promise((r) => requestAnimationFrame(() => r(undefined)))

    // After the rAF fires, all three entries must be cleared.
    expect(clearMarksSpy).toHaveBeenCalledWith('perf:kanban-drop:start')
    expect(clearMarksSpy).toHaveBeenCalledWith('perf:kanban-drop:end')
    expect(clearMeasuresSpy).toHaveBeenCalledWith('perf:kanban-drop')

    const marksAfter = performance.getEntriesByType('mark')
    const measuresAfter = performance.getEntriesByType('measure')
    expect(marksAfter.some((m) => m.name === 'perf:kanban-drop:start')).toBe(
      false
    )
    expect(marksAfter.some((m) => m.name === 'perf:kanban-drop:end')).toBe(
      false
    )
    expect(measuresAfter.some((m) => m.name === 'perf:kanban-drop')).toBe(false)
  })

  it('logs the duration with the ✓ flag when under the 16ms budget', async () => {
    const { measureFrameBudget } = await loadWithSearch('?perf=1')

    measureFrameBudget('cheap-op', () => null)
    await new Promise((r) => requestAnimationFrame(() => r(undefined)))

    expect(consoleSpy).toHaveBeenCalledTimes(1)
    const msg = consoleSpy.mock.calls[0].join(' ')
    expect(msg).toContain('[perf]')
    expect(msg).toContain('cheap-op')
    expect(msg).toContain('✓')
  })
})
