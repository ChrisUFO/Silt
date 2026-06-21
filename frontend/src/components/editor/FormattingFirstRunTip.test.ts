import { describe, it, expect } from 'vitest'
import { render, fireEvent } from '@testing-library/svelte'
import FormattingFirstRunTip from './FormattingFirstRunTip.svelte'

describe('FormattingFirstRunTip', () => {
  it('renders when not dismissed', () => {
    const { getByRole, getByText } = render(FormattingFirstRunTip, {
      props: { dismissed: false, onDismiss: () => {} }
    })
    expect(getByRole('status')).toBeTruthy()
    expect(getByText(/Ctrl\+B/)).toBeTruthy()
  })

  it('does not render when dismissed', () => {
    const { container } = render(FormattingFirstRunTip, {
      props: { dismissed: true, onDismiss: () => {} }
    })
    expect(container.querySelector('.first-run-tip')).toBeNull()
  })

  it('calls onDismiss when Got it is clicked', async () => {
    let dismissed = false
    const { getByText } = render(FormattingFirstRunTip, {
      props: { dismissed: false, onDismiss: () => { dismissed = true } }
    })
    await fireEvent.click(getByText('Got it'))
    expect(dismissed).toBe(true)
  })

  it('has role=status with aria-live=polite', () => {
    const { getByRole } = render(FormattingFirstRunTip, {
      props: { dismissed: false, onDismiss: () => {} }
    })
    const status = getByRole('status')
    expect(status.getAttribute('aria-live')).toBe('polite')
  })
})
