import { api, isAxiosError } from './axios'
import type { User, Session, APIKey, Passkey, OAuthClient, ConsentGrant, ScopeService, ActivityPage, KYCStatus, SupportInternalNote, SupportMessage, SupportMetricBucket, SupportTicket, AdminKYCReview, AdminKYCReviewSummary, AdminKYCAuditEvent, KYCReviewQueue, Organization, OrganizationMember, OrganizationInvitation, Company, CompanyActor } from './types'

export async function fetchProfile(): Promise<User> {
  const { data } = await api.get<User>('/v1.0/account/profile')
  return data
}

export async function fetchSessions(): Promise<Session[]> {
  const { data } = await api.get<{ sessions: Session[] }>('/v1.0/account/sessions')
  return data.sessions ?? []
}

export async function fetchAPIKeys(): Promise<APIKey[]> {
  const { data } = await api.get<{ api_keys: APIKey[] }>('/v1.0/account/api-keys')
  return data.api_keys ?? []
}

export async function fetchOAuthClients(): Promise<OAuthClient[]> {
  const { data } = await api.get<{ oauth_clients: OAuthClient[] }>('/v1.0/account/oauth-clients')
  return data.oauth_clients ?? []
}

export async function fetchScopeCatalog(): Promise<ScopeService[]> {
  const { data } = await api.get<{ services: ScopeService[] }>('/v1.0/scopes')
  return data.services ?? []
}

export async function fetchConsents(): Promise<ConsentGrant[]> {
  const { data } = await api.get<{ consents: ConsentGrant[] }>('/v1.0/account/consents')
  return data.consents ?? []
}

export async function fetchPasskeys(): Promise<Passkey[]> {
  const { data } = await api.get<{ passkeys: Passkey[] }>('/v1.0/account/mfa/passkeys')
  return data.passkeys ?? []
}

export async function fetchTOTPStatus(): Promise<{ enabled: boolean }> {
  const { data } = await api.get<{ enabled: boolean }>('/v1.0/account/mfa/totp')
  return data
}

export async function fetchTOTPSetup(): Promise<{ provisioning_uri: string } | null> {
  try {
    const { data } = await api.get<{ provisioning_uri: string }>('/v1.0/account/mfa/totp/setup')
    return data
  } catch (error) {
    // 409 = already configured; return null so the page shows "already set up"
    if (isAxiosError(error) && error.response?.status === 409) return null
    throw error
  }
}

const ACTIVITY_PAGE_SIZE = 25

export async function fetchActivity(cursor: string): Promise<ActivityPage> {
  const params = new URLSearchParams({ limit: String(ACTIVITY_PAGE_SIZE) })
  if (cursor) params.set('cursor', cursor)
  const { data } = await api.get<ActivityPage>(`/v1.0/account/activity?${params}`)
  return { events: data.events ?? [], next_cursor: data.next_cursor ?? '' }
}

export async function fetchKYC(): Promise<KYCStatus> {
  const { data } = await api.get<KYCStatus>('/v1.0/account/kyc')
  return data
}

export async function fetchSupportTicket(id: string, token = ''): Promise<{ ticket: SupportTicket; messages: SupportMessage[] }> {
  const { data } = await api.get(`/v1.0/support/tickets/${encodeURIComponent(id)}${token ? `?token=${encodeURIComponent(token)}` : ''}`)
  return { ticket: data.ticket, messages: data.messages ?? [] }
}

export async function fetchMySupportTickets(cursor = ''): Promise<{ tickets: SupportTicket[]; next_cursor: string }> {
  const { data } = await api.get(`/v1.0/account/support/tickets${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''}`)
  return { tickets: data.tickets ?? [], next_cursor: data.next_cursor ?? '' }
}

export async function fetchAdminSupportTickets(status = 'open'): Promise<{ tickets: SupportTicket[]; next_cursor: string }> {
  const { data } = await api.get(`/v1.0/admin/support/tickets?status=${encodeURIComponent(status)}`)
  return { tickets: data.tickets ?? [], next_cursor: data.next_cursor ?? '' }
}

export async function fetchAdminSupportTicket(id: string): Promise<{ ticket: SupportTicket; messages: SupportMessage[]; internal_notes: SupportInternalNote[] }> {
  const { data } = await api.get(`/v1.0/admin/support/tickets/${encodeURIComponent(id)}`)
  return { ticket: data.ticket, messages: data.messages ?? [], internal_notes: data.internal_notes ?? [] }
}

export async function fetchAdminSupportMetrics(): Promise<{buckets: SupportMetricBucket[]}> {
  const {data} = await api.get('/v1.0/admin/support/metrics')
  return {buckets: data.buckets ?? []}
}

export async function fetchAdminKYCReviews(status: KYCReviewQueue): Promise<AdminKYCReviewSummary[]> {
  const { data } = await api.get<{ reviews: AdminKYCReviewSummary[] }>(`/v1.0/admin/kyc/reviews?status=${status}`)
  return data.reviews ?? []
}

export async function fetchAdminKYCReview(userId: string): Promise<{ review: AdminKYCReview; audit_log: AdminKYCAuditEvent[] }> {
  const { data } = await api.get<{ review: AdminKYCReview; audit_log: AdminKYCAuditEvent[] }>(`/v1.0/admin/kyc/reviews/${encodeURIComponent(userId)}`)
  return { review: data.review, audit_log: data.audit_log ?? [] }
}

export async function fetchOrganizations(): Promise<Organization[]> {
  const { data } = await api.get<{ organizations: Organization[] }>('/v1.0/organizations')
  return data.organizations ?? []
}

export async function fetchOrganization(id: string): Promise<Organization> {
  const { data } = await api.get<Organization>(`/v1.0/organizations/${encodeURIComponent(id)}`)
  return data
}

export async function fetchOrganizationMembers(id: string): Promise<OrganizationMember[]> {
  const { data } = await api.get<{ members: OrganizationMember[] }>(
    `/v1.0/organizations/${encodeURIComponent(id)}/members`,
  )
  return data.members ?? []
}

export async function fetchOrganizationInvitations(id: string): Promise<OrganizationInvitation[]> {
  const { data } = await api.get<{ invitations: OrganizationInvitation[] }>(
    `/v1.0/organizations/${encodeURIComponent(id)}/invitations`,
  )
  return data.invitations ?? []
}

export async function fetchCompanies(id: string): Promise<Company[]> {
  const { data } = await api.get<{ companies: Company[] }>(
    `/v1.0/organizations/${encodeURIComponent(id)}/companies`,
  )
  return data.companies ?? []
}

export async function fetchCompanyActors(id: string, companyID: string): Promise<CompanyActor[]> {
  const { data } = await api.get<{ actors: CompanyActor[] }>(
    `/v1.0/organizations/${encodeURIComponent(id)}/companies/${encodeURIComponent(companyID)}/actors`,
  )
  return data.actors ?? []
}

/**
 * Fills the names for a CNPJ. Never throws and never rejects: every failure —
 * an outage, a register that has not heard of this CNPJ — means the same thing
 * to the form, which is that the person types the names.
 */
export async function lookupTaxID(
  id: string,
  taxID: string,
): Promise<{ legal_name: string; trade_name: string } | null> {
  try {
    const { data } = await api.get<{ found: boolean; legal_name?: string; trade_name?: string }>(
      `/v1.0/organizations/${encodeURIComponent(id)}/companies/lookup`,
      { params: { tax_id: taxID } },
    )
    return data.found ? { legal_name: data.legal_name ?? '', trade_name: data.trade_name ?? '' } : null
  } catch {
    return null
  }
}
