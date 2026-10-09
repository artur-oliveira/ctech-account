import {cleanup, render, screen, waitFor} from '@testing-library/react'
import {QueryClient, QueryClientProvider} from '@tanstack/react-query'
import {AxiosError, AxiosHeaders} from 'axios'
import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import SpacePeoplePage from './page'
import {fetchHandoff, fetchOrganization} from '@/lib/queries'
import type {Organization, OrganizationRole} from '@/lib/types'

// The tabs are the organization page's own, tested there with a space. This
// test is about the page around them.
vi.mock('@/app/account/organizations/detail/members-tab', () => ({
  MembersTab: ({organization}: {organization: Organization}) => <div>members of {organization.kind}</div>,
}))
vi.mock('@/app/account/organizations/detail/invitations-tab', () => ({
  InvitationsTab: () => <div>invitations</div>,
}))
vi.mock('@/app/account/organizations/detail/settings-tab', () => ({
  SettingsTab: () => <div>settings</div>,
}))

vi.mock('@/lib/queries', () => ({
  fetchOrganization: vi.fn(),
  fetchHandoff: vi.fn(),
}))

const replace = vi.fn()
let search = new URLSearchParams()

vi.mock('next/navigation', () => ({
  useRouter: () => ({replace, push: vi.fn()}),
  useSearchParams: () => search,
}))

afterEach(cleanup)

function space(role: OrganizationRole): Organization {
  return {
    id: 'org_s', display_name: 'Casa', owner_user_id: 'usr_owner',
    role, kind: 'personal', joined_at: new Date().toISOString(),
  }
}

function renderPage(query: string) {
  search = new URLSearchParams(query)
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}})
  return render(
    <QueryClientProvider client={client}>
      <SpacePeoplePage/>
    </QueryClientProvider>,
  )
}

describe('space people', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(fetchHandoff).mockResolvedValue({
      client_name: 'Billing',
      return_to: 'https://billing.example/espacos',
    })
  })

  it('gives the owner the roster, the invitations and the settings', async () => {
    vi.mocked(fetchOrganization).mockResolvedValue(space('owner'))
    renderPage('id=org_s')

    expect(await screen.findByRole('heading', {name: 'Casa'})).toBeInTheDocument()
    expect(screen.getByRole('tab', {name: /people/i})).toBeInTheDocument()
    expect(screen.getByRole('tab', {name: /invitations/i})).toBeInTheDocument()
    expect(screen.getByRole('tab', {name: /settings/i})).toBeInTheDocument()
    expect(screen.getByText('members of personal')).toBeInTheDocument()
    // A space never has a company.
    expect(screen.queryByRole('tab', {name: /compan/i})).toBeNull()
    expect(screen.getByRole('link', {name: /all spaces/i})).toHaveAttribute('href', '/account/spaces')
  })

  // Only the owner acts. Everybody else reads the roster and is told why
  // there is nothing to press, rather than left to wonder.
  it('gives anybody else the read-only roster and says so', async () => {
    vi.mocked(fetchOrganization).mockResolvedValue(space('member'))
    renderPage('id=org_s')

    expect(await screen.findByText(/only the owner of this space/i)).toBeInTheDocument()
    expect(screen.queryByRole('tab', {name: /invitations/i})).toBeNull()
    expect(screen.getByText('Full access')).toBeInTheDocument()
  })

  // The way back goes through the URL the server validated, with the state
  // echoed — never the raw return_to in the address bar.
  it('offers the way back to the product that sent the person', async () => {
    vi.mocked(fetchOrganization).mockResolvedValue(space('owner'))
    renderPage('id=org_s&client_id=billing&return_to=https://billing.example/raw&state=abc123')

    const back = await screen.findByRole('link', {name: /back to billing/i})
    const url = new URL(back.getAttribute('href')!)
    expect(url.origin + url.pathname).toBe('https://billing.example/espacos')
    expect(url.searchParams.get('state')).toBe('abc123')
    expect(fetchHandoff).toHaveBeenCalledWith('billing', 'https://billing.example/raw', 'abc123')
  })

  // A refused handoff offers no way back anywhere — but the people are still
  // the person's to manage, so the page itself stays.
  it('offers no way back when the handoff is refused', async () => {
    vi.mocked(fetchOrganization).mockResolvedValue(space('owner'))
    vi.mocked(fetchHandoff).mockRejectedValue(new Error('422'))
    renderPage('id=org_s&client_id=billing&return_to=https://evil.example/x')

    expect(await screen.findByRole('heading', {name: 'Casa'})).toBeInTheDocument()
    await waitFor(() => expect(fetchHandoff).toHaveBeenCalled())
    expect(screen.queryByRole('link', {name: /back to/i})).toBeNull()
  })

  it('asks the server nothing about a handoff when there is none', async () => {
    vi.mocked(fetchOrganization).mockResolvedValue(space('owner'))
    renderPage('id=org_s')
    await screen.findByRole('heading', {name: 'Casa'})
    expect(fetchHandoff).not.toHaveBeenCalled()
  })

  // An organization id typed into this URL goes to the organization's page:
  // this screen has no companies tab and calls everything a space.
  it('passes an organization on to its own page', async () => {
    // An absent kind is an organization.
    vi.mocked(fetchOrganization).mockResolvedValue({...space('owner'), kind: undefined})
    renderPage('id=org_s')
    await waitFor(() =>
      expect(replace).toHaveBeenCalledWith('/account/organizations/detail?id=org_s'),
    )
    expect(screen.queryByRole('heading', {name: 'Casa'})).toBeNull()
  })

  // The server refuses to say whether a space exists or the person is not in
  // it, so this screen does not either.
  it('says the same thing for a missing space and a refused one', async () => {
    vi.mocked(fetchOrganization).mockRejectedValue(
      new AxiosError('forbidden', '403', undefined, undefined, {
        status: 403, statusText: 'Forbidden', data: {}, headers: {}, config: {headers: new AxiosHeaders()},
      }),
    )
    renderPage('id=org_x')
    expect(await screen.findByText(/do not have access to this space/i)).toBeInTheDocument()
  })
})
