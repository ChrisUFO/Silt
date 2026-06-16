import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/svelte'
import ToastContainer from './ToastContainer.svelte'
import {
  pushNotification,
  dismissNotification,
  _resetForTests
} from '../notifications/store.svelte'

describe('ToastContainer (#86)', () => {
  beforeEach(() => {
    _resetForTests()
  })

  afterEach(() => {
    cleanup()
    _resetForTests()
  })

  it('renders nothing when there are no notifications', () => {
    const { container } = render(ToastContainer)
    expect(container.querySelectorAll('[role="status"], [role="alert"]')).toHaveLength(0)
  })

  it('renders an info notification as role=status', async () => {
    pushNotification({ kind: 'info', message: 'Saved' })
    render(ToastContainer)
    const status = await screen.findByRole('status')
    expect(status).toHaveTextContent('Saved')
  })

  it('renders an error notification as role=alert', async () => {
    pushNotification({ kind: 'error', message: 'Boom' })
    render(ToastContainer)
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Boom')
  })

  it('renders an action button when an action is attached', async () => {
    const action = vi.fn()
    pushNotification({ kind: 'error', message: 'Try again', action: { label: 'Retry', run: action } })
    render(ToastContainer)
    const btn = await screen.findByRole('button', { name: 'Retry' })
    await fireEvent.click(btn)
    expect(action).toHaveBeenCalledTimes(1)
    // After action + dismiss, the toast is gone.
    expect(screen.queryByText('Try again')).not.toBeInTheDocument()
  })

  it('dismiss button removes the notification', async () => {
    pushNotification({ kind: 'info', message: 'Dismissable' })
    render(ToastContainer)
    const dismissBtn = screen.getByRole('button', { name: 'Dismiss notification' })
    await fireEvent.click(dismissBtn)
    expect(screen.queryByText('Dismissable')).not.toBeInTheDocument()
  })

  it('renders multiple notifications in the stack', async () => {
    pushNotification({ kind: 'info', message: 'First' })
    pushNotification({ kind: 'error', message: 'Second' })
    render(ToastContainer)
    expect(screen.getByText('First')).toBeInTheDocument()
    expect(screen.getByText('Second')).toBeInTheDocument()
  })

  it('live region is aria-live=assertive for errors and polite for info', async () => {
    pushNotification({ kind: 'info', message: 'Info' })
    pushNotification({ kind: 'error', message: 'Err' })
    render(ToastContainer)
    expect(screen.getByRole('status')).toHaveAttribute('aria-live', 'polite')
    expect(screen.getByRole('alert')).toHaveAttribute('aria-live', 'assertive')
  })
})
