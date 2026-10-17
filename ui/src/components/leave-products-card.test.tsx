import {cleanup, render, screen} from '@testing-library/react'
import {QueryClient, QueryClientProvider} from '@tanstack/react-query'
import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import {LeaveProductsCard} from './leave-products-card'
import {fetchDeletionRequest, fetchLeavableServices} from '@/lib/queries'

vi.mock('@/lib/queries', () => ({fetchLeavableServices: vi.fn(), fetchDeletionRequest: vi.fn()}))

afterEach(cleanup)

function renderCard() {
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}})
  return render(<QueryClientProvider client={client}><LeaveProductsCard/></QueryClientProvider>)
}

describe('LeaveProductsCard', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(fetchDeletionRequest).mockResolvedValue(null)
  })

  it('lists one entry per leavable product', async () => {
    vi.mocked(fetchLeavableServices).mockResolvedValue([{service: 'poker', can_leave: true}, {service: 'dfe', can_leave: true}])
    renderCard()
    expect(await screen.findByText('CTech Poker')).toBeInTheDocument()
    expect(screen.getByText('CTech DF-e')).toBeInTheDocument()
    expect(screen.getAllByRole('button', {name: 'Leave'})[0]).toHaveAttribute('href', '/account/leave?service=poker')
  })

  it('renders nothing when no product can be left', async () => {
    vi.mocked(fetchLeavableServices).mockResolvedValue([])
    const {container} = renderCard()
    await vi.waitFor(() => expect(fetchLeavableServices).toHaveBeenCalled())
    expect(container).toBeEmptyDOMElement()
  })

  it('disables leaving while another request is open', async () => {
    vi.mocked(fetchLeavableServices).mockResolvedValue([{service: 'poker', can_leave: false}])
    renderCard()
    expect(await screen.findByText(/another deletion request is in progress/i)).toBeInTheDocument()
    expect(screen.queryByRole('button', {name: 'Leave'})).toBeNull()
  })

  it('says which product is being left and until when', async () => {
    vi.mocked(fetchLeavableServices).mockResolvedValue([{service: 'poker', can_leave: false}])
    vi.mocked(fetchDeletionRequest).mockResolvedValue({
      request_id: 'r1', state: 'pending_deletion', scope: 'service', service: 'poker', grace_until: '2026-10-24T12:00:00Z',
    })
    renderCard()
    expect(await screen.findByText(/Leaving CTech Poker/)).toBeInTheDocument()
  })
})
