import {cleanup, render, screen} from '@testing-library/react'
import {QueryClient, QueryClientProvider} from '@tanstack/react-query'
import {afterEach, describe, expect, it, vi} from 'vitest'
import {CreateOAuthClientDialog} from './oauth-client-actions'

vi.mock('@/components/scope-picker', () => ({
  ScopePicker: ({value}: {value: string[]}) => <output data-testid="scopes">{value.join(' ')}</output>,
}))

afterEach(cleanup)

function renderDialog(prefill?: {open: boolean; scopes: string[] | null}) {
  const client = new QueryClient()
  return render(<QueryClientProvider client={client}><CreateOAuthClientDialog prefill={prefill}/></QueryClientProvider>)
}

describe('CreateOAuthClientDialog prefill', () => {
  it('opens at once with the given scopes and says where they came from', async () => {
    renderDialog({open: true, scopes: ['billing:invoices:read']})
    expect(await screen.findByTestId('scopes')).toHaveTextContent('billing:invoices:read')
    expect(screen.getByTestId('scopes')).not.toHaveTextContent('openid')
    expect(screen.getByText(/acesso de máquina|machine access/i)).toBeInTheDocument()
  })
  it('stays closed without a prefill', () => {
    renderDialog()
    expect(screen.queryByTestId('scopes')).toBeNull()
  })
})
