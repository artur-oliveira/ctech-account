import {cleanup, render, screen} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {QueryClient, QueryClientProvider} from '@tanstack/react-query'
import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import {SettingsTab} from './settings-tab'
import {fetchOrganizationMembers, fetchProfile} from '@/lib/queries'
import {removeMemberAPI} from '@/lib/mutations'
import {AxiosError} from 'axios'
import {toast} from 'sonner'
import type {Organization, OrganizationRole} from '@/lib/types'

vi.mock('@/lib/queries', () => ({
  fetchOrganizationMembers: vi.fn(),
  fetchProfile: vi.fn(),
}))

vi.mock('@/lib/mutations', () => ({
  removeMemberAPI: vi.fn(),
  renameOrganizationAPI: vi.fn(),
  transferOwnershipAPI: vi.fn(),
}))

vi.mock('sonner', () => ({toast: {error: vi.fn(), success: vi.fn()}}))

vi.mock('next/navigation', () => ({useRouter: () => ({push: vi.fn()})}))

afterEach(cleanup)

function workspace(role: OrganizationRole, kind?: Organization['kind']): Organization {
  return {
    id: 'org_1', display_name: 'Casa', owner_user_id: 'usr_owner',
    role, kind, joined_at: new Date().toISOString(),
  }
}

function renderTab(org: Organization) {
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}})
  return render(
    <QueryClientProvider client={client}>
      <SettingsTab organization={org}/>
    </QueryClientProvider>,
  )
}

describe('settings on a space', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(fetchProfile).mockResolvedValue({user_id: 'usr_owner'} as never)
    vi.mocked(fetchOrganizationMembers).mockResolvedValue([
      {organization_id: 'org_1', user_id: 'usr_owner', name: 'Dona', role: 'owner', created_at: new Date().toISOString()},
      {organization_id: 'org_1', user_id: 'usr_full', name: 'Pedro', role: 'member', created_at: new Date().toISOString()},
      {organization_id: 'org_1', user_id: 'usr_read', name: 'Ana', role: 'viewer', created_at: new Date().toISOString()},
    ])
  })

  // The server refuses a transfer to a reader. Listing one is offering a
  // choice that always fails.
  it('offers ownership only to people with full access', async () => {
    const user = userEvent.setup()
    renderTab(workspace('owner', 'personal'))
    expect(await screen.findByRole('heading', {name: /transfer the space/i})).toBeInTheDocument()

    await user.click(await screen.findByRole('combobox', {name: /new owner/i}))
    expect(await screen.findByRole('option', {name: 'Pedro'})).toBeInTheDocument()
    expect(screen.queryByRole('option', {name: 'Ana'})).toBeNull()
  })

  it('says what to do when nobody has full access', async () => {
    vi.mocked(fetchOrganizationMembers).mockResolvedValue([
      {organization_id: 'org_1', user_id: 'usr_owner', name: 'Dona', role: 'owner', created_at: new Date().toISOString()},
      {organization_id: 'org_1', user_id: 'usr_read', name: 'Ana', role: 'viewer', created_at: new Date().toISOString()},
    ])
    renderTab(workspace('owner', 'personal'))
    expect(await screen.findByText(/nobody here has full access/i)).toBeInTheDocument()
  })

  // Renaming is the owner's on a space: there is no admin to share it with.
  it('does not offer a rename to somebody with full access', async () => {
    renderTab(workspace('member', 'personal'))
    expect(await screen.findByRole('heading', {name: /leave the space/i})).toBeInTheDocument()
    expect(screen.queryByRole('button', {name: /^save$/i})).toBeNull()
  })

  // The server's problem detail is written for organizations ("You do not have
  // access to this organization."). On a space the screen says it in the
  // space's own words instead of passing that text through.
  it('words a failure in the space\'s own terms', async () => {
    const user = userEvent.setup()
    vi.mocked(fetchProfile).mockResolvedValue({user_id: 'usr_full'} as never)
    vi.mocked(removeMemberAPI).mockRejectedValue(
      Object.assign(new AxiosError('forbidden'), {response: {data: {detail: 'You do not have access to this organization.'}}}),
    )
    renderTab(workspace('member', 'personal'))
    await user.click(await screen.findByRole('button', {name: /leave the space/i}))
    await user.click(await screen.findByRole('button', {name: /confirm/i}))
    await vi.waitFor(() => expect(toast.error).toHaveBeenCalled())
    expect(toast.error).toHaveBeenCalledWith('Could not leave the space.')
  })

  it('never calls a space an organization', async () => {
    renderTab(workspace('owner', 'personal'))
    await screen.findByRole('heading', {name: /transfer the space/i})
    expect(screen.queryByText(/organization/i)).toBeNull()
  })

  // The organization's ladder is untouched: a viewer can still be handed an
  // organization, as before.
  it('keeps every non-owner as a candidate on an organization', async () => {
    const user = userEvent.setup()
    renderTab(workspace('owner'))
    await user.click(await screen.findByRole('combobox', {name: /new owner/i}))
    expect(await screen.findByRole('option', {name: 'Ana'})).toBeInTheDocument()
    expect(screen.getByRole('option', {name: 'Pedro'})).toBeInTheDocument()
  })
})
