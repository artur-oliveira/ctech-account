import {render, screen, waitFor} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import LoginPage from './page'
import {beginPasskeyAuthAPI, completePasskeyAuthAPI} from '@/lib/mutations'
import {buildAssertionCredential} from '@/lib/webauthn'
import {startOAuthFlow} from '@/lib/auth-flow'

vi.mock('@/lib/mutations', () => ({
  beginPasskeyAuthAPI: vi.fn(),
  completePasskeyAuthAPI: vi.fn(),
  resendVerificationAPI: vi.fn(),
}))

vi.mock('@/lib/webauthn', () => ({
  buildAssertionCredential: vi.fn(),
}))

vi.mock('@/lib/auth-flow', () => ({
  startOAuthFlow: vi.fn(),
}))

vi.mock('@/lib/axios', () => ({
  api: {post: vi.fn()},
  isAxiosError: () => false,
}))

vi.mock('next/navigation', () => ({
  useSearchParams: () => new URLSearchParams(),
  useRouter: () => ({replace: vi.fn(), push: vi.fn()}),
}))

beforeEach(() => {
  vi.clearAllMocks()
  vi.stubGlobal('PublicKeyCredential', undefined)
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('LoginPage — passkey-first authentication', () => {
  it('keeps password, passkey and Google available without probing an email', () => {
    render(<LoginPage/>)

    const email = screen.getByLabelText('Email')
    expect(email).toHaveAttribute('autocomplete', 'username webauthn')
    expect(screen.getByLabelText('Password')).toBeInTheDocument()
    expect(screen.getByRole('button', {name: 'Sign in with passkey'})).toBeInTheDocument()
    expect(screen.getByRole('button', {name: 'Continue with Google'})).toBeInTheDocument()
    expect(beginPasskeyAuthAPI).not.toHaveBeenCalled()
  })

  it('completes an explicit passkey ceremony as a first-factor login', async () => {
    vi.mocked(beginPasskeyAuthAPI).mockResolvedValue({session_token: 'tok', options: '{"publicKey":{}}'})
    vi.mocked(buildAssertionCredential).mockResolvedValue({id: 'credential'} as never)
    vi.mocked(completePasskeyAuthAPI).mockResolvedValue({requires_mfa: false})
    const user = userEvent.setup()
    render(<LoginPage/>)

    await user.click(screen.getByRole('button', {name: 'Sign in with passkey'}))

    await waitFor(() => expect(completePasskeyAuthAPI).toHaveBeenCalledWith('tok', {id: 'credential'}))
    expect(startOAuthFlow).toHaveBeenCalledWith('/account')
  })

  it('explains a user-cancelled passkey prompt without calling it a network error', async () => {
    vi.mocked(beginPasskeyAuthAPI).mockResolvedValue({session_token: 'tok', options: '{"publicKey":{}}'})
    vi.mocked(buildAssertionCredential).mockRejectedValue(new DOMException('cancelled', 'NotAllowedError'))
    const user = userEvent.setup()
    render(<LoginPage/>)

    await user.click(screen.getByRole('button', {name: 'Sign in with passkey'}))

    expect(await screen.findByText(
      'Passkey sign-in was cancelled. You can try again or use your password.',
    )).toBeInTheDocument()
    expect(screen.getByLabelText('Password')).toBeInTheDocument()
  })

  it('starts privacy-preserving conditional mediation when the browser supports it', async () => {
    const available = vi.fn().mockResolvedValue(true)
    vi.stubGlobal('PublicKeyCredential', {isConditionalMediationAvailable: available})
    vi.mocked(beginPasskeyAuthAPI).mockResolvedValue({session_token: 'conditional', options: '{"publicKey":{}}'})
    vi.mocked(buildAssertionCredential).mockResolvedValue({id: 'conditional-credential'} as never)
    vi.mocked(completePasskeyAuthAPI).mockResolvedValue({requires_mfa: false})

    render(<LoginPage/>)

    await waitFor(() => expect(buildAssertionCredential).toHaveBeenCalledWith(
      '{"publicKey":{}}',
      expect.objectContaining({mediation: 'conditional', signal: expect.any(AbortSignal)}),
    ))
    await waitFor(() => expect(startOAuthFlow).toHaveBeenCalledWith('/account'))
  })

  it('fails open when conditional mediation is unavailable', async () => {
    vi.stubGlobal('PublicKeyCredential', {
      isConditionalMediationAvailable: vi.fn().mockResolvedValue(false),
    })
    render(<LoginPage/>)

    await waitFor(() => expect(beginPasskeyAuthAPI).not.toHaveBeenCalled())
    expect(screen.getByLabelText('Password')).toBeInTheDocument()
    expect(screen.getByRole('button', {name: 'Sign in with passkey'})).toBeInTheDocument()
  })
})
