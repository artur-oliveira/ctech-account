import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { RegisterPasskeyButton } from './passkey-actions'
import { beginPasskeyRegistrationAPI, completePasskeyRegistrationAPI } from '@/lib/mutations'
import { buildRegistrationCredential } from '@/lib/webauthn'

vi.mock('@/lib/mutations', () => ({
  beginPasskeyRegistrationAPI: vi.fn(),
  completePasskeyRegistrationAPI: vi.fn(),
  removePasskeyAPI: vi.fn(),
}))

vi.mock('@/lib/webauthn', () => ({
  buildRegistrationCredential: vi.fn(),
}))

vi.mock('@/lib/axios', () => ({
  isAxiosError: () => false,
}))

function renderAction() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <RegisterPasskeyButton />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('RegisterPasskeyButton', () => {
  it('asks for a recognizable name and persists it through the WebAuthn ceremony', async () => {
    vi.mocked(beginPasskeyRegistrationAPI).mockResolvedValue({
      session_token: 'registration-token',
      name: 'Office YubiKey',
      options: '{"publicKey":{}}',
    })
    vi.mocked(buildRegistrationCredential).mockResolvedValue({ id: 'credential' } as never)
    vi.mocked(completePasskeyRegistrationAPI).mockResolvedValue(undefined)
    const user = userEvent.setup()
    renderAction()

    await user.click(screen.getByRole('button', { name: 'Add passkey' }))
    await user.type(screen.getByLabelText('Passkey name'), '  Office YubiKey  ')
    await user.click(screen.getByRole('button', { name: 'Continue' }))

    await waitFor(() => expect(beginPasskeyRegistrationAPI).toHaveBeenCalledWith('Office YubiKey'))
    expect(completePasskeyRegistrationAPI).toHaveBeenCalledWith(
      'registration-token',
      'Office YubiKey',
      { id: 'credential' },
    )
  })
})
