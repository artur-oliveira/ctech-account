import {cleanup, render, screen} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {QueryClient, QueryClientProvider} from '@tanstack/react-query'
import type {ReactElement} from 'react'
import {afterEach, describe, expect, it, vi} from 'vitest'
import {DeletionLinkPage} from './deletion-link-page'

vi.mock('@/lib/axios', () => ({isAxiosError: () => true}))
vi.mock('@/store/auth', () => ({useAuthStore: {getState: () => ({clearAuth: vi.fn()})}}))

afterEach(cleanup)

function renderWithClient(ui: ReactElement) {
  const client = new QueryClient({defaultOptions: {mutations: {retry: false}}})
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>)
}

describe('DeletionLinkPage', () => {
  it('TestConfirmPage_DoesNothingUntilClicked', async () => {
    const action = vi.fn().mockResolvedValue({request_id: 'r1', state: 'pending_deletion', grace_until: '2026-10-14T12:00:00Z'})
    const user = userEvent.setup()
    renderWithClient(<DeletionLinkPage kind="confirm" requestID="r1" token="t1" action={action}/>)
    expect(action).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', {name: 'Confirm deletion'}))
    expect(action).toHaveBeenCalledWith('r1', 't1')
    expect(await screen.findByText(/link to cancel/i)).toBeInTheDocument()
  })

  it('CancelPage distinguishes too-late from invalid', async () => {
    const user = userEvent.setup()
    const tooLate = vi.fn().mockRejectedValue({response: {status: 409}})
    renderWithClient(<DeletionLinkPage kind="cancel" requestID="r1" token="t1" action={tooLate}/>)
    await user.click(screen.getByRole('button', {name: 'Cancel deletion'}))
    expect(await screen.findByText(/can no longer be cancelled/i)).toBeInTheDocument()
    cleanup()
    const invalid = vi.fn().mockRejectedValue({response: {status: 401}})
    renderWithClient(<DeletionLinkPage kind="cancel" requestID="r1" token="t1" action={invalid}/>)
    await user.click(screen.getByRole('button', {name: 'Cancel deletion'}))
    expect(await screen.findByText(/link is invalid/i)).toBeInTheDocument()
  })

  it('shows the invalid message when the link lacks parameters', () => {
    renderWithClient(<DeletionLinkPage kind="confirm" requestID="" token="" action={vi.fn()}/>)
    expect(screen.queryByRole('button')).toBeNull()
  })
})
