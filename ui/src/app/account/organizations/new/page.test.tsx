import {cleanup, render, screen, waitFor} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {QueryClient, QueryClientProvider} from '@tanstack/react-query'
import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import NewOrganizationPage from './page'
import {fetchHandoff, lookupTaxID} from '@/lib/queries'
import {createOrganizationAPI, registerCompanyAPI} from '@/lib/mutations'

vi.mock('@/lib/queries', () => ({
  fetchHandoff: vi.fn(),
  lookupTaxID: vi.fn(),
}))

vi.mock('@/lib/mutations', () => ({
  createOrganizationAPI: vi.fn(),
  registerCompanyAPI: vi.fn(),
}))

const push = vi.fn()
let search = new URLSearchParams()

vi.mock('next/navigation', () => ({
  useRouter: () => ({push}),
  useSearchParams: () => search,
}))

afterEach(cleanup)

// The page leaves through window.location.assign, which jsdom does not
// implement. Captured rather than stubbed away, because the exact URL is the
// contract with the product that sent us.
let assigned: string | null = null

function renderPage(query: string) {
  search = new URLSearchParams(query)
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}})
  return render(
    <QueryClientProvider client={client}>
      <NewOrganizationPage/>
    </QueryClientProvider>,
  )
}

describe('new organization', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    assigned = null
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: {assign: (url: string) => { assigned = url }},
    })
    vi.mocked(lookupTaxID).mockResolvedValue(null)
    vi.mocked(createOrganizationAPI).mockResolvedValue({
      id: 'org_new', display_name: 'CTech', owner_user_id: 'usr_1',
      role: 'owner', joined_at: new Date().toISOString(),
    })
    vi.mocked(registerCompanyAPI).mockResolvedValue({
      id: 'cmp_new', tax_id: '11222333000181', tax_id_kind: 'cnpj',
      legal_name: 'Acme LTDA', created_at: new Date().toISOString(),
    })
    vi.mocked(fetchHandoff).mockResolvedValue({
      client_name: 'DF-e',
      return_to: 'https://dfe.example/empresas/vincular',
    })
  })

  // Without handoff parameters this is the plain create screen. It is a real
  // route people can reach directly, and it must not break when they do.
  it('is the plain create screen without handoff parameters', async () => {
    renderPage('')
    expect(await screen.findByLabelText(/organization name/i)).toBeInTheDocument()
    expect(fetchHandoff).not.toHaveBeenCalled()
    // The tax id is optional here: somebody creating a workspace from their own
    // account is not necessarily registering a company yet.
    expect(screen.getByLabelText(/optional/i)).not.toBeRequired()
  })

  it('sends a direct visitor to the organization it created', async () => {
    const user = userEvent.setup()
    renderPage('')
    await user.type(await screen.findByLabelText(/organization name/i), 'CTech')
    await user.click(screen.getByRole('button', {name: /create organization/i}))
    await waitFor(() => expect(push).toHaveBeenCalledWith('/account/organizations/detail?id=org_new'))
  })

  // The name in the banner comes from the server. A client_name in the query
  // string is a banner anybody can make say whatever they like.
  it('names the product from the server, never from the query string', async () => {
    renderPage('client_id=dfe&return_to=https://dfe.example/x&client_name=Banco%20Falso')
    expect(await screen.findByText(/DF-e/)).toBeInTheDocument()
    expect(screen.queryByText(/Banco Falso/)).toBeNull()
  })

  // The whole point of the round trip: both ids and the echoed state, on the
  // URL the server validated — not the one in the address bar.
  it('returns both ids to the echoed return_to', async () => {
    const user = userEvent.setup()
    renderPage('client_id=dfe&return_to=https://dfe.example/raw&state=abc123')
    await user.type(await screen.findByLabelText(/organization name/i), 'CTech')
    await user.type(screen.getByLabelText(/cnpj/i), '11222333000181')
    await user.type(screen.getByLabelText(/legal name/i), 'Acme LTDA')
    await user.click(screen.getByRole('button', {name: /create organization/i}))

    await waitFor(() => expect(assigned).not.toBeNull())
    const url = new URL(assigned!)
    // The echoed host, not the raw one: whatever the server validated is what
    // the browser follows.
    expect(url.pathname).toBe('/empresas/vincular')
    expect(url.searchParams.get('organization_id')).toBe('org_new')
    expect(url.searchParams.get('company_id')).toBe('cmp_new')
    expect(url.searchParams.get('state')).toBe('abc123')
  })

  // Cancel is a real action, not a back button: the product has to be told, or
  // it cannot put the person back where they were.
  it('tells the product when somebody backs out', async () => {
    const user = userEvent.setup()
    renderPage('client_id=dfe&return_to=https://dfe.example/x&state=abc123')
    await screen.findByLabelText(/organization name/i)
    await user.click(screen.getByRole('button', {name: /cancel/i}))

    await waitFor(() => expect(assigned).not.toBeNull())
    const url = new URL(assigned!)
    expect(url.searchParams.get('cancelled')).toBe('1')
    expect(url.searchParams.get('state')).toBe('abc123')
    expect(url.searchParams.get('organization_id')).toBeNull()
  })

  // A misconfigured integration must strand nobody: no redirect anywhere, and
  // a way to do this from the account instead.
  it('strands nobody when the handoff is refused', async () => {
    vi.mocked(fetchHandoff).mockRejectedValue(new Error('422'))
    renderPage('client_id=dfe&return_to=https://evil.example/x')
    expect(await screen.findByRole('link', {name: /organizations/i})).toHaveAttribute(
      'href', '/account/organizations',
    )
    expect(assigned).toBeNull()
    expect(screen.queryByLabelText(/organization name/i)).toBeNull()
  })

  // A handoff exists to produce a company. Letting it finish without one would
  // send the product an empty company_id it cannot use.
  it('requires the tax id in a handoff', async () => {
    renderPage('client_id=dfe&return_to=https://dfe.example/x')
    await screen.findByLabelText(/organization name/i)
    expect(screen.getByLabelText(/cnpj/i)).toBeRequired()
  })
})
