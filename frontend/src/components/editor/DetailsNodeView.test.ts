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
    const toggle = container.querySelector('button')!
    expect(toggle?.getAttribute('aria-expanded')).toBe('false')
    toggle?.click()
    await new Promise((r) => setTimeout(r, 0))
    const toggleAfter = container.querySelector('button')
    expect(toggleAfter?.getAttribute('aria-expanded')).toBe('true')
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

  it('renders body content in the DOM after toggling open (#183)', async () => {
    const { container, cleanup } = await mountNodeViewEditor([
      mkBlock('NOTE', {
        clean_text:
          '<details><summary>Expand me</summary>Important body text</details>'
      })
    ])
    // Body content must exist in the DOM even when closed (ProseMirror
    // requires a live contentDOM for inline* nodes). It's hidden via CSS,
    // not unmounted.
    const wrapper = container.querySelector('[data-type="silt-details"]')
    const contentEl = wrapper?.querySelector(
      '.svelte-tiptap-node-view-content, [data-view-content]'
    )
    // The NodeViewContent element should be present in the DOM regardless
    // of open state. Check that the body text survives somewhere in the
    // wrapper's text content after the summary.
    const fullText = wrapper?.textContent || ''
    expect(fullText).toContain('Important body text')

    // Toggle open and verify it's still present
    const toggle = container.querySelector('button')!
    toggle.click()
    await new Promise((r) => setTimeout(r, 0))
    const fullTextAfter =
      container.querySelector('[data-type="silt-details"]')?.textContent || ''
    expect(fullTextAfter).toContain('Important body text')
    cleanup()
  })
})
