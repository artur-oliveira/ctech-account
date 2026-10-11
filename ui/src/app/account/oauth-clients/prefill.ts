/**
 * A product's link to "new client with these scopes" (ctech-billing console
 * self-service spec § 5.1): `?new=1&scopes=a,b`. Only scope-shaped tokens are
 * kept; the server validates every scope again on create, so this is a
 * convenience, never authority.
 */
const SCOPE = /^[a-z][a-z0-9-]*:[a-z0-9:_-]+$/
const MAX = 32

export type ClientPrefill = {open: boolean; scopes: string[] | null}

export function readClientPrefill(params: URLSearchParams): ClientPrefill {
  const open = params.get('new') === '1'
  const raw = params.get('scopes')
  if (!open || raw === null) return {open, scopes: null}
  const scopes = [...new Set(raw.split(',').map((s) => s.trim()).filter((s) => SCOPE.test(s)))].slice(0, MAX)
  return {open, scopes: scopes.length > 0 ? scopes : null}
}
