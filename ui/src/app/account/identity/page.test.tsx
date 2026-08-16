import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, describe, expect, it, vi, beforeEach } from 'vitest'
import IdentityPage from './page'
import { fetchKYC, fetchPasskeys, fetchTOTPStatus } from '@/lib/queries'
import { submitBasicKYCAPI } from '@/lib/mutations'
import { lookupViaCEP } from '@/lib/viacep'
import type { KYCStatus } from '@/lib/types'

vi.mock('@/lib/queries', () => ({
  fetchKYC: vi.fn(),
  fetchTOTPStatus: vi.fn(),
  fetchPasskeys: vi.fn(),
}))

vi.mock('@/lib/mutations', () => ({
  submitBasicKYCAPI: vi.fn(),
  submitEnhancedKYCAPI: vi.fn(),
  uploadKYCDocumentAPI: vi.fn(),
}))

vi.mock('@/lib/viacep', () => ({
  lookupViaCEP: vi.fn(),
}))

const NOT_STARTED: KYCStatus = { state: 'not_started', level: '' }
const BASIC_VERIFIED: KYCStatus = { state: 'basic_verified', level: 'basic', phone_masked: '***4321' }

afterEach(cleanup)

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <IdentityPage />
    </QueryClientProvider>,
  )
}

async function fillAddress(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByLabelText('Postal code'), '01310100')
  await user.type(screen.getByLabelText('Number'), '1000')
  await user.type(screen.getByLabelText('Street'), 'Av. Paulista')
  await user.type(screen.getByLabelText('District'), 'Bela Vista')
  await user.type(screen.getByLabelText('City'), 'São Paulo')
  await user.type(screen.getByLabelText('State'), 'SP')
}

describe('IdentityPage — Basic step', () => {
  beforeEach(() => {
    vi.mocked(fetchKYC).mockResolvedValue(NOT_STARTED)
    vi.mocked(fetchTOTPStatus).mockResolvedValue({ enabled: true })
    vi.mocked(fetchPasskeys).mockResolvedValue([])
    vi.mocked(lookupViaCEP).mockResolvedValue({ status: 'not_found' })
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
    await user.type(screen.getByLabelText('Phone number'), '11987654321')
    await fillAddress(user)

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
    await user.type(screen.getByLabelText('Phone number'), '11987654321')
    await fillAddress(user)

    await user.click(screen.getByRole('button', { name: 'Submit' }))

    expect(await screen.findByText('Something went wrong. Try again.')).toBeInTheDocument()
    expect(screen.queryByText('Check the data and try again.')).not.toBeInTheDocument()
  })

  it('formats a national Brazilian number and submits it as E.164', async () => {
    const user = userEvent.setup()
    vi.mocked(submitBasicKYCAPI).mockResolvedValue(BASIC_VERIFIED)
    renderPage()

    await screen.findByLabelText('CPF')
    await screen.findByLabelText('CPF')
    await user.type(screen.getByLabelText('CPF'), '11144477735')
    await user.type(screen.getByLabelText('Full legal name'), 'Jane Doe')
    const eighteenYearsAgo = new Date()
    eighteenYearsAgo.setUTCFullYear(eighteenYearsAgo.getUTCFullYear() - 18)
    await user.type(screen.getByLabelText('Date of birth'), eighteenYearsAgo.toISOString().slice(0, 10))
    await user.type(screen.getByLabelText('Phone number'), '11987654321')
    await fillAddress(user)

    expect(screen.getByLabelText('Phone number')).toHaveValue('(11) 98765-4321')
    await user.click(screen.getByRole('button', { name: 'Submit' }))

    await waitFor(() => expect(submitBasicKYCAPI).toHaveBeenCalled())
    expect(vi.mocked(submitBasicKYCAPI).mock.calls.at(-1)?.[0]).toEqual(expect.objectContaining({ phone_number: '+5511987654321', address: { zip_code: '01310100', street: 'Av. Paulista', number: '1000', complement: '', district: 'Bela Vista', city: 'São Paulo', state: 'SP' } }))
  })

  it('filters countries by name and updates the calling code used for submission', async () => {
    const user = userEvent.setup()
    vi.mocked(submitBasicKYCAPI).mockResolvedValue(BASIC_VERIFIED)
    renderPage()

    await screen.findByLabelText('CPF')
    await user.type(screen.getByLabelText('CPF'), '11144477735')
    await user.type(screen.getByLabelText('Full legal name'), 'Jane Doe')
    const eighteenYearsAgo = new Date()
    eighteenYearsAgo.setUTCFullYear(eighteenYearsAgo.getUTCFullYear() - 18)
    await user.type(screen.getByLabelText('Date of birth'), eighteenYearsAgo.toISOString().slice(0, 10))

    const countrySearch = screen.getByRole('combobox')
    await user.clear(countrySearch)
    await user.type(countrySearch, 'Portugal')
    await user.click(await screen.findByRole('option', { name: /Portugal.*\+351/ }))
    await user.type(screen.getByLabelText('Phone number'), '912345678')
    await fillAddress(user)
    await user.click(screen.getByRole('button', { name: 'Submit' }))

    await waitFor(() => expect(submitBasicKYCAPI).toHaveBeenCalled())
    expect(vi.mocked(submitBasicKYCAPI).mock.calls.at(-1)?.[0]).toEqual(expect.objectContaining({ phone_number: '+351912345678' }))
  })

  it('limits legal names and phone input to valid country-specific maximum lengths', async () => {
    const user = userEvent.setup()
    renderPage()

    await screen.findByLabelText('Full legal name')
    await user.type(screen.getByLabelText('Full legal name'), 'a'.repeat(300))
    await user.type(screen.getByLabelText('Phone number'), '11987654321999')

    expect(screen.getByLabelText('Full legal name')).toHaveValue('a'.repeat(255))
    expect(screen.getByLabelText('Phone number')).toHaveValue('(11) 98765-4321')
  })

  it('fills editable address details from ViaCEP after an eight-digit CEP', async () => {
    const user = userEvent.setup()
    vi.mocked(lookupViaCEP).mockResolvedValue({
      status: 'found',
      address: { street: 'Praça da Sé', district: 'Sé', city: 'São Paulo', state: 'SP' },
    })
    renderPage()

    await screen.findByLabelText('Postal code')
    await user.type(screen.getByLabelText('Postal code'), '01001000')

    await waitFor(() => expect(lookupViaCEP).toHaveBeenCalledWith('01001000', expect.any(AbortSignal)))
    expect(await screen.findByDisplayValue('Praça da Sé')).toBeInTheDocument()
    expect(screen.getByLabelText('District')).toHaveValue('Sé')
    expect(screen.getByLabelText('City')).toHaveValue('São Paulo')
    expect(screen.getByLabelText('State')).toHaveValue('SP')
    expect(screen.getByText('Address details filled from the postal code. Review them before submitting.')).toBeInTheDocument()
  })
})
