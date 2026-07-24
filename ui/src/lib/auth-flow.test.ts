import { afterEach, describe, expect, it, vi } from 'vitest'

const { startOAuthFlowMock } = vi.hoisted(() => ({ startOAuthFlowMock: vi.fn() }))
vi.mock('./oauth-client', () => ({
  oauthClient: { startOAuthFlow: startOAuthFlowMock },
}))
vi.mock('./env', () => ({ API_URL: 'https://accounts.aoctech.app' }))
vi.mock('./mock', () => ({ USE_MOCK: false, MOCK_ACCESS_TOKEN: 'mock-token' }))

import { startOAuthFlow } from './auth-flow'

function setLocation() {
  const location = { href: '' }
  Object.defineProperty(window, 'location', { value: location, writable: true, configurable: true })
  return location
}

afterEach(() => {
  vi.clearAllMocks()
})

describe('startOAuthFlow', () => {
  it('follows an external authorize continue URL directly, skipping the self-login round-trip', async () => {
    const location = setLocation()
    await startOAuthFlow('/v1.0/authorize?client_id=wallet&max_age=0')

    expect(location.href).toBe('https://accounts.aoctech.app/v1.0/authorize?client_id=wallet&max_age=0')
    expect(startOAuthFlowMock).not.toHaveBeenCalled()
  })

  it('runs the normal accounts OAuth round-trip for an internal SPA path', async () => {
    setLocation()
    await startOAuthFlow('/account')

    expect(startOAuthFlowMock).toHaveBeenCalledWith('/account')
  })
})
