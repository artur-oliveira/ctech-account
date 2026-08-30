import {cleanup, render, screen, waitFor, within} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {QueryClient, QueryClientProvider} from '@tanstack/react-query'
import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import {InvitationsTab} from './invitations-tab'
import {fetchCompanies, fetchOrganizationInvitations} from '@/lib/queries'
import {inviteMemberAPI} from '@/lib/mutations'
import type {Organization, OrganizationRole} from '@/lib/types'

vi.mock('@/lib/queries', () => ({
  fetchOrganizationInvitations: vi.fn(),
  fetchCompanies: vi.fn(),
}))

vi.mock('@/lib/mutations', () => ({
  inviteMemberAPI: vi.fn(),
  revokeInvitationAPI: vi.fn(),
}))

afterEach(cleanup)

function organization(role: OrganizationRole): Organization {
  return {
    id: 'org_1', display_name: 'Contabilidade', owner_user_id: 'usr_owner',
    role, joined_at: new Date().toISOString(),
  }
}

function renderTab() {
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}})
  return render(
    <QueryClientProvider client={client}>
      <InvitationsTab organization={organization('owner')}/>
    </QueryClientProvider>,
  )
}

describe('invitations tab', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(fetchOrganizationInvitations).mockResolvedValue([])
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
