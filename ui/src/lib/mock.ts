// Dev-only mock so the whole app can be exercised in a browser without the
// Go API running, same intent as ctech-wallet/ui's src/lib/mock.ts. Gated by
// NEXT_PUBLIC_MOCK_API — no production code path reads this unless the flag
// is set, so it is safe to leave in the tree.
import axios, { AxiosError } from 'axios'
import type { AxiosResponse, InternalAxiosRequestConfig } from 'axios'
import { REQUIRED_DOC_TYPES } from './constants'
import type {
  APIKey,
  ActivityEvent,
  ConsentGrant,
  KYCDocumentType,
  KYCStatus,
  OAuthClient,
  Passkey,
  PresignedUpload,
  ScopeService,
  Session,
  User,
} from './types'

export const USE_MOCK = process.env.NEXT_PUBLIC_MOCK_API === 'true'

export const MOCK_ACCESS_TOKEN = 'mock.access.token'

function mockId(prefix: string): string {
  return `${prefix}_${Date.now()}_${Math.random().toString(36).slice(2, 8)}`
}

// Dev-only, mock-gated seeds so the KYC states can be exercised in a browser
// without the Go API. Set via localStorage before load:
//   mock_kyc_seed -> JSON partial merged over the default KYC status
//   mock_totp_enabled -> "true" flips MFA on (unlocks the submission form)
//   mock_errors -> JSON map of "METHOD path" -> { status, body } to force
//     any endpoint to fail instead of returning its happy-path data, e.g.:
//       localStorage.setItem('mock_errors', JSON.stringify({
//         'POST /v1.0/auth/login': { status: 401, body: { detail: 'Invalid credentials' } },
//         'GET /v1.0/account/sessions/*': { status: 500 },
//         'PUT *': { status: 422, body: { detail: 'Validation failed' } },
//         'DELETE /v1.0/account/mfa/totp': { status: 0 }, // status 0 = network error
//       }))
//     path segments accept "*" as a wildcard (matches one segment, e.g. an
//     id); method or path may also be "*" to match anything. Most specific
//     (exact method + exact path) wins.
function mockKycSeed(): KYCStatus {
  const base: KYCStatus = {
    state: 'under_review',
    level: '',
    method: 'document',
    cpf_masked: '***.***.***-00',
    legal_name: 'Mock User',
    birth_date: '1990-01-01',
    address: {
      zip_code: '01001-000',
      street: 'Praça da Sé',
      number: '100',
      district: 'Sé',
      city: 'São Paulo',
      state: 'SP',
    },
    documents: [
      { id: 'doc_front', type: 'id_front', uploaded_at: new Date(Date.now() - 2 * 86_400_000).toISOString() },
      { id: 'doc_back', type: 'id_back', uploaded_at: new Date(Date.now() - 2 * 86_400_000).toISOString() },
      { id: 'doc_selfie_up', type: 'selfie_up', uploaded_at: new Date(Date.now() - 2 * 86_400_000).toISOString() },
    ],
    submitted_at: new Date(Date.now() - 2 * 86_400_000).toISOString(),
  }
  if (typeof window !== 'undefined' && USE_MOCK) {
    const raw = window.localStorage.getItem('mock_kyc_seed')
    if (raw) {
      try {
        return { ...base, ...(JSON.parse(raw) as Partial<KYCStatus>) }
      } catch {
        /* ignore malformed seed */
      }
    }
  }
  return base
}

function mockTotpEnabled(): boolean {
  if (typeof window === 'undefined' || !USE_MOCK) return false
  return window.localStorage.getItem('mock_totp_enabled') === 'true'
}

function mockSession(overrides: Partial<Session>): Session {
  return {
    session_id: mockId('sess'),
    device_name: 'Chrome on macOS',
    ip_address: '203.0.113.10',
    created_at: new Date(Date.now() - 30 * 86_400_000).toISOString(),
    last_used_at: new Date().toISOString(),
    is_current: false,
    geo_city: 'São Paulo',
    geo_region: 'SP',
    geo_latitude: -23.55,
    geo_longitude: -46.63,
    ...overrides,
  }
}

const state = {
  user: {
    user_id: 'mock_user',
    email: 'mock.user@aoctech.app',
    first_name: 'Mock',
    last_name: 'User',
    display_name: null,
    avatar_url: null,
    email_verified: true,
    has_password: true,
    google_linked: false,
    created_at: new Date('2026-01-01').toISOString(),
    terms_pending: { tos: false, privacy: false },
  } as User,
  sessions: [
    mockSession({ is_current: true, device_name: 'Chrome on macOS' }),
    mockSession({ device_name: 'Safari on iPhone', geo_city: 'Rio de Janeiro', geo_region: 'RJ' }),
  ] as Session[],
  apiKeys: [] as APIKey[],
  oauthClients: [] as OAuthClient[],
  consents: [
    {
      client_id: 'client_mock_analytics',
      client_name: 'Analytics Dashboard',
      scopes: ['profile:read', 'sessions:read'],
      created_at: new Date(Date.now() - 15 * 86_400_000).toISOString(),
      updated_at: new Date(Date.now() - 15 * 86_400_000).toISOString(),
    },
    {
      client_id: 'client_mock_billing',
      client_name: 'Billing Portal',
      scopes: ['profile:read', 'identity:read'],
      created_at: new Date(Date.now() - 40 * 86_400_000).toISOString(),
      updated_at: new Date(Date.now() - 40 * 86_400_000).toISOString(),
    },
  ] as ConsentGrant[],
  passkeys: [] as Passkey[],
  totpEnabled: mockTotpEnabled(),
  activity: [
    { event_type: 'login_success', ip: '203.0.113.10', user_agent: 'Chrome/125', metadata: { device_name: 'Chrome on macOS' }, created_at: new Date(Date.now() - 3_600_000).toISOString() },
    { event_type: 'mfa_challenge_success', ip: '203.0.113.10', user_agent: 'Chrome/125', metadata: {}, created_at: new Date(Date.now() - 7_200_000).toISOString() },
    { event_type: 'password_changed', ip: '203.0.113.10', user_agent: 'Chrome/125', metadata: {}, created_at: new Date(Date.now() - 86_400_000).toISOString() },
    { event_type: 'apikey_created', ip: '203.0.113.10', user_agent: 'Chrome/125', metadata: { client_id: 'CI/CD pipeline' }, created_at: new Date(Date.now() - 5 * 86_400_000).toISOString() },
    { event_type: 'consent_granted', ip: '203.0.113.10', user_agent: 'Chrome/125', metadata: { client_id: 'Analytics Dashboard' }, created_at: new Date(Date.now() - 15 * 86_400_000).toISOString() },
  ] as ActivityEvent[],
  kyc: mockKycSeed(),
  scopeCatalog: [
    { service: 'account', name: 'Account', scopes: [
      { scope: 'profile:read', description: 'Read your profile', description_pt: 'Ler seu perfil' },
      { scope: 'profile:write', description: 'Update your profile', description_pt: 'Atualizar seu perfil' },
      { scope: 'sessions:read', description: 'Read your sessions', description_pt: 'Ler suas sessões' },
      { scope: 'sessions:revoke', description: 'Revoke your sessions', description_pt: 'Revogar suas sessões' },
      { scope: 'api_keys:read', description: 'Read your API keys', description_pt: 'Ler suas chaves de API' },
      { scope: 'api_keys:write', description: 'Create and revoke API keys', description_pt: 'Criar e revogar chaves de API' },
    ] },
    { service: 'identity', name: 'Identity (OIDC)', scopes: [
      { scope: 'openid', description: 'OpenID Connect identifier', description_pt: 'Identificador OpenID Connect' },
      { scope: 'identity:read', description: 'Read your verified identity (KYC)', description_pt: 'Ler sua identidade verificada (KYC)' },
      { scope: 'email', description: 'Read your email address', description_pt: 'Ler seu endereço de e-mail' },
    ] },
  ] as ScopeService[],
}

function ok<T>(data: T, config: InternalAxiosRequestConfig): AxiosResponse<T> {
  return { data, status: 200, statusText: 'OK', headers: {}, config }
}

function fail(status: number, data: unknown, config: InternalAxiosRequestConfig): never {
  throw new AxiosError('Request failed', String(status), config, undefined, {
    data,
    status,
    statusText: '',
    headers: {},
    config,
  })
}

type ErrorRule = { status: number; body?: unknown }

function mockErrorRules(): Record<string, ErrorRule> {
  if (typeof window === 'undefined' || !USE_MOCK) return {}
  const raw = window.localStorage.getItem('mock_errors')
  if (!raw) return {}
  try {
    return JSON.parse(raw) as Record<string, ErrorRule>
  } catch {
    return {}
  }
}

function matchErrorRule(method: string, path: string): ErrorRule | undefined {
  const rules = mockErrorRules()
  const exact = rules[`${method} ${path}`]
  if (exact) return exact
  for (const [key, rule] of Object.entries(rules)) {
    const [ruleMethod, rulePath] = key.split(' ')
    if (ruleMethod !== '*' && ruleMethod !== method) continue
    if (rulePath === '*') return rule
    const pattern = new RegExp(`^${rulePath.replace(/[.+?^${}()|[\]\\]/g, '\\$&').replace(/\\\*/g, '[^/]+')}$`)
    if (pattern.test(path)) return rule
  }
  return undefined
}

type Route = {
  method: string
  pattern: RegExp
  handle: (match: RegExpMatchArray, body: Record<string, unknown>, config: InternalAxiosRequestConfig) => unknown
}

const routes: Route[] = [
  { method: 'get', pattern: /^\/v1\.0\/account\/profile$/, handle: () => state.user },
  { method: 'get', pattern: /^\/v1\.0\/account\/sessions$/, handle: () => ({ sessions: state.sessions }) },
  { method: 'get', pattern: /^\/v1\.0\/account\/api-keys$/, handle: () => ({ api_keys: state.apiKeys }) },
  { method: 'get', pattern: /^\/v1\.0\/account\/oauth-clients$/, handle: () => ({ oauth_clients: state.oauthClients }) },
  { method: 'get', pattern: /^\/v1\.0\/scopes$/, handle: () => ({ services: state.scopeCatalog }) },
  { method: 'get', pattern: /^\/v1\.0\/account\/consents$/, handle: () => ({ consents: state.consents }) },
  { method: 'get', pattern: /^\/v1\.0\/account\/mfa\/passkeys$/, handle: () => ({ passkeys: state.passkeys }) },
  { method: 'get', pattern: /^\/v1\.0\/account\/mfa\/totp$/, handle: () => ({ enabled: state.totpEnabled }) },
  {
    method: 'get',
    pattern: /^\/v1\.0\/account\/mfa\/totp\/setup$/,
    handle: (_m, _b, config) => {
      if (state.totpEnabled) fail(409, { detail: 'TOTP already configured' }, config)
      return { provisioning_uri: 'otpauth://totp/CTech%20Account:mock.user@aoctech.app?secret=MOCKSECRET&issuer=CTech%20Account' }
    },
  },
  { method: 'get', pattern: /^\/v1\.0\/account\/activity/, handle: () => ({ events: state.activity, next_cursor: '' }) },
  { method: 'get', pattern: /^\/v1\.0\/account\/kyc$/, handle: () => state.kyc },

  { method: 'post', pattern: /^\/v1\.0\/auth\/login$/, handle: () => ({ requires_mfa: false }) },
  { method: 'post', pattern: /^\/v1\.0\/auth\/mfa\/challenge$/, handle: () => ({}) },
  { method: 'post', pattern: /^\/v1\.0\/auth\/accept-terms$/, handle: () => ({ redirect: '/account' }) },
  {
    method: 'post',
    pattern: /^\/v1\.0\/account\/terms\/accept$/,
    handle: () => {
      state.user.terms_pending = { tos: false, privacy: false }
      return { terms_pending: state.user.terms_pending }
    },
  },
  { method: 'post', pattern: /^\/v1\.0\/auth\/register$/, handle: () => ({}) },
  { method: 'post', pattern: /^\/v1\.0\/auth\/logout$/, handle: () => ({}) },
  {
    method: 'post',
    pattern: /^\/v1\.0\/account\/api-keys$/,
    handle: (_m, body) => {
      const key: APIKey = {
        key_id: mockId('key'),
        key_prefix: 'ctk_mock',
        name: String(body.name ?? 'Mock key'),
        scopes: Array.isArray(body.scopes) ? (body.scopes as string[]) : [],
        last_used_at: null,
        expires_at: null,
        created_at: new Date().toISOString(),
      }
      state.apiKeys.unshift(key)
      return { raw_key: `ctk_mock_${mockId('secret')}` }
    },
  },
  {
    method: 'post',
    pattern: /^\/v1\.0\/account\/oauth-clients$/,
    handle: (_m, body) => {
      const client: OAuthClient = {
        client_id: mockId('client'),
        name: String(body.name ?? 'Mock client'),
        client_type: (body.client_type as OAuthClient['client_type']) ?? 'confidential',
        redirect_uris: Array.isArray(body.redirect_uris) ? (body.redirect_uris as string[]) : [],
        allowed_scopes: Array.isArray(body.allowed_scopes) ? (body.allowed_scopes as string[]) : [],
        audience: Array.isArray(body.audience) ? (body.audience as string[]) : null,
        created_at: new Date().toISOString(),
        updated_at: new Date().toISOString(),
      }
      state.oauthClients.unshift(client)
      return { ...client, client_secret: `mock_secret_${mockId('cs')}` }
    },
  },
  { method: 'post', pattern: /^\/v1\.0\/authorize\/consent$/, handle: () => ({ redirect_to: '/account' }) },
  {
    method: 'post',
    pattern: /^\/v1\.0\/account\/oauth-clients\/([^/]+)\/regenerate-secret$/,
    handle: () => ({ client_secret: `mock_secret_${mockId('cs')}` }),
  },
  {
    method: 'post',
    pattern: /^\/v1\.0\/account\/mfa\/totp\/confirm$/,
    handle: () => {
      state.totpEnabled = true
      return { backup_codes: ['MOCK-0001', 'MOCK-0002', 'MOCK-0003'] }
    },
  },
  { method: 'post', pattern: /^\/v1\.0\/account\/mfa\/totp\/backup-codes$/, handle: () => ({ backup_codes: ['MOCK-0004', 'MOCK-0005'] }) },
  { method: 'post', pattern: /^\/v1\.0\/account\/mfa\/passkeys\/register\/begin$/, handle: () => ({ session_token: mockId('pkreg'), name: 'Mock passkey', options: '{}' }) },
  {
    method: 'post',
    pattern: /^\/v1\.0\/account\/mfa\/passkeys\/register\/complete$/,
    handle: (_m, _b, config) => {
      const name = new URL(config.url ?? '', 'http://mock').searchParams.get('name') ?? 'Mock passkey'
      const passkey: Passkey = { id: mockId('pk'), name, aaguid: mockId('aaguid'), created_at: new Date().toISOString(), last_used_at: null }
      state.passkeys.unshift(passkey)
      return {}
    },
  },
  { method: 'post', pattern: /^\/v1\.0\/auth\/forgot-password$/, handle: () => ({}) },
  { method: 'post', pattern: /^\/v1\.0\/auth\/reset-password$/, handle: () => ({}) },
  { method: 'post', pattern: /^\/v1\.0\/auth\/verify-email$/, handle: () => ({}) },
  { method: 'post', pattern: /^\/v1\.0\/auth\/resend-verification$/, handle: () => ({}) },
  { method: 'post', pattern: /^\/v1\.0\/auth\/passkeys\/authenticate\/begin$/, handle: () => ({ session_token: mockId('pkauth'), options: '{}' }) },
  { method: 'post', pattern: /^\/v1\.0\/auth\/passkeys\/authenticate\/complete/, handle: () => ({ requires_mfa: false }) },
  { method: 'post', pattern: /^\/v1\.0\/auth\/mfa\/passkey\/begin$/, handle: () => ({ session_token: mockId('mfapk'), options: '{}' }) },
  { method: 'post', pattern: /^\/v1\.0\/auth\/mfa\/passkey\/complete/, handle: () => ({}) },
  { method: 'post', pattern: /^\/v1\.0\/auth\/step-up$/, handle: () => ({}) },
  { method: 'post', pattern: /^\/v1\.0\/auth\/step-up\/passkeys\/begin$/, handle: () => ({ session_token: mockId('stepup'), options: '{}' }) },
  { method: 'post', pattern: /^\/v1\.0\/auth\/step-up\/passkeys\/complete/, handle: () => ({}) },
  {
    method: 'post',
    pattern: /^\/v1\.0\/account\/kyc$/,
    handle: (_m, body) => {
      state.kyc = {
        ...state.kyc,
        state: 'awaiting_files',
        legal_name: String(body.legal_name ?? ''),
        birth_date: String(body.birth_date ?? ''),
        cpf_masked: '***.***.***-00',
        address: body.address as KYCStatus['address'],
        submitted_at: new Date().toISOString(),
      }
      return state.kyc
    },
  },
  {
    method: 'post',
    pattern: /^\/v1\.0\/account\/kyc\/documents$/,
    handle: (_m, body): PresignedUpload => ({
      document_id: mockId('doc'),
      upload_url: '/__mock_upload__',
      expires_in: 300,
      max_bytes: 5 * 1024 * 1024,
      content_type: String(body.content_type ?? 'application/octet-stream'),
    }),
  },
  {
    method: 'post',
    pattern: /^\/v1\.0\/account\/kyc\/documents\/confirm$/,
    handle: (_m, body) => {
      const type = body.type as KYCDocumentType
      const documents = [...(state.kyc.documents ?? []), { id: String(body.document_id), type, uploaded_at: new Date().toISOString() }]
      const allUploaded = REQUIRED_DOC_TYPES.every((required) => documents.some((doc) => doc.type === required))
      state.kyc = { ...state.kyc, documents, state: allUploaded ? 'under_review' : 'awaiting_files' }
      return state.kyc
    },
  },

  { method: 'put', pattern: /^\/__mock_upload__/, handle: () => ({}) },
  {
    method: 'put',
    pattern: /^\/v1\.0\/account\/profile$/,
    handle: (_m, body) => {
      state.user = { ...state.user, ...body } as User
      return state.user
    },
  },
  { method: 'put', pattern: /^\/v1\.0\/account\/password$/, handle: () => ({}) },
  {
    method: 'put',
    pattern: /^\/v1\.0\/account\/oauth-clients\/([^/]+)$/,
    handle: (m, body) => {
      const client = state.oauthClients.find((c) => c.client_id === m[1])
      if (!client) return {}
      Object.assign(client, body, { updated_at: new Date().toISOString() })
      return client
    },
  },

  { method: 'delete', pattern: /^\/v1\.0\/account\/link\/google$/, handle: () => { state.user.google_linked = false; return {} } },
  {
    method: 'delete',
    pattern: /^\/v1\.0\/account\/sessions\/([^/]+)$/,
    handle: (m) => { state.sessions = state.sessions.filter((s) => s.session_id !== m[1]); return {} },
  },
  { method: 'delete', pattern: /^\/v1\.0\/account\/sessions$/, handle: () => { state.sessions = state.sessions.filter((s) => s.is_current); return {} } },
  {
    method: 'delete',
    pattern: /^\/v1\.0\/account\/api-keys\/([^/]+)$/,
    handle: (m) => { state.apiKeys = state.apiKeys.filter((k) => k.key_id !== m[1]); return {} },
  },
  {
    method: 'delete',
    pattern: /^\/v1\.0\/account\/oauth-clients\/([^/]+)$/,
    handle: (m) => { state.oauthClients = state.oauthClients.filter((c) => c.client_id !== m[1]); return {} },
  },
  {
    method: 'delete',
    pattern: /^\/v1\.0\/account\/consents\/([^/]+)$/,
    handle: (m) => { state.consents = state.consents.filter((c) => c.client_id !== m[1]); return {} },
  },
  { method: 'delete', pattern: /^\/v1\.0\/account\/mfa\/totp$/, handle: () => { state.totpEnabled = false; return {} } },
  {
    method: 'delete',
    pattern: /^\/v1\.0\/account\/mfa\/passkeys\/([^/]+)$/,
    handle: (m) => { state.passkeys = state.passkeys.filter((p) => p.id !== m[1]); return {} },
  },
]

/** In-memory stand-in for the Go API. Mirrors ctech-wallet/ui's MockApiClient, adapted to axios's adapter hook since this app calls a shared `api` instance instead of a class. */
export async function mockAdapter(config: InternalAxiosRequestConfig): Promise<AxiosResponse> {
  const method = (config.method ?? 'get').toLowerCase()
  const path = (config.url ?? '').replace(/^https?:\/\/[^/]+/, '').split('?')[0]

  // The silent-refresh endpoint is hit with a bare `axios.post`, not `api`.
  if (path.endsWith('/v1.0/token')) return ok({ access_token: MOCK_ACCESS_TOKEN }, config)

  const rule = matchErrorRule(method.toUpperCase(), path)
  if (rule) {
    if (rule.status === 0) throw new AxiosError('Network Error', AxiosError.ERR_NETWORK, config)
    fail(rule.status, rule.body ?? { detail: 'Mock error' }, config)
  }

  const body = typeof config.data === 'string' ? JSON.parse(config.data || '{}') : (config.data as Record<string, unknown>) ?? {}

  for (const route of routes) {
    if (route.method !== method) continue
    const match = path.match(route.pattern)
    if (match) return ok(route.handle(match, body, config), config)
  }

  // Unmodeled route (e.g. a presigned S3 PUT for a document type not listed
  // above) — succeed rather than fail the flow outright.
  return ok({}, config)
}

if (USE_MOCK) axios.defaults.adapter = mockAdapter
