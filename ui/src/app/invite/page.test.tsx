import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import InvitePage from './page'
import { acceptInvitationAPI } from '@/lib/mutations'
import { useAuthStore } from '@/store/auth'

const push = vi.fn()

vi.mock('next/navigation', () => ({
  useRouter: () => ({ push }),
  useSearchParams: () => new URLSearchParams('token=the-token'),
}))

vi.mock('@/lib/mutations', () => ({
  acceptInvitationAPI: vi.fn(),
  resendVerificationAPI: vi.fn(),
}))

vi.mock('@/lib/queries', () => ({
  fetchProfile: vi.fn().mockResolvedValue({ user_id: 'u1', email: 'a@b.com' }),
}))

afterEach(cleanup)

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <InvitePage />
    </QueryClientProvider>,
  )
}

/** An axios-shaped rejection, which is what the page branches on. */
function problem(status: number, type: string) {
  return Object.assign(new Error('rejected'), {
    isAxiosError: true,
    response: { status, data: { type, status } },
  })
}

describe('invitation acceptance', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useAuthStore.setState({ accessToken: 'token', isInitialized: true })
  })

  // The address the invitation is checked against is read from the account
  // record server-side. Sending one from the browser would turn the token into
  // a bearer capability anybody who found the link could spend — this test is
  // what stops an e-mail field being "helpfully" added back later.
  it('sends the token alone, never an e-mail address', async () => {
    vi.mocked(acceptInvitationAPI).mockResolvedValue({
      organization_id: 'org_1',
      user_id: 'u1',
      role: 'member',
      created_at: new Date().toISOString(),
    })
    const user = userEvent.setup()
    renderPage()

    await user.click(await screen.findByRole('button', { name: /accept invitation/i }))

    await waitFor(() => expect(acceptInvitationAPI).toHaveBeenCalled())
    expect(acceptInvitationAPI).toHaveBeenCalledWith('the-token')
    expect(acceptInvitationAPI).toHaveBeenCalledTimes(1)
    // One argument, so no object carrying an address can ride along.
    expect(vi.mocked(acceptInvitationAPI).mock.calls[0]).toHaveLength(1)
  })

  it('forwards into the organization it just joined', async () => {
    vi.mocked(acceptInvitationAPI).mockResolvedValue({
      organization_id: 'org_7',
      user_id: 'u1',
      role: 'member',
      created_at: new Date().toISOString(),
    })
    const user = userEvent.setup()
    renderPage()

    await user.click(await screen.findByRole('button', { name: /accept invitation/i }))

    await waitFor(() =>
      expect(push).toHaveBeenCalledWith('/account/organizations/detail?id=org_7'),
    )
  })

  // The verification gate offers a button that fixes the problem. Every other
  // refusal must not, or the page sends people to resend an e-mail that cannot
  // help them.
  it('offers resend only for the verification gate', async () => {
    vi.mocked(acceptInvitationAPI).mockRejectedValue(
      problem(403, 'https://accounts.aoctech.app/problems/email-not-verified'),
    )
    const user = userEvent.setup()
    renderPage()

    await user.click(await screen.findByRole('button', { name: /accept invitation/i }))

    expect(await screen.findByText(/verify your e-mail address first/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /resend verification/i })).toBeInTheDocument()
  })

  // Unknown, expired, already used and addressed-to-somebody-else arrive alike
  // from the server on purpose. The page must not guess which happened.
  it('says only that the invitation is no longer valid', async () => {
    vi.mocked(acceptInvitationAPI).mockRejectedValue(
      problem(403, 'https://accounts.aoctech.app/problems/forbidden'),
    )
    const user = userEvent.setup()
    renderPage()

    await user.click(await screen.findByRole('button', { name: /accept invitation/i }))

    expect(await screen.findByText(/no longer valid/i)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /resend verification/i })).not.toBeInTheDocument()
  })

  // Signed out, the page must reveal nothing about the organization: there is
  // no unauthenticated endpoint that would tell it, and adding one would turn a
  // leaked link into a read capability for the workspace's name.
  it('reveals nothing about the organization while signed out', async () => {
    useAuthStore.setState({ accessToken: null, isInitialized: true })
    renderPage()

    expect(await screen.findByRole('link', { name: /sign in/i })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /create an account/i })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /accept invitation/i })).not.toBeInTheDocument()
    expect(acceptInvitationAPI).not.toHaveBeenCalled()
  })
})
