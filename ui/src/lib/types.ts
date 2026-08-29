/** Documents whose published version moved past the one this account accepted. */
export type TermsPending = {
  tos: boolean
  privacy: boolean
}

export type User = {
  user_id: string
  email: string
  first_name: string
  last_name: string
  display_name: string | null
  avatar_url: string | null
  email_verified: boolean
  /** False for Google-created accounts that never set a password. Drives the "create a password" vs "change password" UI. */
  has_password: boolean
  /** Whether a Google identity is bound — drives the Link/Unlink Google UI. The raw sub is never exposed. */
  google_linked: boolean
  support_role: '' | 'agent' | 'manager' | 'admin'
  created_at: string
  terms_pending: TermsPending
}

export type SupportTicket = { id: string; ticket_number: number; user_id?: string; subject_category: string; subject_other?: string; priority: 'low'|'medium'|'high'|'urgent'|'critical'; status: 'open'|'answered'|'closed'; escalation_level: 'none'|'specialist'|'engineering'; escalated_at?: string; created_at: string; closed_at?: string; last_message_at: string; nps_score?: number }
export type SupportMessage = { author_type: 'user'|'agent'|'system'; body: string; created_at: string }
export type SupportInternalNote = { id: string; author_id: string; body: string; created_at: string }
export type SupportMetricBucket = { period: string; created_count: number; resolved_count: number; average_resolution_seconds: number; tickets_by_product: Record<string, number> }

export type Session = {
  session_id: string
  device_name: string
  ip_address: string
  created_at: string
  last_used_at: string
  is_current: boolean
  geo_city: string
  geo_region: string
  geo_latitude: number
  geo_longitude: number
}

export type APIKey = {
  key_id: string
  key_prefix: string
  name: string
  scopes: string[]
  last_used_at: string | null
  expires_at: string | null
  created_at: string
}

export type Passkey = {
  id: string
  name: string
  aaguid: string
  created_at: string
  last_used_at: string | null
}

export type OAuthClient = {
  client_id: string
  name: string
  client_type: 'public' | 'confidential'
  redirect_uris: string[]
  allowed_scopes: string[]
  audience: string[] | null
  created_at: string
  updated_at: string
  /** Present only in the creation response — shown exactly once. */
  client_secret?: string
}

export type ScopeEntry = {
  scope: string
  description: string
  description_pt: string
}

export type ScopeService = {
  service: string
  name: string
  scopes: ScopeEntry[]
}

export type ConsentGrant = {
  client_id: string
  client_name: string
  scopes: string[]
  created_at: string
  updated_at: string
}

export type ProblemDetail = {
  type: string
  title: string
  status: number
  detail: string
  instance: string
}

export interface ActivityEvent {
  event_type: string
  ip: string
  user_agent: string
  metadata: Record<string, string>
  created_at: string
}

export interface ActivityPage {
  events: ActivityEvent[]
  next_cursor: string
}

export type KYCLevel = '' | 'basic' | 'enhanced'

/** Derived by the API from level+status+expiry — branch on this. */
export type KYCState =
  | 'not_started'
  | 'basic_verified'
  | 'under_review'
  | 'rejected'
  | 'verified'

/** A static photo holding the document replaces the old four-clip video liveness check. */
export type KYCDocumentType = 'id_front' | 'id_back' | 'selfie_with_document'

export interface KYCDocument {
  id: string
  type: KYCDocumentType
  uploaded_at: string
}

export interface KYCStatus {
  state: KYCState
  level: KYCLevel
  cpf_masked?: string
  legal_name?: string
  birth_date?: string
  phone_masked?: string
  basic_verified_at?: string
  documents?: KYCDocument[]
  rejection_reason?: string
  rejection_code?: KYCRejectionCode
  submitted_at?: string
  expires_at?: string
  verified_at?: string
}

export interface KYCBasicSubmission {
  cpf: string
  legal_name: string
  birth_date: string
  phone_number: string
  address: KYCAddress
}

export interface KYCAddress {
  zip_code: string
  street: string
  number: string
  complement?: string
  district: string
  city: string
  state: string
}

/** Response fields consumed from the public ViaCEP address-assistance service. */
export interface ViaCEPResponse {
  erro?: boolean
  logradouro?: string
  bairro?: string
  localidade?: string
  uf?: string
}

export interface PresignedUpload {
  document_id: string
  upload_url: string
  expires_in: number
  max_bytes: number
  content_type: string
}

export type KYCReviewQueue = 'pending' | 'completed'
export type KYCReviewDecision = 'approve' | 'reject'
export type KYCRejectionCode = 'document_unreadable' | 'document_incomplete' | 'document_mismatch' | 'selfie_mismatch' | 'data_mismatch' | 'suspected_fraud' | 'other'

export interface AdminKYCReviewSummary {
  user_id: string
  legal_name: string
  submitted_at: string
  status: 'pending' | 'verified' | 'rejected'
  risk_score: number
  reviewed_at?: string
  reviewed_by?: string
  reviewed_by_name?: string
  decision?: KYCReviewDecision
}

export interface AdminKYCReview extends AdminKYCReviewSummary {
  cpf: string
  birth_date: string
  phone_number: string
  address: KYCAddress
  risk_signals: string[]
  risk_evaluated_at?: string
  documents: KYCDocument[]
  rejection_reason?: string
  rejection_code?: KYCRejectionCode
  expires_at?: string
}

export interface AdminKYCAuditEvent {
  event_type: 'kyc.documents_viewed' | 'kyc.verified' | 'kyc.rejected'
  created_at: string
  actor_id: string
  actor_name: string
  actor_role: User['support_role']
  reason_code?: KYCRejectionCode
  details?: string
}

export interface AdminKYCDocument extends KYCDocument {
  url: string
}

/** The organization role ladder. `owner` is never assignable through member management. */
export type OrganizationRole = 'owner' | 'admin' | 'member' | 'viewer'

/** Roles the invite and role-change controls may offer. Owner moves only through transfer. */
export const GRANTABLE_ROLES: OrganizationRole[] = ['admin', 'member', 'viewer']

/** One organization as the person who belongs to it sees it. */
export interface Organization {
  id: string
  display_name: string
  owner_user_id: string
  /** The caller's own role. Read fresh from every response — never cached past it. */
  role: OrganizationRole
  joined_at: string
}

export interface OrganizationMember {
  organization_id: string
  user_id: string
  role: OrganizationRole
  created_at: string
}

/** A pending invitation. The token is never here — only the admin who created it saw it. */
export interface OrganizationInvitation {
  email: string
  role: OrganizationRole
  invited_by: string
  expires_at: string
}
