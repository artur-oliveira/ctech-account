import {cleanup, render, screen, waitFor} from '@testing-library/react'
import {QueryClient, QueryClientProvider} from '@tanstack/react-query'
import {afterEach, describe, expect, it, vi} from 'vitest'
import {AppNetworkProvider} from './network-provider'

vi.mock('next/navigation', () => ({usePathname: () => '/account/delete', useRouter: () => ({replace: vi.fn()})}))

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

function setOnline(value: boolean) {
  Object.defineProperty(navigator, 'onLine', {value, configurable: true})
}

describe('AppNetworkProvider', () => {
  it('shows an in-flow banner offline (never fixed over buttons) and refetches active queries on reconnect', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(null, {status: 204})))
    const client = new QueryClient()
    const refetch = vi.spyOn(client, 'refetchQueries').mockResolvedValue()
    setOnline(false)
    render(
      <QueryClientProvider client={client}>
        <AppNetworkProvider><button>Delete account</button></AppNetworkProvider>
      </QueryClientProvider>,
    )
    window.dispatchEvent(new Event('offline'))
    const banner = await screen.findByRole('status')
    await waitFor(() => expect(banner.textContent).not.toBe(''))
    expect(banner.className).not.toMatch(/fixed|sticky|absolute/)
    expect(screen.getByRole('button', {name: 'Delete account'})).toBeInTheDocument()

    setOnline(true)
    window.dispatchEvent(new Event('online'))
    await waitFor(() => expect(refetch).toHaveBeenCalledWith({type: 'active'}))
  })
})
