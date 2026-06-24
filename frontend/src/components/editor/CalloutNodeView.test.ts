import { describe, it, expect } from 'vitest'
import {
  mountNodeViewEditor,
  mkBlock
} from '../../lib/editor/nodeview-test-harness'

describe('CalloutNodeView (#180)', () => {
  it('renders a note variant with the info icon', async () => {
    const { container, cleanup } = await mountNodeViewEditor([
      mkBlock('NOTE', { clean_text: '> [!note] A note' })
    ])
    const callout = container.querySelector('[data-variant="note"]')
    expect(callout).toBeTruthy()
    expect(callout?.getAttribute('role')).toBe('note')
    cleanup()
  })

  it('renders a warning variant with role=alert', async () => {
    const { container, cleanup } = await mountNodeViewEditor([
      mkBlock('NOTE', { clean_text: '> [!warning] Heads up' })
    ])
    const callout = container.querySelector('[data-variant="warning"]')
    expect(callout).toBeTruthy()
    expect(callout?.getAttribute('role')).toBe('alert')
    cleanup()
  })

  it('renders a danger variant with role=alert', async () => {
    const { container, cleanup } = await mountNodeViewEditor([
      mkBlock('NOTE', { clean_text: '> [!danger] Stop' })
    ])
    const callout = container.querySelector('[data-variant="danger"]')
    expect(callout?.getAttribute('role')).toBe('alert')
    cleanup()
  })

  it('renders a tip variant with the lightbulb icon', async () => {
    const { container, cleanup } = await mountNodeViewEditor([
      mkBlock('NOTE', { clean_text: '> [!tip] Hint' })
    ])
    const callout = container.querySelector('[data-variant="tip"]')
    const icon = callout?.querySelector('.material-symbols-outlined')
    expect(icon?.textContent).toBe('lightbulb')
    cleanup()
  })
})
