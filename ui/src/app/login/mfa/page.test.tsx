import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi, beforeEach } from 'vitest'
import MFAPage from './page'
import { mfaChallengeAPI } from '@/lib/mutations'
import { startOAuthFlow } from '@/lib/auth-flow'
import { MFA_METHODS_KEY, MFA_TOKEN_KEY } from '@/lib/constants'

vi.mock('@/lib/mutations', () => ({
  mfaChallengeAPI: vi.fn(),
}))

vi.mock('@/lib/auth-flow', () => ({
  startOAuthFlow: vi.fn(),
}))

vi.mock('@/lib/axios', () => ({
  isAxiosError: () => false,
}))

vi.mock('next/navigation', () => ({
  useSearchParams: () => new URLSearchParams(),
}))

function seedSession(methods: string[]) {
  sessionStorage.setItem(MFA_TOKEN_KEY, 'mfa-token-abc')
  sessionStorage.setItem(MFA_METHODS_KEY, JSON.stringify(methods))
}

beforeEach(() => {
  sessionStorage.clear()
  vi.clearAllMocks()
})

describe('MFAPage — TOTP-only', () => {
  it('renders the TOTP form with no passkey button or separator', async () => {
    seedSession(['totp'])
    render(<MFAPage />)

    await screen.findByLabelText('Digit 1')
    expect(screen.queryByText(/passkey/i)).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Verify' })).toBeInTheDocument()
  })

  it('accepts the otp AMR value returned after passkey authentication', async () => {
    seedSession(['otp'])
    render(<MFAPage />)

    await screen.findByLabelText('Digit 1')
    expect(screen.getByRole('button', { name: 'Verify' })).toBeInTheDocument()
    expect(screen.queryByText('Session expired. Please sign in again.')).not.toBeInTheDocument()
  })

  it('never renders a passkey option even if the backend still sends "passkey" as a method', async () => {
    seedSession(['passkey'])
    render(<MFAPage />)

    // No usable method (TOTP absent) — falls to the session-expired state, not a passkey prompt.
    expect(await screen.findByText('Session expired. Please sign in again.')).toBeInTheDocument()
    expect(screen.queryByText(/passkey/i)).not.toBeInTheDocument()
    expect(screen.queryByLabelText('Digit 1')).not.toBeInTheDocument()
  })

  it('shows the expired-session state when there are no MFA methods at all', async () => {
    seedSession([])
    render(<MFAPage />)

    expect(await screen.findByText('Session expired. Please sign in again.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Back to sign in' })).toBeInTheDocument()
  })

  it('submits the TOTP code and starts the OAuth flow on success', async () => {
    seedSession(['totp'])
    vi.mocked(mfaChallengeAPI).mockResolvedValue(undefined)
    const user = userEvent.setup()
    render(<MFAPage />)

    const firstDigit = await screen.findByLabelText('Digit 1')
    await user.click(firstDigit)
    await user.paste('123456')
    await user.click(screen.getByRole('button', { name: 'Verify' }))

    expect(mfaChallengeAPI).toHaveBeenCalledWith('mfa-token-abc', '123456')
    expect(startOAuthFlow).toHaveBeenCalled()
    expect(sessionStorage.getItem(MFA_TOKEN_KEY)).toBeNull()
    expect(sessionStorage.getItem(MFA_METHODS_KEY)).toBeNull()
  })
})
