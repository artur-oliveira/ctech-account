import {cleanup, render, screen, waitFor, within} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {QueryClient, QueryClientProvider} from '@tanstack/react-query'
import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import {InvitationsTab} from './invitations-tab'
import {fetchCompanies, fetchOrganizationInvitations, fetchSpaceUsage} from '@/lib/queries'
import {AxiosError, AxiosHeaders} from 'axios'
import {inviteMemberAPI} from '@/lib/mutations'
import type {Organization, OrganizationRole} from '@/lib/types'

vi.mock('@/lib/queries', () => ({
  fetchOrganizationInvitations: vi.fn(),
  fetchCompanies: vi.fn(),
  fetchSpaceUsage: vi.fn(),
}))

vi.mock('@/lib/env', async (orig) => ({...(await orig<object>()), BILLING_URL: 'https://billing.example'}))

vi.mock('@/lib/mutations', () => ({
  inviteMemberAPI: vi.fn(),
  revokeInvitationAPI: vi.fn(),
}))

afterEach(cleanup)

function refusal(status: number, data: unknown) {
  return new AxiosError('x', String(status), undefined, undefined, {
    status, data, statusText: '', headers: {}, config: {headers: new AxiosHeaders()},
  })
}

function ownedSpace(): Organization {
  return {...organization('owner'), id: 'spc_1', display_name: 'Casa', kind: 'personal'}
}


function organization(role: OrganizationRole): Organization {
  return {
    id: 'org_1', display_name: 'Contabilidade', owner_user_id: 'usr_owner',
    role, joined_at: new Date().toISOString(),
  }
}

function renderTab(workspace: Organization = organization('owner')) {
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}})
  return render(
    <QueryClientProvider client={client}>
      <InvitationsTab organization={workspace}/>
    </QueryClientProvider>,
  )
}

describe('invitations tab', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(fetchOrganizationInvitations).mockResolvedValue([])
    vi.mocked(fetchSpaceUsage).mockResolvedValue({people: 0, pending_invitations: 0, limit: -1})
    vi.mocked(fetchCompanies).mockResolvedValue([
      {id: 'cmp_1', tax_id: '11222333000181', tax_id_kind: 'cnpj', legal_name: 'Acme LTDA', created_at: new Date().toISOString()},
      {id: 'cmp_2', tax_id: '12ABC34501DE35', tax_id_kind: 'cnpj', legal_name: 'Beta LTDA', created_at: new Date().toISOString()},
    ])
    vi.mocked(inviteMemberAPI).mockResolvedValue({token: 'tok', email: 'j@example.com', role: 'member'})
  })

  // The case that pays for this: an accountant invites a junior who should
  // reach some of the companies, not all of them.
  it('sends only the companies that were ticked', async () => {
    const user = userEvent.setup()
    renderTab()
    await user.click(await screen.findByRole('button', {name: /invite/i}))
    await user.type(screen.getByLabelText(/e-?mail/i), 'junior@example.com')

    const dialog = within(screen.getByRole('dialog'))
    await user.click(dialog.getByRole('checkbox', {name: /Acme LTDA/}))
    await user.click(dialog.getByRole('button', {name: /create invitation|criar convite/i}))

    await waitFor(() =>
      expect(inviteMemberAPI).toHaveBeenCalledWith('org_1', 'junior@example.com', 'member', ['cmp_1']),
    )
    expect(fetchSpaceUsage).not.toHaveBeenCalled()
  })

  // Empty is valid and must stay possible: a bookkeeper who only reads
  // invoices. Requiring a company would make the common case carry the
  // accountant's problem.
  it('allows an invitation with no company', async () => {
    const user = userEvent.setup()
    renderTab()
    await user.click(await screen.findByRole('button', {name: /invite/i}))
    await user.type(screen.getByLabelText(/e-?mail/i), 'leitor@example.com')
    await user.click(within(screen.getByRole('dialog')).getByRole('button', {name: /create invitation|criar convite/i}))

    await waitFor(() =>
      expect(inviteMemberAPI).toHaveBeenCalledWith('org_1', 'leitor@example.com', 'member', []),
    )
  })

  // And it says so. Somebody invited with no company joins and can act for
  // nothing; silence there is a person who cannot work with nothing on screen
  // explaining why.
  it('says what an invitation with no company means', async () => {
    const user = userEvent.setup()
    renderTab()
    await user.click(await screen.findByRole('button', {name: /invite/i}))
    const dialog = within(screen.getByRole('dialog'))
    expect(dialog.getByText(/not be able to act|não poderá agir/i)).toBeInTheDocument()

    await user.click(dialog.getByRole('checkbox', {name: /Acme LTDA/}))
    expect(dialog.queryByText(/not be able to act|não poderá agir/i)).toBeNull()
  })

  // An organization with no companies shows no picker at all, rather than an
  // empty box and a warning about a choice that does not exist.
  it('shows no company picker when there are none', async () => {
    vi.mocked(fetchCompanies).mockResolvedValue([])
    const user = userEvent.setup()
    renderTab()
    await user.click(await screen.findByRole('button', {name: /invite/i}))
    expect(within(screen.getByRole('dialog')).queryAllByRole('checkbox')).toHaveLength(0)
  })

  it('keeps a long company name inside one accessible checkbox row', async () => {
    vi.mocked(fetchCompanies).mockResolvedValue([
      {
        id: 'cmp_long', tax_id: '12ABC34501DE35', tax_id_kind: 'cnpj',
        legal_name: 'COMPANHIA'.repeat(30), created_at: new Date().toISOString(),
      },
    ])
    const user = userEvent.setup()
    renderTab()
    await user.click(await screen.findByRole('button', {name: /invite/i}))

    const checkbox = within(screen.getByRole('dialog')).getByRole('checkbox', {name: /COMPANHIA/i})
    expect(checkbox.closest('label')).toHaveClass('min-w-0', 'grid')
  })

  it('blocks submission and offers retry when companies cannot be loaded', async () => {
    vi.mocked(fetchCompanies).mockRejectedValue(new Error('offline'))
    const user = userEvent.setup()
    renderTab()
    await user.click(await screen.findByRole('button', {name: /invite/i}))
    const dialog = within(screen.getByRole('dialog'))

    expect(await dialog.findByRole('button', {name: /retry|try again/i})).toBeInTheDocument()
    expect(dialog.getByRole('button', {name: /create invitation/i})).toBeDisabled()
  })
})

describe('inviting into a space', () => {
  const space: Organization = {...organization('owner'), kind: 'personal'}

  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(fetchOrganizationInvitations).mockResolvedValue([])
    vi.mocked(inviteMemberAPI).mockResolvedValue({token: 'tok', email: 'a@example.com', role: 'viewer'})
  })

  // A space never has a company. Asking for its companies would be a 409, and
  // a picker would be a question with no possible answer.
  it('never asks for companies', async () => {
    const user = userEvent.setup()
    renderTab(space)
    await user.click(await screen.findByRole('button', {name: /invite/i}))

    const dialog = within(screen.getByRole('dialog'))
    expect(dialog.queryAllByRole('checkbox')).toHaveLength(0)
    expect(dialog.queryByText(/compan/i)).toBeNull()
    expect(fetchCompanies).not.toHaveBeenCalled()
  })

  it('offers full access and read only, and sends the choice', async () => {
    const user = userEvent.setup()
    renderTab(space)
    await user.click(await screen.findByRole('button', {name: /invite/i}))
    await user.type(screen.getByLabelText(/e-?mail/i), 'mae@example.com')

    await user.click(screen.getByRole('combobox', {name: /access/i}))
    expect(await screen.findByRole('option', {name: /full access/i})).toBeInTheDocument()
    expect(screen.queryByRole('option', {name: /admin/i})).toBeNull()
    await user.click(screen.getByRole('option', {name: /read only/i}))
    await user.click(within(screen.getByRole('dialog')).getByRole('button', {name: /create invitation/i}))

    await waitFor(() =>
      expect(inviteMemberAPI).toHaveBeenCalledWith('org_1', 'mae@example.com', 'viewer', []),
    )
  })

  it('shows people + pending of the plan beside the invite button', async () => {
    vi.mocked(fetchSpaceUsage).mockResolvedValue({people: 3, pending_invitations: 1, limit: 5, plan: 'basic'})
    renderTab(ownedSpace())
    expect(await screen.findByText('3 + 1 of 5 people')).toBeInTheDocument()
  })

  it('hides the counter when the plan cannot be read, and keeps the list', async () => {
    vi.mocked(fetchSpaceUsage).mockRejectedValue(refusal(503, {code: 'plan_unavailable'}))
    vi.mocked(fetchOrganizationInvitations).mockResolvedValue([
      {email: 'a@example.com', role: 'member', invited_by: 'usr_owner', expires_at: new Date().toISOString()},
    ])
    renderTab(ownedSpace())
    expect((await screen.findAllByText('a@example.com')).length).toBeGreaterThan(0)
    expect(screen.queryByText(/people$/)).toBeNull()
  })

  it('explains a refused invitation inline, with the plans', async () => {
    vi.mocked(fetchSpaceUsage).mockResolvedValue({people: 4, pending_invitations: 1, limit: 5, plan: 'basic'})
    vi.mocked(inviteMemberAPI).mockRejectedValue(
      refusal(402, {code: 'plan_limit', resource: 'people', limit: 5, used: 5, plan: 'basic'}))
    const user = userEvent.setup()
    renderTab(ownedSpace())
    await user.click(await screen.findByRole('button', {name: /invite/i}))
    await user.type(screen.getByLabelText(/e-?mail/i), 'f@example.com')
    await user.click(within(screen.getByRole('dialog')).getByRole('button', {name: /create invitation|criar convite/i}))

    const dialog = within(screen.getByRole('dialog'))
    expect(await dialog.findByText('This space already has 5 of 5 people on your plan.')).toBeInTheDocument()
    expect(dialog.getByRole('link', {name: /see plans/i})).toHaveAttribute('href', 'https://billing.example/finance/plans')
  })
})
