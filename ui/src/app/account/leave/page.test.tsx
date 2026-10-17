import {cleanup, render, screen, waitFor} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {QueryClient, QueryClientProvider} from '@tanstack/react-query'
import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import LeaveProductPage from './page'
import {fetchProfile} from '@/lib/queries'
import {requestServiceUnlinkAPI} from '@/lib/mutations'
import {isAxiosError} from '@/lib/axios'

vi.mock('next/navigation', () => ({useSearchParams: () => new URLSearchParams('service=poker')}))
vi.mock('@/lib/queries', () => ({fetchProfile: vi.fn()}))
vi.mock('@/lib/mutations', () => ({requestServiceUnlinkAPI: vi.fn()}))
vi.mock('@/lib/axios', () => ({isAxiosError: vi.fn()}))

afterEach(cleanup)

function renderPage() {
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}})
  return render(<QueryClientProvider client={client}><LeaveProductPage/></QueryClientProvider>)
}

describe('LeaveProductPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(isAxiosError).mockReset()
    vi.mocked(fetchProfile).mockResolvedValue({has_password: true} as never)
  })

  it('asks for the product phrase, not the account phrase', async () => {
    const user = userEvent.setup()
    vi.mocked(requestServiceUnlinkAPI).mockResolvedValue({request_id: 'r1', state: 'awaiting_confirmation', confirm_by: '2026-10-08T12:00:00Z'})
    renderPage()
    const input = await screen.findByLabelText(/SAIR DO POKER/)
    await user.type(input, 'EXCLUIR MINHA CONTA')
    expect(screen.getByRole('button', {name: 'Leave CTech Poker'})).toBeDisabled()
    await user.clear(input)
    await user.type(input, 'SAIR DO POKER')
    await user.type(screen.getByLabelText('Account password'), 'pw')
    await user.click(screen.getByRole('button', {name: 'Leave CTech Poker'}))
    await waitFor(() => expect(requestServiceUnlinkAPI).toHaveBeenCalledWith('poker', 'SAIR DO POKER', 'pw'))
    expect(await screen.findByText(/We sent a confirmation link/i)).toBeInTheDocument()
  })

  it('shows the product blockers', async () => {
    const user = userEvent.setup()
    vi.mocked(isAxiosError).mockImplementation((e: unknown) => e != null)
    vi.mocked(requestServiceUnlinkAPI).mockRejectedValue({response: {status: 409, data: {
      type: 'https://accounts.aoctech.app/problems/deletion-blocked',
      blockers: [{code: 'poker.seated_at_table'}],
    }}})
    renderPage()
    await user.type(await screen.findByLabelText(/SAIR DO POKER/), 'SAIR DO POKER')
    await user.type(screen.getByLabelText('Account password'), 'pw')
    await user.click(screen.getByRole('button', {name: 'Leave CTech Poker'}))
    expect(await screen.findByText(/seated at a CTech Poker table/i)).toBeInTheDocument()
  })

  it('says the product cannot be left on 422', async () => {
    const user = userEvent.setup()
    vi.mocked(isAxiosError).mockImplementation((e: unknown) => e != null)
    vi.mocked(requestServiceUnlinkAPI).mockRejectedValue({response: {status: 422, data: {type: 'https://x/problems/unknown-service'}}})
    renderPage()
    await user.type(await screen.findByLabelText(/SAIR DO POKER/), 'SAIR DO POKER')
    await user.type(screen.getByLabelText('Account password'), 'pw')
    await user.click(screen.getByRole('button', {name: 'Leave CTech Poker'}))
    expect(await screen.findByText(/cannot be left on its own/i)).toBeInTheDocument()
  })
})
