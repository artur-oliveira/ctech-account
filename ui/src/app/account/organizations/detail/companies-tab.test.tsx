import {cleanup, render, screen, within} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {QueryClient, QueryClientProvider} from '@tanstack/react-query'
import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import {CompaniesTab} from './companies-tab'
import {fetchCompanies, lookupTaxID} from '@/lib/queries'
import {registerCompanyAPI} from '@/lib/mutations'
import type {Organization, OrganizationRole} from '@/lib/types'

vi.mock('@/lib/queries', () => ({
  fetchCompanies: vi.fn(),
  lookupTaxID: vi.fn(),
}))

vi.mock('@/lib/mutations', () => ({
  registerCompanyAPI: vi.fn(),
}))

afterEach(cleanup)

function organization(role: OrganizationRole): Organization {
  return {
    id: 'org_1',
    display_name: 'CTech',
    owner_user_id: 'usr_owner',
    role,
    joined_at: new Date().toISOString(),
  }
}

function renderTab(role: OrganizationRole) {
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}})
  return render(
    <QueryClientProvider client={client}>
      <CompaniesTab organization={organization(role)}/>
    </QueryClientProvider>,
  )
}


// ResponsiveDataList renders the mobile cards and the desktop table at once, so
// every unscoped text query finds two of everything. Scope to the table.
async function seeded() {
  return within(await screen.findByRole('table')).getByText('Acme LTDA')
}

describe('companies tab', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(lookupTaxID).mockResolvedValue(null)
    vi.mocked(fetchCompanies).mockResolvedValue([
      {
        id: 'cmp_1',
        tax_id: '11222333000181',
        tax_id_kind: 'cnpj',
        legal_name: 'Acme LTDA',
        trade_name: 'Acme',
        created_at: new Date().toISOString(),
      },
    ])
  })

  // The stored value is canonical; a person reads a mask. Showing the raw form
  // makes them count characters to check they typed the right company.
  it('shows the tax id masked', async () => {
    renderTab('admin')
    const table = within(await screen.findByRole('table'))
    expect(table.getByText('11.222.333/0001-81')).toBeInTheDocument()
  })

  // A CNPJ has been alphanumeric in its first twelve positions since 2026. A
  // mask that assumed digits would mangle it.
  it('masks an alphanumeric CNPJ', async () => {
    vi.mocked(fetchCompanies).mockResolvedValue([
      {
        id: 'cmp_2',
        tax_id: '12ABC34501DE35',
        tax_id_kind: 'cnpj',
        legal_name: 'Beta LTDA',
        created_at: new Date().toISOString(),
      },
    ])
    renderTab('admin')
    const table = within(await screen.findByRole('table'))
    expect(table.getByText('12.ABC.345/01DE-35')).toBeInTheDocument()
  })

  // The control is absent, not disabled: the server refuses a viewer's write,
  // and a dead control invites the attempt.
  it('gives a viewer no way to register a company', async () => {
    renderTab('viewer')
    await seeded()
    expect(screen.queryByRole('button', {name: /add company/i})).toBeNull()
  })

  it('offers an admin the register control', async () => {
    renderTab('admin')
    await seeded()
    expect(screen.getByRole('button', {name: /add company/i})).toBeInTheDocument()
  })

  // The lookup fills the names and they stay editable — the register can be
  // wrong, and a person who cannot correct it is stuck.
  it('fills the names from the register and leaves them editable', async () => {
    const user = userEvent.setup()
    vi.mocked(lookupTaxID).mockResolvedValue({legal_name: 'ACME COMERCIO LTDA', trade_name: 'Acme'})
    renderTab('admin')
    await seeded()

    await user.click(screen.getByRole('button', {name: /add company/i}))
    const taxField = screen.getByLabelText(/cnpj/i)
    await user.type(taxField, '11.222.333/0001-81')
    await user.tab()

    const legal = await screen.findByDisplayValue('ACME COMERCIO LTDA')
    expect(legal).not.toBeDisabled()
    await user.clear(legal)
    await user.type(legal, 'Corrected LTDA')
    expect(screen.getByDisplayValue('Corrected LTDA')).toBeInTheDocument()
  })

  // A register that knows nothing is not an error. The form must stay usable
  // and say nothing about it.
  it('says nothing when the register has no answer', async () => {
    const user = userEvent.setup()
    vi.mocked(lookupTaxID).mockResolvedValue(null)
    renderTab('admin')
    await seeded()

    await user.click(screen.getByRole('button', {name: /add company/i}))
    await user.type(screen.getByLabelText(/cnpj/i), '11222333000181')
    await user.tab()

    expect(screen.queryByRole('alert')).toBeNull()
    expect(screen.getByLabelText(/legal name/i)).toHaveValue('')
  })

  // The empty state teaches what this is for. "Nothing here" teaches nothing.
  it('teaches the empty state', async () => {
    vi.mocked(fetchCompanies).mockResolvedValue([])
    renderTab('admin')
    expect(await screen.findByText(/issue documents for/i)).toBeInTheDocument()
  })

  it('sends the typed tax id to the server, which canonicalizes it', async () => {
    const user = userEvent.setup()
    vi.mocked(registerCompanyAPI).mockResolvedValue({
      id: 'cmp_new',
      tax_id: '11222333000181',
      tax_id_kind: 'cnpj',
      legal_name: 'New LTDA',
      created_at: new Date().toISOString(),
    })
    renderTab('admin')
    await seeded()

    await user.click(screen.getByRole('button', {name: /add company/i}))
    await user.type(screen.getByLabelText(/cnpj/i), '11.222.333/0001-81')
    await user.type(screen.getByLabelText(/legal name/i), 'New LTDA')
    await user.click(screen.getByRole('button', {name: /^add company$/i}))

    expect(registerCompanyAPI).toHaveBeenCalledWith('org_1', {
      tax_id: '11.222.333/0001-81',
      legal_name: 'New LTDA',
      trade_name: '',
    })
  })

  it('accepts and masks an alphanumeric CNPJ while typing', async () => {
    const user = userEvent.setup()
    renderTab('admin')
    await seeded()
    await user.click(screen.getByRole('button', {name: /add company/i}))

    const taxID = screen.getByLabelText(/cnpj/i)
    await user.type(taxID, '12abc34501de35')

    expect(taxID).toHaveValue('12.ABC.345/01DE-35')
    expect(taxID).toHaveAttribute('maxlength', '18')
  })
})
