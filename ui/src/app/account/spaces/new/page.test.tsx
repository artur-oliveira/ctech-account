import {cleanup, render, screen, waitFor} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {QueryClient, QueryClientProvider} from '@tanstack/react-query'
import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import {AxiosError, AxiosHeaders} from 'axios'
import NewSpacePage from './page'
import {fetchHandoff} from '@/lib/queries'
import {createOrganizationAPI} from '@/lib/mutations'

vi.mock('@/lib/queries', () => ({fetchHandoff: vi.fn()}))
vi.mock('@/lib/mutations', () => ({createOrganizationAPI: vi.fn()}))
vi.mock('@/lib/env', async (orig) => ({...(await orig<object>()), BILLING_URL: 'https://billing.example'}))

function refusal(status: number, data: unknown) {
  return new AxiosError('x', String(status), undefined, undefined, {
    status, data, statusText: '', headers: {}, config: {headers: new AxiosHeaders()},
  })
}


const push = vi.fn()
let search = new URLSearchParams()

vi.mock('next/navigation', () => ({
  useRouter: () => ({push}),
  useSearchParams: () => search,
}))

afterEach(cleanup)

// The page leaves through window.location.replace, which jsdom does not
// implement. Captured rather than stubbed away: the exact URL is the contract
// with the product that sent us.
let replaced: string | null = null

function renderPage(query: string) {
  search = new URLSearchParams(query)
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}})
  return render(
    <QueryClientProvider client={client}>
      <NewSpacePage/>
    </QueryClientProvider>,
  )
}

describe('new space', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    replaced = null
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: {replace: (url: string) => { replaced = url }},
    })
    vi.mocked(createOrganizationAPI).mockResolvedValue({
      id: 'org_space', display_name: 'Casa', owner_user_id: 'usr_1',
      role: 'owner', kind: 'personal', joined_at: new Date().toISOString(),
    })
    vi.mocked(fetchHandoff).mockResolvedValue({
      client_name: 'Billing',
      return_to: 'https://billing.example/espacos/vincular',
    })
  })

  // The name and nothing else: no tax id, no company, none of an
  // organization's questions.
  it('asks for the name only', async () => {
    renderPage('')
    expect(await screen.findByLabelText(/space name/i)).toBeInTheDocument()
    expect(screen.getAllByRole('textbox')).toHaveLength(1)
    expect(screen.queryByText(/cnpj|company|organization/i)).toBeNull()
    expect(fetchHandoff).not.toHaveBeenCalled()
  })

  it('creates a personal workspace and goes to the spaces', async () => {
    const user = userEvent.setup()
    renderPage('')
    await user.type(await screen.findByLabelText(/space name/i), '  Casa  ')
    await user.click(screen.getByRole('button', {name: /create space/i}))

    await waitFor(() => expect(push).toHaveBeenCalledWith('/account/spaces'))
    expect(createOrganizationAPI).toHaveBeenCalledWith('Casa', 'personal')
    expect(replaced).toBeNull()
  })

  // The banner's product name comes from the server. A client_name in the
  // query string is a banner anybody can make say whatever they like.
  it('names the product from the server, never from the query string', async () => {
    renderPage('client_id=billing&return_to=https://billing.example/x&client_name=Banco%20Falso')
    expect(await screen.findByText(/creating a space for billing/i)).toBeInTheDocument()
    expect(screen.queryByText(/Banco Falso/)).toBeNull()
    expect(fetchHandoff).toHaveBeenCalledWith('billing', 'https://billing.example/x', '')
  })

  // The round trip: the new id and the echoed state, on the URL the server
  // validated — not the one in the address bar.
  it('returns the new id to the echoed return_to', async () => {
    const user = userEvent.setup()
    renderPage('client_id=billing&return_to=https://billing.example/raw&state=abc123')
    await user.type(await screen.findByLabelText(/space name/i), 'Casa')
    await user.click(screen.getByRole('button', {name: /create space/i}))

    await waitFor(() => expect(replaced).not.toBeNull())
    const url = new URL(replaced!)
    expect(url.origin + url.pathname).toBe('https://billing.example/espacos/vincular')
    expect(url.searchParams.get('organization_id')).toBe('org_space')
    expect(url.searchParams.get('state')).toBe('abc123')
    // A space has no company, so the product is never handed an empty one.
    expect(url.searchParams.has('company_id')).toBe(false)
    expect(push).not.toHaveBeenCalled()
  })

  // Cancel is a real action: the product has to be told, or it cannot put the
  // person back where they were.
  it('tells the product when somebody backs out', async () => {
    const user = userEvent.setup()
    renderPage('client_id=billing&return_to=https://billing.example/x&state=abc123')
    await screen.findByLabelText(/space name/i)
    await user.click(screen.getByRole('button', {name: /cancel/i}))

    await waitFor(() => expect(replaced).not.toBeNull())
    const url = new URL(replaced!)
    expect(url.searchParams.get('cancelled')).toBe('1')
    expect(url.searchParams.get('state')).toBe('abc123')
    expect(url.searchParams.get('organization_id')).toBeNull()
    expect(createOrganizationAPI).not.toHaveBeenCalled()
  })

  // A misconfigured integration strands nobody: no redirect anywhere, and a
  // way to do this from the account instead.
  it('strands nobody when the handoff is refused', async () => {
    vi.mocked(fetchHandoff).mockRejectedValue(new Error('422'))
    renderPage('client_id=billing&return_to=https://evil.example/x')
    expect(await screen.findByRole('link', {name: /my spaces/i})).toHaveAttribute(
      'href', '/account/spaces',
    )
    expect(screen.queryByLabelText(/space name/i)).toBeNull()
    expect(replaced).toBeNull()
  })

  it('replaces the form with the plan limit and a way to the plans', async () => {
    vi.mocked(createOrganizationAPI).mockRejectedValue(
      refusal(402, {code: 'plan_limit', resource: 'spaces', limit: 3, used: 3, plan: 'basic'}))
    const user = userEvent.setup()
    renderPage('client_id=billing&return_to=https://billing.example/x&state=abc123')
    await user.type(await screen.findByLabelText(/space name/i), 'Casa')
    await user.click(screen.getByRole('button', {name: /create space/i}))

    expect(await screen.findByText('Your plan allows 3 spaces and you already have 3.')).toBeInTheDocument()
    expect(screen.queryByLabelText(/space name/i)).toBeNull()
    expect(screen.getByRole('link', {name: /see plans/i})).toHaveAttribute('href', 'https://billing.example/finance/plano')

    await user.click(screen.getByRole('button', {name: /^back$/i}))
    await waitFor(() => expect(replaced).not.toBeNull())
    const url = new URL(replaced!)
    expect(url.searchParams.get('cancelled')).toBe('1')
    expect(url.searchParams.get('state')).toBe('abc123')
  })

  it('says one space in the singular', async () => {
    vi.mocked(createOrganizationAPI).mockRejectedValue(
      refusal(402, {code: 'plan_limit', resource: 'spaces', limit: 1, used: 1, plan: 'free'}))
    const user = userEvent.setup()
    renderPage('')
    await user.type(await screen.findByLabelText(/space name/i), 'Casa')
    await user.click(screen.getByRole('button', {name: /create space/i}))
    expect(await screen.findByText('Your plan allows 1 space and you already have 1.')).toBeInTheDocument()
    expect(screen.getByRole('link', {name: /^back$/i})).toHaveAttribute('href', '/account/spaces')
  })

  it('keeps the form when the plan cannot be read', async () => {
    vi.mocked(createOrganizationAPI).mockRejectedValue(refusal(503, {code: 'plan_unavailable'}))
    const user = userEvent.setup()
    renderPage('')
    await user.type(await screen.findByLabelText(/space name/i), 'Casa')
    await user.click(screen.getByRole('button', {name: /create space/i}))
    expect(await screen.findByText(/couldn't check your plan right now/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/space name/i)).toBeInTheDocument()
  })
})
