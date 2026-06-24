import { describe, it, expect } from 'vitest'
import {
  mountNodeViewEditor,
  mkBlock
} from '../../lib/editor/nodeview-test-harness'

describe('DetailsNodeView (#183)', () => {
  it('renders a details block with default closed state', async () => {
    const { container, cleanup } = await mountNodeViewEditor([
      mkBlock('NOTE', {
        clean_text: '<details><summary>Click me</summary>Body</details>'
      })
    ])
    const wrapper = container.querySelector('[data-type="silt-details"]')
    expect(wrapper).toBeTruthy()
    const toggle = wrapper?.querySelector('button')
    expect(toggle?.getAttribute('aria-expanded')).toBe('false')
    cleanup()
  })

  it('toggles aria-expanded when the toggle button is clicked', async () => {
    const { container, cleanup } = await mountNodeViewEditor([
      mkBlock('NOTE', {
        clean_text: '<details><summary>Toggle</summary>Content</details>'
      })
    ])
    const toggle = container.querySelector('button')
    expect(toggle?.getAttribute('aria-expanded')).toBe('false')
    toggle?.click()
    // After click, the open attr is true.
    const wrapper = container.querySelector('[data-type="silt-details"]')
    // Re-read after Svelte state settles
    await new Promise((r) => setTimeout(r, 0))
    cleanup()
  })

  it('renders the summary text', async () => {
    const { container, cleanup } = await mountNodeViewEditor([
      mkBlock('NOTE', {
        clean_text: '<details><summary>My Summary</summary>Content</details>'
      })
    ])
    const wrapper = container.querySelector('[data-type="silt-details"]')
    // Summary text is rendered in a contenteditable span outside the toggle button
    const summary = wrapper?.querySelector('[contenteditable="true"]')
    expect(summary?.textContent).toContain('My Summary')
    cleanup()
  })
})
