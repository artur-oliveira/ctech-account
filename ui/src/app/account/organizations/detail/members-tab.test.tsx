import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { MembersTab } from './members-tab'
import { fetchOrganizationMembers, fetchProfile } from '@/lib/queries'
import type { Organization, OrganizationRole } from '@/lib/types'

vi.mock('@/lib/queries', () => ({
  fetchOrganizationMembers: vi.fn(),
  fetchProfile: vi.fn(),
}))

vi.mock('@/lib/mutations', () => ({
  setMemberRoleAPI: vi.fn(),
  removeMemberAPI: vi.fn(),
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

/** The signed-in caller. Their own row is never actionable, so tests that need
 *  controls must not be looking at themselves. */
function signedInAs(userID: string) {
  vi.mocked(fetchProfile).mockResolvedValue({ user_id: userID, email: 'me@example.com' } as never)
}

function renderTab(role: OrganizationRole) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MembersTab organization={organization(role)} />
    </QueryClientProvider>,
  )
}

describe('members tab', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    signedInAs('usr_me')
    vi.mocked(fetchOrganizationMembers).mockResolvedValue([
      { organization_id: 'org_1', user_id: 'usr_owner', role: 'owner', created_at: new Date().toISOString() },
      { organization_id: 'org_1', user_id: 'usr_2', role: 'member', created_at: new Date().toISOString() },
    ])
  })

  // A viewer's controls must be absent, not disabled. A dead control is an
  // invitation to try, and the server would refuse it anyway — so the honest
  // rendering is no control at all.
  it('gives a viewer no controls at all', async () => {
    renderTab('viewer')

    expect(await screen.findAllByText('usr_2')).not.toHaveLength(0)
    expect(screen.queryByRole('button', { name: /remove/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
  })

  it('gives an admin the role and remove controls', async () => {
    renderTab('admin')

    expect(await screen.findAllByText('usr_2')).not.toHaveLength(0)
    expect(screen.getAllByRole('button', { name: /remove/i }).length).toBeGreaterThan(0)
    expect(screen.getAllByRole('combobox').length).toBeGreaterThan(0)
  })

  // The API refuses to re-role or remove an owner. Rendering a control that
  // always fails is worse than rendering none, so the owner's row carries
  // neither — even for an admin who has controls on every other row.
  it('gives the owner row no controls, even to an admin', async () => {
    renderTab('admin')
    await screen.findAllByText('usr_2')

    // Scoped to the table: ResponsiveDataList renders the mobile cards and the
    // desktop table at once and hides one with CSS, so an unscoped count sees
    // every control twice.
    const table = within(screen.getByRole('table'))
    expect(table.getAllByRole('combobox')).toHaveLength(1)
    expect(table.getAllByRole('button', { name: /remove/i })).toHaveLength(1)

    const ownerRow = table.getByText('usr_owner').closest('tr')
    expect(ownerRow).not.toBeNull()
    expect(within(ownerRow as HTMLElement).queryByRole('combobox')).not.toBeInTheDocument()
    expect(
      within(ownerRow as HTMLElement).queryByRole('button', { name: /remove/i }),
    ).not.toBeInTheDocument()
  })
})

describe('member identity', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    signedInAs('usr_me')
  })

  // The name is what a colleague recognizes. The id stays because it is what
  // support asks for, and because a row written before names were stored has
  // nothing else to show.
  it('leads with the name and keeps the id', async () => {
    vi.mocked(fetchOrganizationMembers).mockResolvedValue([
      {
        organization_id: 'org_1',
        user_id: 'usr_owner',
        name: 'Artur Oliveira',
        role: 'owner',
        created_at: new Date().toISOString(),
      },
    ])
    renderTab('viewer')

    expect(await screen.findAllByText('Artur Oliveira')).not.toHaveLength(0)
    expect(screen.getAllByText('usr_owner')).not.toHaveLength(0)
  })

  it('falls back to the id when a row predates stored names', async () => {
    vi.mocked(fetchOrganizationMembers).mockResolvedValue([
      {
        organization_id: 'org_1',
        user_id: 'usr_legacy',
        role: 'member',
        created_at: new Date().toISOString(),
      },
    ])
    renderTab('viewer')

    expect(await screen.findAllByText('usr_legacy')).not.toHaveLength(0)
  })
})

describe('who may act on whom', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    signedInAs('usr_admin_a')
    vi.mocked(fetchOrganizationMembers).mockResolvedValue([
      { organization_id: 'org_1', user_id: 'usr_owner', name: 'Dono', role: 'owner', created_at: new Date().toISOString() },
      { organization_id: 'org_1', user_id: 'usr_admin_a', name: 'Eu', role: 'admin', created_at: new Date().toISOString() },
      { organization_id: 'org_1', user_id: 'usr_admin_b', name: 'Colega', role: 'admin', created_at: new Date().toISOString() },
      { organization_id: 'org_1', user_id: 'usr_viewer', name: 'Leitor', role: 'viewer', created_at: new Date().toISOString() },
    ])
  })

  // Demoting yourself is one wrong click in a column of dropdowns, and you may
  // no longer hold the role needed to undo it.
  it('gives an admin no control over their own row', async () => {
    renderTab('admin')
    await screen.findAllByText('Leitor')

    const table = within(screen.getByRole('table'))
    const own = table.getByText('Eu').closest('tr') as HTMLElement
    expect(within(own).queryByRole('combobox')).not.toBeInTheDocument()
    expect(within(own).queryByRole('button', { name: /remove/i })).not.toBeInTheDocument()
  })

  // Two admins able to edit each other is a disagreement that resolves as a
  // race. Removal is gated too, or the rule is a formality: refused a demotion,
  // an admin could remove the person outright.
  it('gives an admin no control over a peer', async () => {
    renderTab('admin')
    await screen.findAllByText('Leitor')

    const table = within(screen.getByRole('table'))
    const peer = table.getByText('Colega').closest('tr') as HTMLElement
    expect(within(peer).queryByRole('combobox')).not.toBeInTheDocument()
    expect(within(peer).queryByRole('button', { name: /remove/i })).not.toBeInTheDocument()
  })

  it('gives an admin control over somebody below them', async () => {
    renderTab('admin')
    await screen.findAllByText('Leitor')

    const table = within(screen.getByRole('table'))
    const below = table.getByText('Leitor').closest('tr') as HTMLElement
    expect(within(below).getByRole('combobox')).toBeInTheDocument()
    expect(within(below).getByRole('button', { name: /remove/i })).toBeInTheDocument()
  })

  // The dropdown must not offer a rank the caller holds: an admin promoting
  // somebody to admin creates a peer who can act back on them, and the server
  // refuses it anyway.
  it('does not offer an admin the rank they hold', async () => {
    const user = userEvent.setup()
    renderTab('admin')
    await screen.findAllByText('Leitor')

    const table = within(screen.getByRole('table'))
    const below = table.getByText('Leitor').closest('tr') as HTMLElement
    await user.click(within(below).getByRole('combobox'))

    expect(await screen.findByRole('option', { name: /member/i })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: /^admin$/i })).not.toBeInTheDocument()
  })
})
