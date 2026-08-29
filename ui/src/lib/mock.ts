// Dev-only mock so the whole app can be exercised in a browser without the
// Go API running, same intent as ctech-wallet/ui's src/lib/mock.ts. Gated by
// NEXT_PUBLIC_MOCK_API — no production code path reads this unless the flag
// is set, so it is safe to leave in the tree.
import axios, { AxiosError } from 'axios'
import type { AxiosResponse, InternalAxiosRequestConfig } from 'axios'
import type {
  APIKey,
  AdminKYCAuditEvent,
  AdminKYCReview,
  ActivityEvent,
  ConsentGrant,
  KYCDocumentType,
  KYCStatus,
  OAuthClient,
  Organization,
  OrganizationInvitation,
  OrganizationMember,
  OrganizationRole,
  Passkey,
  PresignedUpload,
  ScopeService,
  Session,
  SupportMessage,
  SupportTicket,
  User,
  Company,
  CompanyActor,
} from './types'

export const USE_MOCK = process.env.NEXT_PUBLIC_MOCK_API === 'true'

export const MOCK_ACCESS_TOKEN = 'mock.access.token'

/** The token the mock accepts; anything else is refused. */
// The one CNPJ the mock register has heard of. Everything else is a miss, so
// the "type the names yourself" path is reachable without faking an outage.
const MOCK_KNOWN_CNPJ = '11222333000181'

const MOCK_VALID_INVITE_TOKEN = 'mock-valid-invitation'
/** The token that reproduces the unverified-e-mail gate. */
const MOCK_UNVERIFIED_INVITE_TOKEN = 'mock-unverified-invitation'

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
    level: 'enhanced',
    cpf_masked: '***.***.***-00',
    legal_name: 'Mock User',
    birth_date: '1990-01-01',
    phone_masked: '***4321',
    documents: [
      { id: 'doc_front', type: 'id_front', uploaded_at: new Date(Date.now() - 2 * 86_400_000).toISOString() },
      { id: 'doc_back', type: 'id_back', uploaded_at: new Date(Date.now() - 2 * 86_400_000).toISOString() },
      { id: 'doc_selfie_with_document', type: 'selfie_with_document', uploaded_at: new Date(Date.now() - 2 * 86_400_000).toISOString() },
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

// mock_support_role -> '' | 'agent' | 'manager' | 'admin', flips the
// /admin/support gate on. Default '' exercises the "regular user redirected
// away" scenario without extra setup.
function mockSupportRole(): '' | 'agent' | 'manager' | 'admin' {
  if (typeof window === 'undefined' || !USE_MOCK) return ''
  const raw = window.localStorage.getItem('mock_support_role')
  return raw === 'agent' || raw === 'manager' || raw === 'admin' ? raw : ''
}

/**
 * Organization scenario, read at request time so QA can switch states from the
 * console without restarting the module.
 *
 *   localStorage.mock_organizations_seed = '{"count":0}'              // the empty state
 *   localStorage.mock_organizations_seed = '{"count":1,"role":"owner"}'
 *   localStorage.mock_organizations_seed = '{"count":1,"role":"viewer"}' // read-only roster
 *   localStorage.mock_organizations_seed = '{"count":3}'              // mixed roles
 */
function mockOrganizationSeed(): { count: number; role: OrganizationRole } {
  const fallback = { count: 1, role: 'owner' as OrganizationRole }
  if (typeof window === 'undefined' || !USE_MOCK) return fallback
  try {
    const raw = window.localStorage.getItem('mock_organizations_seed')
    if (!raw) return fallback
    const parsed = JSON.parse(raw) as Partial<{ count: number; role: OrganizationRole }>
    return {
      count: typeof parsed.count === 'number' ? parsed.count : fallback.count,
      role: parsed.role ?? fallback.role,
    }
  } catch {
    return fallback
  }
}

/** Roles handed out across a multi-organization seed, so the list is not uniform. */
const MOCK_ROLE_CYCLE: OrganizationRole[] = ['owner', 'admin', 'viewer']

function seedOrganizations(): Organization[] {
  const { count, role } = mockOrganizationSeed()
  const names = ['CTech', 'Contabilidade Silva', 'Transportes Souza']
  return Array.from({ length: count }, (_, i) => ({
    id: `org_mock_${i + 1}`,
    display_name: names[i] ?? `Organização ${i + 1}`,
    owner_user_id: i === 0 ? 'mock_user' : 'mock_other',
    role: count === 1 ? role : MOCK_ROLE_CYCLE[i % MOCK_ROLE_CYCLE.length],
    joined_at: new Date(Date.now() - (i + 1) * 30 * 86_400_000).toISOString(),
  }))
}

function mockSupportMessage(overrides: Partial<SupportMessage>): SupportMessage {
  return {
    author_type: 'user',
    body: '',
    created_at: new Date().toISOString(),
    ...overrides,
  }
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
    support_role: mockSupportRole(),
    created_at: new Date('2026-01-01').toISOString(),
    terms_pending: { tos: false, privacy: false },
  } as User,
  organizations: [] as Organization[],
  members: {} as Record<string, OrganizationMember[]>,
  companies: {} as Record<string, Company[]>,
  companyActors: {} as Record<string, CompanyActor[]>,
  invitations: {} as Record<string, OrganizationInvitation[]>,
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
      { scope: 'account:profile:read', description: 'Read your profile', description_pt: 'Ler seu perfil' },
      { scope: 'account:profile:write', description: 'Update your profile', description_pt: 'Atualizar seu perfil' },
      { scope: 'account:security:write', description: 'Manage sign-in methods', description_pt: 'Gerenciar métodos de acesso' },
      { scope: 'account:sessions:read', description: 'Read your sessions', description_pt: 'Ler suas sessões' },
      { scope: 'account:sessions:revoke', description: 'Revoke your sessions', description_pt: 'Revogar suas sessões' },
      { scope: 'account:activity:read', description: 'Read security activity', description_pt: 'Ler atividades de segurança' },
      { scope: 'account:api-keys:read', description: 'Read your API keys', description_pt: 'Ler suas chaves de API' },
      { scope: 'account:api-keys:write', description: 'Create and revoke API keys', description_pt: 'Criar e revogar chaves de API' },
      { scope: 'account:oauth-clients:read', description: 'Read your OAuth clients', description_pt: 'Ler seus clientes OAuth' },
      { scope: 'account:oauth-clients:write', description: 'Manage your OAuth clients', description_pt: 'Gerenciar seus clientes OAuth' },
      { scope: 'account:consents:read', description: 'Read connected applications', description_pt: 'Ler aplicações conectadas' },
      { scope: 'account:consents:revoke', description: 'Revoke application consent', description_pt: 'Revogar consentimento de aplicações' },
      { scope: 'account:mfa:read', description: 'Read MFA methods', description_pt: 'Ler métodos de MFA' },
      { scope: 'account:mfa:write', description: 'Manage MFA methods', description_pt: 'Gerenciar métodos de MFA' },
      { scope: 'account:kyc:read', description: 'Read identity-verification status', description_pt: 'Ler status da verificação de identidade' },
      { scope: 'account:kyc:write', description: 'Manage identity-verification data', description_pt: 'Gerenciar dados da verificação de identidade' },
      { scope: 'account:terms:write', description: 'Accept pending terms', description_pt: 'Aceitar termos pendentes' },
    ] },
    { service: 'identity', name: 'Identity (OIDC)', scopes: [
      { scope: 'openid', description: 'OpenID Connect identifier', description_pt: 'Identificador OpenID Connect' },
      { scope: 'profile', description: 'Read your basic identity profile', description_pt: 'Ler seu perfil básico de identidade' },
      { scope: 'email', description: 'Read your email address', description_pt: 'Ler seu endereço de e-mail' },
      { scope: 'kyc', description: 'Read your verified identity level', description_pt: 'Ler seu nível de identidade verificada' },
    ] },
  ] as ScopeService[],
  // Seeded to exercise every ticket-thread scenario: open, answered, closed
  // without an NPS score yet, closed with one already submitted, and one
  // anonymous submission (no user_id) that only the admin queue — not "my
  // tickets" — should surface.
  supportTickets: [
    { id: 'TICKET_supp_open', ticket_number: 101, user_id: 'mock_user', subject_category: 'account', subject_other: 'Conta e login — Não consigo fazer login', priority: 'high', status: 'open', escalation_level: 'none', created_at: new Date(Date.now() - 2 * 3_600_000).toISOString(), last_message_at: new Date(Date.now() - 2 * 3_600_000).toISOString() },
    { id: 'TICKET_supp_answered', ticket_number: 102, user_id: 'mock_user', subject_category: 'wallet', subject_other: 'Wallet — Problema com depósito', priority: 'medium', status: 'answered', escalation_level: 'specialist', created_at: new Date(Date.now() - 2 * 86_400_000).toISOString(), last_message_at: new Date(Date.now() - 3_600_000).toISOString() },
    { id: 'TICKET_supp_closed_no_nps', ticket_number: 103, user_id: 'mock_user', subject_category: 'kyc', subject_other: 'KYC e verificação — Documentos reprovados', priority: 'low', status: 'closed', escalation_level: 'none', created_at: new Date(Date.now() - 6 * 86_400_000).toISOString(), last_message_at: new Date(Date.now() - 5 * 86_400_000).toISOString() },
    { id: 'TICKET_supp_closed_with_nps', ticket_number: 104, user_id: 'mock_user', subject_category: 'billing', subject_other: 'Billing — Cobrança indevida', priority: 'urgent', status: 'closed', escalation_level: 'none', created_at: new Date(Date.now() - 10 * 86_400_000).toISOString(), last_message_at: new Date(Date.now() - 9 * 86_400_000).toISOString(), nps_score: 5 },
    { id: 'TICKET_supp_anon', ticket_number: 105, subject_category: 'other', subject_other: 'Problema não listado no catálogo', priority: 'critical', status: 'open', escalation_level: 'engineering', created_at: new Date(Date.now() - 30 * 60_000).toISOString(), last_message_at: new Date(Date.now() - 30 * 60_000).toISOString() },
  ] as SupportTicket[],
  supportMessages: {
    TICKET_supp_open: [
      mockSupportMessage({ author_type: 'system', body: 'Ticket criado.', created_at: new Date(Date.now() - 2 * 3_600_000).toISOString() }),
      mockSupportMessage({ author_type: 'user', body: 'Não consigo fazer login desde ontem à noite, a senha não é aceita.', created_at: new Date(Date.now() - 2 * 3_600_000 + 1000).toISOString() }),
    ],
    TICKET_supp_answered: [
      mockSupportMessage({ author_type: 'system', body: 'Ticket criado.', created_at: new Date(Date.now() - 2 * 86_400_000).toISOString() }),
      mockSupportMessage({ author_type: 'user', body: 'Fiz um depósito e o saldo não atualizou.', created_at: new Date(Date.now() - 2 * 86_400_000 + 1000).toISOString() }),
      mockSupportMessage({ author_type: 'agent', body: 'Olá! Verificamos e o depósito foi processado, deve refletir em até 1 hora. Qualquer coisa nos avise.', created_at: new Date(Date.now() - 3_600_000).toISOString() }),
    ],
    TICKET_supp_closed_no_nps: [
      mockSupportMessage({ author_type: 'system', body: 'Ticket criado.', created_at: new Date(Date.now() - 6 * 86_400_000).toISOString() }),
      mockSupportMessage({ author_type: 'user', body: 'Meus documentos de KYC foram reprovados sem explicação clara.', created_at: new Date(Date.now() - 6 * 86_400_000 + 1000).toISOString() }),
      mockSupportMessage({ author_type: 'agent', body: 'A foto do documento estava ilegível. Por favor reenvie pelo fluxo de KYC.', created_at: new Date(Date.now() - 5 * 86_400_000 - 1000).toISOString() }),
      mockSupportMessage({ author_type: 'system', body: 'Status alterado para "closed".', created_at: new Date(Date.now() - 5 * 86_400_000).toISOString() }),
    ],
    TICKET_supp_closed_with_nps: [
      mockSupportMessage({ author_type: 'system', body: 'Ticket criado.', created_at: new Date(Date.now() - 10 * 86_400_000).toISOString() }),
      mockSupportMessage({ author_type: 'user', body: 'Fui cobrado duas vezes no mesmo mês.', created_at: new Date(Date.now() - 10 * 86_400_000 + 1000).toISOString() }),
      mockSupportMessage({ author_type: 'agent', body: 'Identificamos a duplicidade e o estorno já foi processado.', created_at: new Date(Date.now() - 9 * 86_400_000 - 1000).toISOString() }),
      mockSupportMessage({ author_type: 'system', body: 'Status alterado para "closed".', created_at: new Date(Date.now() - 9 * 86_400_000).toISOString() }),
      mockSupportMessage({ author_type: 'system', body: 'Avaliação NPS registrada.', created_at: new Date(Date.now() - 9 * 86_400_000 + 500).toISOString() }),
    ],
    TICKET_supp_anon: [
      mockSupportMessage({ author_type: 'system', body: 'Ticket criado.', created_at: new Date(Date.now() - 30 * 60_000).toISOString() }),
      mockSupportMessage({ author_type: 'user', body: 'Não encontrei uma categoria para o meu problema na lista.', created_at: new Date(Date.now() - 30 * 60_000 + 1000).toISOString() }),
    ],
  } as Record<string, SupportMessage[]>,
  supportInternalNotes: {} as Record<string, Array<{id: string; author_id: string; body: string; created_at: string}>>,
  adminKYCReview: {
    user_id: 'mock_kyc_subject', legal_name: 'Marina Andrade', submitted_at: new Date(Date.now() - 4 * 3_600_000).toISOString(),
    status: 'pending', risk_score: 18, cpf: '11144477735', birth_date: '1992-06-14', phone_number: '+5511987654321',
    address: {zip_code: '01310100', street: 'Av. Paulista', number: '1000', district: 'Bela Vista', city: 'São Paulo', state: 'SP'},
    risk_signals: ['device:new'], risk_evaluated_at: new Date(Date.now() - 4 * 3_600_000).toISOString(),
    documents: [
      {id: 'mock_front', type: 'id_front', uploaded_at: new Date(Date.now() - 4 * 3_600_000).toISOString()},
      {id: 'mock_back', type: 'id_back', uploaded_at: new Date(Date.now() - 4 * 3_600_000).toISOString()},
      {id: 'mock_selfie', type: 'selfie_with_document', uploaded_at: new Date(Date.now() - 4 * 3_600_000).toISOString()},
    ],
  } as AdminKYCReview,
  adminKYCAudit: [] as AdminKYCAuditEvent[],
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
  {
    method: 'get',
    pattern: /^\/v1\.0\/organizations$/,
    handle: () => {
      // Re-seeded per request so a scenario change in localStorage takes
      // effect on the next refresh, like the support role above.
      state.organizations = seedOrganizations()
      for (const org of state.organizations) {
        if (!state.members[org.id]) {
          state.members[org.id] = [
            {
              organization_id: org.id,
              user_id: org.owner_user_id,
              name: org.owner_user_id === 'mock_user' ? 'Mock User' : 'Sócia Fictícia',
              role: 'owner',
              created_at: org.joined_at,
            },
          ]
          if (org.owner_user_id !== 'mock_user') {
            state.members[org.id].push({
              organization_id: org.id,
              user_id: 'mock_user',
              name: 'Mock User',
              role: org.role,
              created_at: org.joined_at,
            })
          }
        }
        state.invitations[org.id] ??= []
      }
      return { organizations: state.organizations }
    },
  },
  {
    method: 'post',
    pattern: /^\/v1\.0\/organizations$/,
    handle: (_m, body) => {
      const org: Organization = {
        id: mockId('org'),
        display_name: String(body.display_name ?? 'Nova organização'),
        owner_user_id: 'mock_user',
        role: 'owner',
        joined_at: new Date().toISOString(),
      }
      state.organizations.unshift(org)
      state.members[org.id] = [
        {
          organization_id: org.id,
          user_id: 'mock_user',
          name: 'Mock User',
          role: 'owner',
          created_at: org.joined_at,
        },
      ]
      state.invitations[org.id] = []
      return org
    },
  },
  {
    method: 'get',
    pattern: /^\/v1\.0\/organizations\/([^/]+)$/,
    handle: (m, _b, config) => {
      const org = state.organizations.find((o) => o.id === m[1])
      // One answer for "not a member" and "no such organization", exactly as
      // the API gives — the mock must not be more forthcoming than production.
      if (!org) {
        return fail(403, { type: 'https://accounts.aoctech.app/problems/forbidden', status: 403 }, config)
      }
      return org
    },
  },
  {
    method: 'patch',
    pattern: /^\/v1\.0\/organizations\/([^/]+)$/,
    handle: (m, body) => {
      const org = state.organizations.find((o) => o.id === m[1])
      if (org) org.display_name = String(body.display_name ?? org.display_name)
      return {}
    },
  },
  {
    method: 'get',
    pattern: /^\/v1\.0\/organizations\/([^/]+)\/members$/,
    handle: (m) => ({ members: state.members[m[1]] ?? [] }),
  },
  {
    method: 'get',
    pattern: /^\/v1\.0\/organizations\/([^/]+)\/companies$/,
    handle: (m) => ({ companies: state.companies[m[1]] ?? [] }),
  },
  {
    // Outside the organization scope, exactly as the server mounts it: the
    // create screen needs a lookup before an organization exists.
    method: 'get',
    pattern: /^\/v1\.0\/companies\/lookup$/,
    handle: (_m, _body, config) => {
      const raw = new URL(config.url ?? '', 'http://mock').searchParams.get('tax_id') ?? ''
      const canonical = raw.replace(/[^0-9A-Za-z]/g, '').toUpperCase()
      // One CNPJ the register knows, everything else a miss — so the "type
      // them yourself" path is reachable in mock mode without an outage.
      if (canonical === MOCK_KNOWN_CNPJ) {
        return { found: true, legal_name: 'ACME COMERCIO LTDA', trade_name: 'Acme' }
      }
      return { found: false }
    },
  },
  {
    method: 'post',
    pattern: /^\/v1\.0\/organizations\/([^/]+)\/companies$/,
    handle: (m, body, config) => {
      const orgID = m[1]
      const b = body as { tax_id: string; legal_name: string; trade_name?: string }
      const canonical = b.tax_id.replace(/[^0-9A-Za-z]/g, '').toUpperCase()
      const existing = state.companies[orgID] ?? []
      if (existing.some((c) => c.tax_id === canonical)) {
        fail(409, {
          type: 'https://accounts.aoctech.app/problems/conflict',
          status: 409,
          detail: 'This organization already has a company with that CNPJ or CPF.',
        }, config)
      }
      const company: Company = {
        id: `cmp_${Date.now()}`,
        tax_id: canonical,
        tax_id_kind: canonical.length === 11 ? 'cpf' : 'cnpj',
        legal_name: b.legal_name,
        trade_name: b.trade_name || undefined,
        created_at: new Date().toISOString(),
      }
      state.companies[orgID] = [...existing, company]
      return company
    },
  },
  {
    method: 'get',
    pattern: /^\/v1\.0\/organizations\/([^/]+)\/companies\/([^/]+)\/actors$/,
    handle: (m) => ({ actors: state.companyActors[`${m[1]}|${m[2]}`] ?? [] }),
  },
  {
    method: 'get',
    pattern: /^\/v1\.0\/organizations\/([^/]+)\/companies\/([^/]+)$/,
    handle: (m, _body, config) => {
      const found = (state.companies[m[1]] ?? []).find((c) => c.id === m[2])
      if (!found) {
        fail(403, {
          type: 'https://accounts.aoctech.app/problems/forbidden',
          status: 403,
          detail: 'You do not have access to this organization.',
        }, config)
      }
      return found
    },
  },
  {
    method: 'patch',
    pattern: /^\/v1\.0\/organizations\/([^/]+)\/members\/([^/]+)$/,
    handle: (m, body) => {
      const member = (state.members[m[1]] ?? []).find((x) => x.user_id === decodeURIComponent(m[2]))
      if (member && member.role !== 'owner') member.role = body.role as OrganizationRole
      return {}
    },
  },
  {
    method: 'delete',
    pattern: /^\/v1\.0\/organizations\/([^/]+)\/members\/([^/]+)$/,
    handle: (m) => {
      const userId = decodeURIComponent(m[2])
      state.members[m[1]] = (state.members[m[1]] ?? []).filter(
        (x) => x.user_id !== userId || x.role === 'owner',
      )
      if (userId === 'mock_user') {
        state.organizations = state.organizations.filter((o) => o.id !== m[1])
      }
      return {}
    },
  },
  {
    method: 'get',
    pattern: /^\/v1\.0\/organizations\/([^/]+)\/invitations$/,
    handle: (m) => ({ invitations: state.invitations[m[1]] ?? [] }),
  },
  {
    method: 'post',
    pattern: /^\/v1\.0\/organizations\/([^/]+)\/invitations$/,
    handle: (m, body) => {
      const invitation: OrganizationInvitation = {
        email: String(body.email ?? '').toLowerCase(),
        role: (body.role as OrganizationRole) ?? 'member',
        invited_by: 'mock_user',
        expires_at: new Date(Date.now() + 7 * 86_400_000).toISOString(),
      }
      state.invitations[m[1]] = [
        ...(state.invitations[m[1]] ?? []).filter((i) => i.email !== invitation.email),
        invitation,
      ]
      return { ...invitation, token: MOCK_VALID_INVITE_TOKEN }
    },
  },
  {
    method: 'delete',
    pattern: /^\/v1\.0\/organizations\/([^/]+)\/invitations\/([^/]+)$/,
    handle: (m) => {
      const email = decodeURIComponent(m[2]).toLowerCase()
      state.invitations[m[1]] = (state.invitations[m[1]] ?? []).filter((i) => i.email !== email)
      return {}
    },
  },
  {
    method: 'post',
    pattern: /^\/v1\.0\/organizations\/([^/]+)\/transfer$/,
    handle: (m, body) => {
      const members = state.members[m[1]] ?? []
      const from = members.find((x) => x.role === 'owner')
      const to = members.find((x) => x.user_id === String(body.user_id))
      if (from && to) {
        from.role = 'admin'
        to.role = 'owner'
        const org = state.organizations.find((o) => o.id === m[1])
        if (org) {
          org.owner_user_id = to.user_id
          if (from.user_id === 'mock_user') org.role = 'admin'
          if (to.user_id === 'mock_user') org.role = 'owner'
        }
      }
      return {}
    },
  },
  {
    method: 'post',
    pattern: /^\/v1\.0\/invitations\/accept$/,
    // All three branches of /invite are reachable from the browser: one token
    // succeeds, one hits the verification gate, anything else is refused the
    // way an expired or stranger's invitation is.
    handle: (_m, body, config) => {
      const token = String(body.token ?? '')
      if (token === MOCK_UNVERIFIED_INVITE_TOKEN) {
        return fail(
          403,
          { type: 'https://accounts.aoctech.app/problems/email-not-verified', status: 403 },
          config,
        )
      }
      if (token !== MOCK_VALID_INVITE_TOKEN) {
        return fail(
          403,
          { type: 'https://accounts.aoctech.app/problems/forbidden', status: 403 },
          config,
        )
      }
      const org = state.organizations[0] ?? seedOrganizations()[0]
      return {
        organization_id: org?.id ?? 'org_mock_1',
        user_id: 'mock_user',
        role: 'member',
        created_at: new Date().toISOString(),
      }
    },
  },
  {
    method: 'get',
    pattern: /^\/v1\.0\/account\/profile$/,
    handle: () => {
      // Read the role at request time so browser-based QA can switch roles
      // without restarting the mock module.
      state.user.support_role = mockSupportRole()
      return state.user
    },
  },
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
  { method: 'get', pattern: /^\/v1\.0\/account\/support\/tickets$/, handle: () => ({ tickets: state.supportTickets.filter((t) => t.user_id === state.user.user_id), next_cursor: '' }) },
  {
    method: 'get',
    pattern: /^\/v1\.0\/admin\/support\/tickets$/,
    handle: (_m, _b, config) => {
      const status = new URL(config.url ?? '', 'http://mock').searchParams.get('status') || 'open'
      return { tickets: state.supportTickets.filter((t) => t.status === status), next_cursor: '' }
    },
  },
  {
    method: 'get',
    pattern: /^\/v1\.0\/admin\/support\/tickets\/([^/]+)$/,
    handle: (m, _b, config) => {
      const ticket = state.supportTickets.find((t) => t.id.replace('TICKET_', '') === m[1])
      if (!ticket) fail(404, { detail: 'Ticket not found' }, config)
      return { ticket, messages: state.supportMessages[ticket.id] ?? [], internal_notes: state.supportInternalNotes[ticket.id] ?? [] }
    },
  },
  {
    method: 'get', pattern: /^\/v1\.0\/admin\/kyc\/reviews$/,
    handle: (_m, _b, config) => {
      const status = new URL(config.url ?? '', 'http://mock').searchParams.get('status') || 'pending'
      const review = state.adminKYCReview
      const matches = status === 'pending' ? review.status === 'pending' : review.status !== 'pending'
      return {reviews: matches ? [review] : []}
    },
  },
  {
    method: 'get', pattern: /^\/v1\.0\/admin\/kyc\/reviews\/([^/]+)$/,
    handle: (m, _b, config) => {
      if (m[1] !== state.adminKYCReview.user_id) fail(404, {detail: 'KYC review not found'}, config)
      return {review: state.adminKYCReview, audit_log: state.adminKYCAudit}
    },
  },
  {
    method: 'get',
    pattern: /^\/v1\.0\/support\/tickets\/([^/]+)$/,
    handle: (m, _b, config) => {
      const ticket = state.supportTickets.find((t) => t.id.replace('TICKET_', '') === m[1])
      if (!ticket) fail(404, { detail: 'Ticket not found' }, config)
      return { ticket, messages: state.supportMessages[ticket.id] ?? [] }
    },
  },

  {
    method: 'post',
    pattern: /^\/v1\.0\/support\/tickets$/,
    handle: (_m, body, config) => {
      const isAuthenticated = Boolean(config.headers?.Authorization)
      const id = mockId('TICKET')
      const ticket: SupportTicket = {
        id,
        ticket_number: state.supportTickets.length + 101,
        user_id: isAuthenticated ? state.user.user_id : undefined,
        subject_category: String(body.subject_category ?? 'other'),
        subject_other: body.subject_other ? String(body.subject_other) : undefined,
        priority: (body.priority as SupportTicket['priority']) ?? 'low',
        status: 'open',
        escalation_level: 'none',
        created_at: new Date().toISOString(),
        last_message_at: new Date().toISOString(),
      }
      state.supportTickets.unshift(ticket)
      state.supportMessages[id] = [
        mockSupportMessage({ author_type: 'system', body: 'Ticket criado.' }),
        mockSupportMessage({ author_type: 'user', body: String(body.body ?? '') }),
      ]
      const anonymousToken = ticket.user_id ? undefined : mockId('anontoken')
      return { ticket_id: id.replace('TICKET_', ''), ticket_number: ticket.ticket_number, anonymous_token: anonymousToken }
    },
  },
  {
    method: 'post',
    pattern: /^\/v1\.0\/support\/tickets\/([^/]+)\/reply$/,
    handle: (m, body, config) => {
      const ticket = state.supportTickets.find((t) => t.id.replace('TICKET_', '') === m[1])
      if (!ticket) fail(404, { detail: 'Ticket not found' }, config)
      const now = new Date().toISOString()
      state.supportMessages[ticket.id] = [...(state.supportMessages[ticket.id] ?? []), mockSupportMessage({ author_type: 'user', body: String(body.body ?? ''), created_at: now })]
      ticket.last_message_at = now
      if (ticket.status === 'closed') fail(422, {detail: 'Closed tickets are immutable'}, config)
      return {}
    },
  },
  {
    method: 'post',
    pattern: /^\/v1\.0\/admin\/support\/tickets\/([^/]+)\/notes$/,
    handle: (m, body, config) => {
      const ticket = state.supportTickets.find((t) => t.id.replace('TICKET_', '') === m[1])
      if (!ticket) fail(404, {detail: 'Ticket not found'}, config)
      const note = {id: mockId('NOTE'), author_id: state.user.user_id, body: String(body.body ?? ''), created_at: new Date().toISOString()}
      state.supportInternalNotes[ticket.id] = [...(state.supportInternalNotes[ticket.id] ?? []), note]
      return note
    },
  },
  {
    method: 'post',
    pattern: /^\/v1\.0\/support\/tickets\/([^/]+)\/nps$/,
    handle: (m, body, config) => {
      const ticket = state.supportTickets.find((t) => t.id.replace('TICKET_', '') === m[1])
      if (!ticket) fail(404, { detail: 'Ticket not found' }, config)
      ticket.nps_score = Number(body.score ?? 0)
      return {}
    },
  },
  {
    method: 'post',
    pattern: /^\/v1\.0\/admin\/support\/tickets\/([^/]+)\/reply$/,
    handle: (m, body, config) => {
      const ticket = state.supportTickets.find((t) => t.id.replace('TICKET_', '') === m[1])
      if (!ticket) fail(404, { detail: 'Ticket not found' }, config)
      const now = new Date().toISOString()
      state.supportMessages[ticket.id] = [...(state.supportMessages[ticket.id] ?? []), mockSupportMessage({ author_type: 'agent', body: String(body.body ?? ''), created_at: now })]
      ticket.status = 'answered'
      ticket.last_message_at = now
      return { message: state.supportMessages[ticket.id].at(-1), ticket }
    },
  },
  {
    method: 'post', pattern: /^\/v1\.0\/admin\/kyc\/reviews\/([^/]+)\/documents\/access$/,
    handle: (m, _b, config) => {
      if (m[1] !== state.adminKYCReview.user_id) fail(404, {detail: 'KYC review not found'}, config)
      state.adminKYCAudit.unshift({event_type: 'kyc.documents_viewed', created_at: new Date().toISOString(), actor_id: state.user.user_id, actor_name: state.user.display_name || `${state.user.first_name} ${state.user.last_name}`, actor_role: state.user.support_role})
      return {documents: state.adminKYCReview.documents.map((document) => ({...document, url: 'https://example.invalid/mock-kyc-document'})), expires_in: 600}
    },
  },
  {
    method: 'post', pattern: /^\/v1\.0\/admin\/kyc\/reviews\/([^/]+)\/decision$/,
    handle: (m, body, config) => {
      if (m[1] !== state.adminKYCReview.user_id || state.adminKYCReview.status !== 'pending') fail(409, {detail: 'Review already completed'}, config)
      const decision = body.decision === 'reject' ? 'reject' : 'approve'
      state.adminKYCReview.status = decision === 'approve' ? 'verified' : 'rejected'
      state.adminKYCReview.decision = decision
      state.adminKYCReview.reviewed_at = new Date().toISOString()
      state.adminKYCReview.reviewed_by = state.user.user_id
      state.adminKYCReview.reviewed_by_name = state.user.display_name || `${state.user.first_name} ${state.user.last_name}`
      state.adminKYCReview.rejection_code = decision === 'reject' ? body.reason_code as AdminKYCReview['rejection_code'] : undefined
      state.adminKYCReview.rejection_reason = decision === 'reject' ? String(body.details ?? '') : undefined
      state.adminKYCAudit.unshift({event_type: decision === 'approve' ? 'kyc.verified' : 'kyc.rejected', created_at: state.adminKYCReview.reviewed_at, actor_id: state.user.user_id, actor_name: state.adminKYCReview.reviewed_by_name, actor_role: state.user.support_role, reason_code: state.adminKYCReview.rejection_code, details: state.adminKYCReview.rejection_reason})
      return {}
    },
  },
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
  { method: 'post', pattern: /^\/v1\.0\/auth\/step-up$/, handle: () => ({}) },
  { method: 'post', pattern: /^\/v1\.0\/auth\/step-up\/passkeys\/begin$/, handle: () => ({ session_token: mockId('stepup'), options: '{}' }) },
  { method: 'post', pattern: /^\/v1\.0\/auth\/step-up\/passkeys\/complete/, handle: () => ({}) },
  {
    method: 'post',
    pattern: /^\/v1\.0\/account\/kyc\/basic$/,
    handle: (_m, body) => {
      state.kyc = {
        ...state.kyc,
        state: 'basic_verified',
        level: 'basic',
        legal_name: String(body.legal_name ?? ''),
        birth_date: String(body.birth_date ?? ''),
        cpf_masked: '***.***.***-00',
        phone_masked: '***' + String(body.phone_number ?? '').slice(-4),
        submitted_at: new Date().toISOString(),
        basic_verified_at: new Date().toISOString(),
      }
      return state.kyc
    },
  },
  {
    method: 'post',
    pattern: /^\/v1\.0\/account\/kyc\/enhanced$/,
    handle: () => {
      state.kyc = {
        ...state.kyc,
        state: 'under_review',
        level: 'enhanced',
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
      state.kyc = { ...state.kyc, documents }
      return state.kyc
    },
  },

  {
    method: 'get', pattern: /^\/v1\.0\/admin\/support\/metrics$/,
    handle: () => ({buckets: [
      {period: `day#${new Date().toISOString().slice(0, 10)}`, created_count: 6, resolved_count: 4, average_resolution_seconds: 5400, tickets_by_product: {account: 3, wallet: 2, kyc: 1}},
      {period: 'all', created_count: 151, resolved_count: 128, average_resolution_seconds: 8100, tickets_by_product: {account: 49, wallet: 36, kyc: 28, billing: 20, dfe: 10, poker: 8}},
    ]}),
  },
  {
    method: 'put',
    pattern: /^\/v1\.0\/admin\/support\/tickets\/([^/]+)\/escalation$/,
    handle: (m, body, config) => {
      const ticket = state.supportTickets.find((t) => t.id.replace('TICKET_', '') === m[1])
      if (!ticket) fail(404, {detail: 'Ticket not found'}, config)
      ticket.escalation_level = String(body.level ?? 'none') as SupportTicket['escalation_level']
      return {}
    },
  },
  {
    method: 'put',
    pattern: /^\/v1\.0\/admin\/support\/tickets\/([^/]+)\/status$/,
    handle: (m, body, config) => {
      const ticket = state.supportTickets.find((t) => t.id.replace('TICKET_', '') === m[1])
      if (!ticket) fail(404, { detail: 'Ticket not found' }, config)
      ticket.status = String(body.status ?? ticket.status) as SupportTicket['status']
      return {}
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
