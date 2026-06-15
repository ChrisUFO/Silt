// Component coverage for the FontSelect combobox (#82): the ARIA combobox
// contract (aria-expanded + aria-controls → listbox id) and the CSS-injection
// sanitization of the unlisted/legacy config value rendered into a style attr.

import { describe, expect, it, afterEach } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/svelte'
import FontSelect from './FontSelect.svelte'

describe('FontSelect combobox', () => {
  afterEach(() => cleanup())

  it('links the trigger to its listbox via aria-controls when open', async () => {
    render(FontSelect, { props: { value: 'Inter', category: 'body', label: 'Font family' } })
    const combo = screen.getByRole('combobox', { name: 'Font family' })

    // Closed: expanded is false and no control target (listbox not mounted).
    expect(combo.getAttribute('aria-expanded')).toBe('false')
    expect(combo.getAttribute('aria-controls')).toBeNull()
    expect(screen.queryByRole('listbox')).toBeNull()

    // Open: the listbox mounts with the id the trigger now controls.
    await fireEvent.click(combo)
    expect(combo.getAttribute('aria-expanded')).toBe('true')
    const controls = combo.getAttribute('aria-controls')
    expect(controls).toBeTruthy()
    const listbox = screen.getByRole('listbox', { name: 'Font family' })
    expect(listbox.id).toBe(controls)
  })

  it('sanitizes an unlisted (user-controlled) font value before it reaches a style attribute', async () => {
    // A value not in the registry becomes the "unlisted" option whose cssFamily
    // is rendered into style="font-family:…". The breakout chars must be stripped
    // (sandbox-by-validation, mirroring editor-tokens + themes.isValidFontFamily).
    const malicious = "Hack}; body{background:red}"
    render(
      FontSelect,
      { props: { value: malicious, category: 'body', label: 'Font family' } }
    )
    const combo = screen.getByRole('combobox', { name: 'Font family' })
    // The trigger renders the selected (unlisted) option in-font.
    const triggerStyle = combo.getAttribute('style') ?? ''
    expect(triggerStyle).not.toMatch(/[;{}]/)

    // Opening surfaces the option row, also styled — still clean.
    await fireEvent.click(combo)
    const options = screen.getAllByRole('option')
    const unlisted = options.find((o) => o.textContent?.includes('(custom)'))
    expect(unlisted).toBeTruthy()
    const optionStyle = unlisted!.getAttribute('style') ?? ''
    expect(optionStyle).not.toMatch(/[;{}]/)
  })
})
