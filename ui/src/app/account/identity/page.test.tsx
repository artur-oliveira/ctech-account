import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it, vi, beforeEach } from 'vitest'
import IdentityPage from './page'
import { fetchKYC, fetchPasskeys, fetchTOTPStatus } from '@/lib/queries'
import { submitBasicKYCAPI, verifyPhoneKYCAPI } from '@/lib/mutations'
import type { KYCStatus } from '@/lib/types'

vi.mock('@/lib/queries', () => ({
  fetchKYC: vi.fn(),
  fetchTOTPStatus: vi.fn(),
  fetchPasskeys: vi.fn(),
}))

vi.mock('@/lib/mutations', () => ({
  submitBasicKYCAPI: vi.fn(),
  verifyPhoneKYCAPI: vi.fn(),
  resendKYCCodeAPI: vi.fn(),
  submitEnhancedKYCAPI: vi.fn(),
  uploadKYCDocumentAPI: vi.fn(),
}))

const NOT_STARTED: KYCStatus = { state: 'not_started', level: '' }
const AWAITING_PHONE: KYCStatus = { state: 'awaiting_phone_verification', level: 'basic', phone_masked: '***4321' }

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <IdentityPage />
    </QueryClientProvider>,
  )
}

describe('IdentityPage — Basic step', () => {
  beforeEach(() => {
    vi.mocked(fetchKYC).mockResolvedValue(NOT_STARTED)
    vi.mocked(fetchTOTPStatus).mockResolvedValue({ enabled: true })
    vi.mocked(fetchPasskeys).mockResolvedValue([])
  })

  it('blocks submit with an underage error and never calls the API', async () => {
    const user = userEvent.setup()
    renderPage()

    await screen.findByLabelText('CPF')
    await user.type(screen.getByLabelText('CPF'), '11144477735')
    await user.type(screen.getByLabelText('Full legal name'), 'Jane Doe')
    const seventeenYearsAgo = new Date()
    seventeenYearsAgo.setUTCFullYear(seventeenYearsAgo.getUTCFullYear() - 17)
    await user.type(screen.getByLabelText('Date of birth'), seventeenYearsAgo.toISOString().slice(0, 10))
    await user.type(screen.getByLabelText('Phone number'), '+5511987654321')

    await user.click(screen.getByRole('button', { name: 'Submit' }))

    expect(await screen.findByText('You must be at least 18 years old.')).toBeInTheDocument()
    expect(submitBasicKYCAPI).not.toHaveBeenCalled()
  })

  it('shows a generic failure message instead of "invalid data" for an unmapped submit error', async () => {
    const user = userEvent.setup()
    vi.mocked(submitBasicKYCAPI).mockRejectedValue(new Error('network down'))
    renderPage()

    await screen.findByLabelText('CPF')
    await user.type(screen.getByLabelText('CPF'), '11144477735')
    await user.type(screen.getByLabelText('Full legal name'), 'Jane Doe')
    const eighteenYearsAgo = new Date()
    eighteenYearsAgo.setUTCFullYear(eighteenYearsAgo.getUTCFullYear() - 18)
    await user.type(screen.getByLabelText('Date of birth'), eighteenYearsAgo.toISOString().slice(0, 10))
    await user.type(screen.getByLabelText('Phone number'), '+5511987654321')

    await user.click(screen.getByRole('button', { name: 'Submit' }))

    expect(await screen.findByText('Something went wrong. Try again.')).toBeInTheDocument()
    expect(screen.queryByText('Check the data and try again.')).not.toBeInTheDocument()
  })
})

describe('IdentityPage — phone verification step', () => {
  beforeEach(() => {
    vi.mocked(fetchKYC).mockResolvedValue(AWAITING_PHONE)
    vi.mocked(fetchTOTPStatus).mockResolvedValue({ enabled: true })
    vi.mocked(fetchPasskeys).mockResolvedValue([])
  })

  it('maps kyc-invalid-code to a readable message', async () => {
    const user = userEvent.setup()
    vi.mocked(verifyPhoneKYCAPI).mockRejectedValue({
      isAxiosError: true,
      response: { data: { type: 'https://accounts.aoctech.app/problems/kyc-invalid-code' } },
    })
    renderPage()

    await screen.findByLabelText('Verification code')
    await user.type(screen.getByLabelText('Verification code'), '000000')
    await user.click(screen.getByRole('button', { name: 'Verify' }))

    await waitFor(() => expect(screen.getByText('The code you entered is invalid or has expired.')).toBeInTheDocument())
  })
})
