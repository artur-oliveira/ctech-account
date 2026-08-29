import { cleanup, render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { AccountNav } from './account-nav'
import { fetchProfile } from '@/lib/queries'
import type { User } from '@/lib/types'

vi.mock('@/lib/queries', () => ({ fetchProfile: vi.fn() }))
vi.mock('next/navigation', () => ({ usePathname: () => '/account' }))

afterEach(cleanup)

function signedInAs(supportRole: User['support_role']) {
  vi.mocked(fetchProfile).mockResolvedValue({ support_role: supportRole } as never)
}

function renderNav() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <AccountNav />
    </QueryClientProvider>,
  )
}

describe('account nav', () => {
  beforeEach(() => vi.clearAllMocks())

  // The gap this closes: an operator could reach /admin only by typing the URL,
  // because the console's own nav bar lives inside the console.
  it('offers an operator the way into the console', async () => {
    signedInAs('agent')
    renderNav()
    expect(await screen.findByRole('link', { name: /admin/i })).toHaveAttribute(
      'href',
      '/admin/support',
    )
  })

  it('says nothing about the console to somebody without a support role', async () => {
    signedInAs('')
    renderNav()
    // Wait for a link that is always there, so the absence below is a real
    // absence rather than a render that had not happened yet.
    await screen.findByRole('link', { name: /profile/i })
    expect(screen.queryByRole('link', { name: /admin/i })).toBeNull()
  })
})
