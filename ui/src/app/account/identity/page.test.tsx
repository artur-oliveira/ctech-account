import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it, vi, beforeEach } from 'vitest'
import IdentityPage from './page'
import { fetchKYC, fetchPasskeys, fetchTOTPStatus } from '@/lib/queries'
import { submitKYCAPI } from '@/lib/mutations'
import { lookupZipCode } from '@/lib/viacep'
import { REQUIRED_DOC_TYPES } from '@/lib/constants'
import type { Address, KYCStatus } from '@/lib/types'

vi.mock('@/lib/queries', () => ({
  fetchKYC: vi.fn(),
  fetchTOTPStatus: vi.fn(),
  fetchPasskeys: vi.fn(),
}))

vi.mock('@/lib/mutations', () => ({
  submitKYCAPI: vi.fn(),
}))

vi.mock('@/lib/viacep', async () => {
  const actual = await vi.importActual<typeof import('@/lib/viacep')>('@/lib/viacep')
  return { ...actual, lookupZipCode: vi.fn() }
})

const READY_STATUS: KYCStatus = {
  state: 'awaiting_files',
  level: '',
  documents: REQUIRED_DOC_TYPES.map((type) => ({ id: type, type, uploaded_at: '2026-01-01T00:00:00Z' })),
}

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <IdentityPage />
    </QueryClientProvider>,
  )
}

async function fillCPFAndName(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByLabelText('CPF'), '11144477735')
  await user.type(screen.getByLabelText('Full legal name'), 'Jane Doe')
}

describe('IdentityPage — details step', () => {
  beforeEach(() => {
    vi.mocked(fetchKYC).mockResolvedValue(READY_STATUS)
    vi.mocked(fetchTOTPStatus).mockResolvedValue({ enabled: true })
    vi.mocked(fetchPasskeys).mockResolvedValue([])
  })

  it('blocks Review with an underage error for a birth date under the age minimum', async () => {
    const user = userEvent.setup()
    renderPage()

    await screen.findByLabelText('CPF')
    await fillCPFAndName(user)
    const seventeenYearsAgo = new Date()
    seventeenYearsAgo.setUTCFullYear(seventeenYearsAgo.getUTCFullYear() - 17)
    await user.type(screen.getByLabelText('Date of birth'), seventeenYearsAgo.toISOString().slice(0, 10))

    await user.click(screen.getByRole('button', { name: 'Review details' }))

    expect(await screen.findByText('You must be at least 18 years old.')).toBeInTheDocument()
    // Regression: an underage submitter must be blocked before reaching the
    // point-of-no-return review/confirm step, not after.
    expect(screen.queryByText('Submit identity verification?')).not.toBeInTheDocument()
  })

  it('allows Review for a birth date exactly at the age minimum', async () => {
    const user = userEvent.setup()
    renderPage()

    await screen.findByLabelText('CPF')
    await fillCPFAndName(user)
    const eighteenYearsAgo = new Date()
    eighteenYearsAgo.setUTCFullYear(eighteenYearsAgo.getUTCFullYear() - 18)
    await user.type(screen.getByLabelText('Date of birth'), eighteenYearsAgo.toISOString().slice(0, 10))

    await user.type(screen.getByLabelText('Postal code (CEP)'), '01001000')
    await user.type(screen.getByLabelText('Number'), '100')
    await user.type(screen.getByLabelText('Street'), 'Praça da Sé')
    await user.type(screen.getByLabelText('District'), 'Sé')
    await user.type(screen.getByLabelText('City'), 'São Paulo')

    await user.click(screen.getByRole('button', { name: 'Review details' }))

    expect(screen.queryByText('You must be at least 18 years old.')).not.toBeInTheDocument()
  })

  it('discards a stale ViaCEP response that resolves after a newer lookup', async () => {
    const user = userEvent.setup()
    renderPage()
    await screen.findByLabelText('CPF')

    let resolveFirst!: (value: Partial<Address> | null) => void
    let resolveSecond!: (value: Partial<Address> | null) => void
    vi.mocked(lookupZipCode)
      .mockImplementationOnce(() => new Promise((resolve) => { resolveFirst = resolve }))
      .mockImplementationOnce(() => new Promise((resolve) => { resolveSecond = resolve }))

    const zipInput = screen.getByLabelText('Postal code (CEP)')
    await user.type(zipInput, '01001000')
    await user.clear(zipInput)
    await user.type(zipInput, '20040020')

    // Second (newer) lookup resolves first; first (stale) lookup resolves after.
    resolveSecond({ zip_code: '20040020', street: 'Second St', district: 'Second', city: 'Rio de Janeiro', state: 'RJ' })
    await waitFor(() => expect(screen.getByLabelText('Street')).toHaveValue('Second St'))

    resolveFirst({ zip_code: '01001000', street: 'First St', district: 'First', city: 'São Paulo', state: 'SP' })

    // Regression: the stale first response must not overwrite the newer address.
    await new Promise((r) => setTimeout(r, 0))
    expect(screen.getByLabelText('Street')).toHaveValue('Second St')
  })

  it('announces a successful ViaCEP lookup for screen-reader users', async () => {
    const user = userEvent.setup()
    renderPage()
    await screen.findByLabelText('CPF')

    vi.mocked(lookupZipCode).mockResolvedValue({
      zip_code: '01001000',
      street: 'Praça da Sé',
      district: 'Sé',
      city: 'São Paulo',
      state: 'SP',
    })

    // Regression: a successful lookup used to update fields silently — only
    // the not-found case had any announcement (via the role="alert" Alert).
    await user.type(screen.getByLabelText('Postal code (CEP)'), '01001000')

    expect(await screen.findByText('Address found for 01001-000.')).toBeInTheDocument()
  })

  it('shows a generic failure message instead of "invalid data" for an unmapped submit error', async () => {
    const user = userEvent.setup()
    vi.mocked(submitKYCAPI).mockRejectedValue(new Error('network down'))
    renderPage()

    await screen.findByLabelText('CPF')
    await fillCPFAndName(user)
    const eighteenYearsAgo = new Date()
    eighteenYearsAgo.setUTCFullYear(eighteenYearsAgo.getUTCFullYear() - 18)
    await user.type(screen.getByLabelText('Date of birth'), eighteenYearsAgo.toISOString().slice(0, 10))
    await user.type(screen.getByLabelText('Postal code (CEP)'), '01001000')
    await user.type(screen.getByLabelText('Number'), '100')
    await user.type(screen.getByLabelText('Street'), 'Praça da Sé')
    await user.type(screen.getByLabelText('District'), 'Sé')
    await user.type(screen.getByLabelText('City'), 'São Paulo')

    await user.click(screen.getByRole('button', { name: 'Review details' }))
    await user.click(screen.getByRole('button', { name: 'Confirm and submit' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: 'Confirm and submit' }))

    // Regression: an unmapped error (network/500) used to fall back to
    // `identity.invalidData` ("Check the data and try again."), which is
    // reserved for the validation-failed slug and misreports server failures.
    expect(await screen.findByText('Something went wrong. Try again.')).toBeInTheDocument()
    expect(screen.queryByText('Check the data and try again.')).not.toBeInTheDocument()
  })
})
