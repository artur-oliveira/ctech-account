import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { ConfirmDialog } from './confirm-dialog'
import { Button } from '@/components/ui/button'

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

describe('ConfirmDialog', () => {
  it('stays open while onConfirm is in flight and closes only after it resolves', async () => {
    const user = userEvent.setup()
    const gate = deferred<void>()
    const onConfirm = vi.fn(() => gate.promise)

    render(
      <ConfirmDialog
        trigger={<Button>Open</Button>}
        title="Submit for review"
        description="This cannot be changed after this point."
        onConfirm={onConfirm}
      />,
    )

    await user.click(screen.getByRole('button', { name: 'Open' }))
    await user.click(screen.getByRole('button', { name: 'Confirm' }))

    expect(onConfirm).toHaveBeenCalledTimes(1)
    // Regression: onConfirm was previously called via a fire-and-forget `mutate`,
    // so the dialog closed immediately instead of waiting for the real outcome.
    expect(screen.getByRole('dialog')).toBeInTheDocument()

    gate.resolve()
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('shows a visible pending indicator inside the dialog itself, not just on the hidden trigger', async () => {
    const user = userEvent.setup()
    const gate = deferred<void>()
    const onConfirm = vi.fn(() => gate.promise)

    render(
      <ConfirmDialog
        trigger={<Button>Open</Button>}
        title="Submit for review"
        description="This cannot be changed after this point."
        onConfirm={onConfirm}
      />,
    )

    await user.click(screen.getByRole('button', { name: 'Open' }))
    const dialog = screen.getByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: 'Confirm' }))

    // Regression: the pending label used to live only on the trigger button,
    // which sits behind this open dialog and is never seen mid-flight.
    expect(within(dialog).getByText('Processing…')).toBeInTheDocument()

    gate.resolve()
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('does not close on Escape while pending', async () => {
    const user = userEvent.setup()
    const gate = deferred<void>()
    const onConfirm = vi.fn(() => gate.promise)

    render(
      <ConfirmDialog
        trigger={<Button>Open</Button>}
        title="Submit for review"
        description="This cannot be changed after this point."
        onConfirm={onConfirm}
      />,
    )

    await user.click(screen.getByRole('button', { name: 'Open' }))
    await user.click(screen.getByRole('button', { name: 'Confirm' }))
    await user.keyboard('{Escape}')

    expect(screen.getByRole('dialog')).toBeInTheDocument()

    gate.resolve()
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })
})
