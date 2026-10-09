import {cleanup, render, screen, waitFor} from '@testing-library/react'
import {QueryClient, QueryClientProvider} from '@tanstack/react-query'
import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import OrganizationDetailPage from './page'
import {fetchOrganization} from '@/lib/queries'

// The tabs fetch their own data; they are not what this test is about.
vi.mock('./members-tab', () => ({MembersTab: () => <div>members</div>}))
vi.mock('./companies-tab', () => ({CompaniesTab: () => <div>companies</div>}))
vi.mock('./invitations-tab', () => ({InvitationsTab: () => <div>invitations</div>}))
vi.mock('./settings-tab', () => ({SettingsTab: () => <div>settings</div>}))

vi.mock('@/lib/queries', () => ({fetchOrganization: vi.fn()}))

const replace = vi.fn()
let search = new URLSearchParams()

vi.mock('next/navigation', () => ({
  useRouter: () => ({replace, push: vi.fn()}),
  useSearchParams: () => search,
}))

afterEach(cleanup)

function renderPage(query: string) {
  search = new URLSearchParams(query)
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}})
  return render(
    <QueryClientProvider client={client}>
      <OrganizationDetailPage/>
    </QueryClientProvider>,
  )
}

describe('organization detail', () => {
  beforeEach(() => vi.clearAllMocks())

  // An accepted invitation to a space lands here, because the invite page
  // cannot know the kind before it accepts. A space has no companies tab and
  // none of this page's vocabulary, so it goes to its own screen.
  it('sends a space to its people page', async () => {
    vi.mocked(fetchOrganization).mockResolvedValue({
      id: 'org_s', display_name: 'Casa', owner_user_id: 'usr_1', role: 'member',
      kind: 'personal', joined_at: new Date().toISOString(),
    })
    renderPage('id=org_s')
    await waitFor(() => expect(replace).toHaveBeenCalledWith('/account/spaces/people?id=org_s'))
    expect(screen.queryByRole('tab', {name: /companies/i})).toBeNull()
  })

  it('shows an organization as before, absent kind included', async () => {
    vi.mocked(fetchOrganization).mockResolvedValue({
      id: 'org_o', display_name: 'CTech', owner_user_id: 'usr_1', role: 'owner',
      joined_at: new Date().toISOString(),
    })
    renderPage('id=org_o')
    expect(await screen.findByRole('tab', {name: /companies/i})).toBeInTheDocument()
    expect(replace).not.toHaveBeenCalled()
  })
})
