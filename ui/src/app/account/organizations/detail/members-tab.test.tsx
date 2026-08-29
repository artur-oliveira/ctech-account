import { cleanup, render, screen, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { MembersTab } from './members-tab'
import { fetchOrganizationMembers } from '@/lib/queries'
import type { Organization, OrganizationRole } from '@/lib/types'

vi.mock('@/lib/queries', () => ({
  fetchOrganizationMembers: vi.fn(),
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
