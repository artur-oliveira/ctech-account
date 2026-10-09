import {cleanup, render, screen, within} from '@testing-library/react'
import {QueryClient, QueryClientProvider} from '@tanstack/react-query'
import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import SpacesPage from './page'
import {fetchSpaces} from '@/lib/queries'

vi.mock('@/lib/queries', () => ({fetchSpaces: vi.fn()}))

afterEach(cleanup)

function renderPage() {
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}})
  return render(
    <QueryClientProvider client={client}>
      <SpacesPage/>
    </QueryClientProvider>,
  )
}

describe('spaces', () => {
  beforeEach(() => vi.clearAllMocks())

  // A space's roles have their own names. "Member" and "Viewer" are an
  // organization's ladder; a household reads "Full access" and "Read only".
  it('lists each space with the space names for the role', async () => {
    vi.mocked(fetchSpaces).mockResolvedValue([
      {id: 'org_a', display_name: 'Casa', owner_user_id: 'usr_me', role: 'owner', kind: 'personal', joined_at: new Date().toISOString()},
      {id: 'org_b', display_name: 'Viagem', owner_user_id: 'usr_x', role: 'member', kind: 'personal', joined_at: new Date().toISOString()},
      {id: 'org_c', display_name: 'Orçamento', owner_user_id: 'usr_y', role: 'viewer', kind: 'personal', joined_at: new Date().toISOString()},
    ])
    renderPage()

    const table = within(await screen.findByRole('table'))
    expect(table.getByRole('link', {name: 'Casa'})).toHaveAttribute(
      'href', '/account/spaces/people?id=org_a',
    )
    expect(table.getByText('Owner')).toBeInTheDocument()
    expect(table.getByText('Full access')).toBeInTheDocument()
    expect(table.getByText('Read only')).toBeInTheDocument()
    expect(table.queryByText(/^member$|^viewer$/i)).toBeNull()
  })

  it('offers a new space from the list', async () => {
    vi.mocked(fetchSpaces).mockResolvedValue([
      {id: 'org_a', display_name: 'Casa', owner_user_id: 'usr_me', role: 'owner', kind: 'personal', joined_at: new Date().toISOString()},
    ])
    renderPage()
    expect(await screen.findByRole('link', {name: /new space/i})).toHaveAttribute(
      'href', '/account/spaces/new',
    )
  })

  // Having none is where everybody starts. The screen says what a space is for
  // and hands over the one action there is.
  it('teaches what a space is when there are none', async () => {
    vi.mocked(fetchSpaces).mockResolvedValue([])
    renderPage()
    expect(await screen.findByText(/do not have any space yet/i)).toBeInTheDocument()
    expect(screen.getByRole('link', {name: /new space/i})).toHaveAttribute(
      'href', '/account/spaces/new',
    )
    expect(screen.queryByRole('table')).toBeNull()
  })
})
