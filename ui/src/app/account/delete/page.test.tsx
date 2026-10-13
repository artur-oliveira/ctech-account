import {cleanup, render, screen, waitFor} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {QueryClient, QueryClientProvider} from '@tanstack/react-query'
import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import DeleteAccountPage from './page'
import {fetchDeletionRequest, fetchProfile} from '@/lib/queries'
import {requestDeletionAPI} from '@/lib/mutations'
import {isAxiosError} from '@/lib/axios'

vi.mock('@/lib/queries', () => ({fetchDeletionRequest: vi.fn(), fetchProfile: vi.fn()}))
vi.mock('@/lib/mutations', () => ({requestDeletionAPI: vi.fn()}))
vi.mock('@/lib/axios', () => ({isAxiosError: vi.fn()}))

afterEach(cleanup)

function renderPage() {
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}})
  return render(<QueryClientProvider client={client}><DeleteAccountPage/></QueryClientProvider>)
}

describe('DeleteAccountPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(isAxiosError).mockReset()
    vi.mocked(fetchDeletionRequest).mockResolvedValue(null)
    vi.mocked(fetchProfile).mockResolvedValue({has_password: true} as never)
  })

  it('submits phrase and password, then asks to check the e-mail', async () => {
    const user = userEvent.setup()
    vi.mocked(requestDeletionAPI).mockResolvedValue({request_id: 'r1', state: 'awaiting_confirmation', confirm_by: '2026-10-08T12:00:00Z'})
    renderPage()
    await user.type(await screen.findByLabelText(/EXCLUIR MINHA CONTA/), 'EXCLUIR MINHA CONTA')
    await user.type(screen.getByLabelText('Account password'), 'pw')
    await user.click(screen.getByRole('button', {name: 'Request deletion'}))
    await waitFor(() => expect(requestDeletionAPI).toHaveBeenCalledWith('EXCLUIR MINHA CONTA', 'pw'))
    expect(await screen.findByText(/We sent a confirmation link/i)).toBeInTheDocument()
  })

  it('keeps the button disabled until the phrase matches', async () => {
    const user = userEvent.setup()
    renderPage()
    await user.type(await screen.findByLabelText(/EXCLUIR MINHA CONTA/), 'excluir')
    expect(screen.getByRole('button', {name: 'Request deletion'})).toBeDisabled()
  })

  it('shows blockers', async () => {
    const user = userEvent.setup()
    vi.mocked(isAxiosError).mockImplementation((e: unknown) => e != null)
    vi.mocked(requestDeletionAPI).mockRejectedValue({response: {status: 409, data: {
      type: 'https://accounts.aoctech.app/problems/deletion-blocked',
      blockers: [
        {code: 'wallet.balance_nonzero', action_url: 'https://ledger.aoctech.app/withdraw'},
        {code: 'poker.seated'},
      ],
    }}})
    renderPage()
    await user.type(await screen.findByLabelText(/EXCLUIR MINHA CONTA/), 'EXCLUIR MINHA CONTA')
    await user.type(screen.getByLabelText('Account password'), 'pw')
    await user.click(screen.getByRole('button', {name: 'Request deletion'}))
    expect(await screen.findByText(/balance in CTech Ledger/)).toBeInTheDocument()
    expect(screen.getByRole('link', {name: 'Resolve'})).toHaveAttribute('href', 'https://ledger.aoctech.app/withdraw')
    expect(screen.getByText(/poker\.seated/)).toBeInTheDocument()
  })

  it('without password: no password field, submits phrase only', async () => {
    const user = userEvent.setup()
    vi.mocked(fetchProfile).mockResolvedValue({has_password: false} as never)
    vi.mocked(requestDeletionAPI).mockResolvedValue({request_id: 'r1', state: 'awaiting_confirmation'})
    renderPage()
    await user.type(await screen.findByLabelText(/EXCLUIR MINHA CONTA/), 'EXCLUIR MINHA CONTA')
    expect(screen.queryByLabelText('Account password')).toBeNull()
    await user.click(screen.getByRole('button', {name: 'Request deletion'}))
    await waitFor(() => expect(requestDeletionAPI).toHaveBeenCalledWith('EXCLUIR MINHA CONTA', undefined))
  })
})
