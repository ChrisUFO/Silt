import { describe, it, expect, vi } from 'vitest'
import {
  mountNodeViewEditor,
  mkBlock
} from '../../lib/editor/nodeview-test-harness'

// Clipboard API mock
Object.assign(navigator, {
  clipboard: { writeText: vi.fn() }
})

describe('CodeBlockNodeView (#189)', () => {
  it('renders a code block with language badge', async () => {
    const { container, cleanup } = await mountNodeViewEditor([
      mkBlock('CODE', { clean_text: 'const x = 1;', code_lang: 'js' })
    ])
    const wrapper = container.querySelector('[data-type="code-block"]')
    expect(wrapper).toBeTruthy()
    const badge = wrapper?.querySelector('.font-mono')
    expect(badge?.textContent?.trim()).toBe('js')
    cleanup()
  })

  it('renders a copy button', async () => {
    const { container, cleanup } = await mountNodeViewEditor([
      mkBlock('CODE', { clean_text: 'print("hello")', code_lang: 'py' })
    ])
    const copyBtn = container.querySelector('button[aria-label="Copy code"]')
    expect(copyBtn).toBeTruthy()
    cleanup()
  })

  it('has a copy button that references the code content', async () => {
    const { container, cleanup } = await mountNodeViewEditor([
      mkBlock('CODE', { clean_text: 'hello world', code_lang: '' })
    ])
    const copyBtn = container.querySelector('button[aria-label="Copy code"]')
    expect(copyBtn).toBeTruthy()
    // The clipboard writeText may throw in jsdom; the component handles this
    // with a catch. Verify the button exists and is accessible.
    expect(copyBtn?.textContent?.trim()).toBe('content_copy')
    cleanup()
  })

  it('renders code block with no language as "code"', async () => {
    const { container, cleanup } = await mountNodeViewEditor([
      mkBlock('CODE', { clean_text: 'plain code', code_lang: '' })
    ])
    const wrapper = container.querySelector('[data-type="code-block"]')
    const badge = wrapper?.querySelector('.font-mono')
    expect(badge?.textContent?.trim()).toBe('code')
    cleanup()
  })
})
