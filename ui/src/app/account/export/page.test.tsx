import {cleanup, render, screen, waitFor} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {QueryClient, QueryClientProvider} from '@tanstack/react-query'
import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import DataExportPage from './page'
import {fetchDataExport, fetchProfile} from '@/lib/queries'
import {discardDataExportAPI, requestDataExportAPI} from '@/lib/mutations'

vi.mock('@/lib/queries', () => ({fetchDataExport: vi.fn(), fetchProfile: vi.fn()}))
vi.mock('@/lib/mutations', () => ({requestDataExportAPI: vi.fn(), discardDataExportAPI: vi.fn()}))
vi.mock('@/lib/axios', () => ({isAxiosError: () => false}))

afterEach(cleanup)

function renderPage() {
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}})
  return render(<QueryClientProvider client={client}><DataExportPage/></QueryClientProvider>)
}

describe('DataExportPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(fetchDataExport).mockResolvedValue(null)
    vi.mocked(fetchProfile).mockResolvedValue({has_password: true} as never)
  })

  it('requests a copy with the password', async () => {
    const user = userEvent.setup()
    vi.mocked(requestDataExportAPI).mockResolvedValue({id: 'e1', state: 'building', parts: [], links: []})
    renderPage()
    await user.type(await screen.findByLabelText('Your password'), 'pw')
    await user.click(screen.getByRole('button', {name: 'Request a copy'}))
    await waitFor(() => expect(requestDataExportAPI).toHaveBeenCalledWith('pw'))
  })

  it('says which products are still preparing their part or failed', async () => {
    vi.mocked(fetchDataExport).mockResolvedValue({
      id: 'e1', state: 'ready', expires_at: '2026-10-24T12:00:00Z',
      parts: [{service: 'account', status: 'ready', size: 1}, {service: 'poker', status: 'dispatched', size: 0}, {service: 'dfe', status: 'failed', size: 0}],
      links: [{service: 'account', url: 'https://s3.test/a.zip', size: 1}],
    })
    renderPage()
    expect(await screen.findByText('CTech Poker: still being prepared')).toBeInTheDocument()
    expect(screen.getByText(/CTech DF-e: could not be prepared/)).toBeInTheDocument()
  })

  it('offers the download links of a ready copy and can discard it', async () => {
    const user = userEvent.setup()
    vi.mocked(fetchDataExport).mockResolvedValue({
      id: 'e1', state: 'ready', expires_at: '2026-10-24T12:00:00Z',
      parts: [{service: 'account', status: 'ready', size: 2048}],
      links: [{service: 'account', url: 'https://s3.test/a.zip', size: 2048}],
    })
    vi.mocked(discardDataExportAPI).mockResolvedValue()
    renderPage()
    const link = await screen.findByRole('button', {name: /Download Account \(2 KB\)/})
    expect(link).toHaveAttribute('href', 'https://s3.test/a.zip')
    await user.click(screen.getByRole('button', {name: 'Delete this copy'}))
    await waitFor(() => expect(discardDataExportAPI).toHaveBeenCalled())
  })
})
