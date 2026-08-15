import { describe, expect, it, vi } from 'vitest'

const { constructorMock } = vi.hoisted(() => ({ constructorMock: vi.fn() }))

vi.mock('@aoctech/auth-client', () => ({
  OAuthClient: function OAuthClient(config: unknown) {
    constructorMock(config)
    return { hasAuthHint: vi.fn(), clearAuthHint: vi.fn(), close: vi.fn() }
  },
}))
vi.mock('./env', () => ({ API_URL: 'https://accounts-api.example.test', CLIENT_ID: 'accounts' }))
vi.mock('./mock', () => ({ USE_MOCK: false }))

import { ACCOUNT_OAUTH_SCOPE, ACCOUNT_USER_SCOPES } from './oauth-client'

describe('Account OAuth scopes', () => {
  it('requests every explicit Resource Server permission exactly once', () => {
    expect(new Set(ACCOUNT_USER_SCOPES).size).toBe(17)
    expect(ACCOUNT_USER_SCOPES).toContain('account:profile:read')
    expect(ACCOUNT_USER_SCOPES).toContain('account:security:write')
    expect(ACCOUNT_USER_SCOPES).toContain('account:terms:write')

    const requested = ACCOUNT_OAUTH_SCOPE.split(' ')
    expect(requested).toEqual(['openid', 'profile', 'email', ...ACCOUNT_USER_SCOPES])
    expect(new Set(requested).size).toBe(requested.length)

    expect(constructorMock).toHaveBeenCalledWith(expect.objectContaining({
      clientId: 'accounts',
      scope: ACCOUNT_OAUTH_SCOPE,
    }))
  })
})
