import {TAX_ID_CNPJ_LENGTH, TAX_ID_CPF_LENGTH} from '@/lib/constants'

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

/** Roles that exist below owner. Owner moves only through transfer. */
export const GRANTABLE_ROLES: OrganizationRole[] = ['admin', 'member', 'viewer']

const ROLE_RANK: Record<OrganizationRole, number> = { viewer: 1, member: 2, admin: 3, owner: 4 }

/** Strictly above. Mirrors `organization.Outranks` on the server. */
export function outranks(role: OrganizationRole, other: OrganizationRole): boolean {
  return ROLE_RANK[role] > ROLE_RANK[other]
}

/**
 * The roles this caller may hand out — everything they strictly outrank, and
 * nothing at all below admin. Mirrors `organization.AssignableRoles`: a
 * dropdown offering a choice the server refuses teaches people the product is
 * broken.
 */
export function assignableRoles(callerRole: OrganizationRole): OrganizationRole[] {
  // The admin floor first: a member outranks a viewer but manages nobody.
  if (ROLE_RANK[callerRole] < ROLE_RANK.admin) return []
  return GRANTABLE_ROLES.filter((role) => outranks(callerRole, role))
}

/**
 * Whether this caller may act on this member at all. Never yourself — demoting
 * yourself is one wrong click and you may not hold the role needed to undo it —
 * and never a peer.
 */
export function canManageMember(
  callerRole: OrganizationRole,
  callerUserID: string,
  member: { user_id: string; role: OrganizationRole },
): boolean {
  if (callerUserID === member.user_id) return false
  return outranks(callerRole, member.role)
}

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
  /**
   * Copied onto the row when the person joined, refreshed when they rename
   * themselves. Absent on rows written before names were stored — render the
   * user id rather than a blank.
   */
  name?: string
  role: OrganizationRole
  created_at: string
}

/** A pending invitation. The token is never here — only the admin who created it saw it. */
export interface OrganizationInvitation {
  email: string
  role: OrganizationRole
  /** Companies this invitation also grants reach to. Absent means none, and
   *  none means the person joins the workspace able to act for no company. */
  company_ids?: string[]
  invited_by: string
  expires_at: string
}

export type TaxIDKind = 'cnpj' | 'cpf'

/**
 * A tax id an organization acts for. Identity only — the fiscal configuration
 * (inscrição estadual, regime, certificate) lives in ctech-dfe, per ADR 0022.
 */
export interface Company {
  id: string
  /**
   * Canonical: mask stripped, letters uppercased. A CNPJ has been alphanumeric
   * in its first twelve positions since 2026, so never assume digits.
   */
  tax_id: string
  tax_id_kind: TaxIDKind
  legal_name: string
  trade_name?: string
  created_at: string
}

/** One person's permission to act for one company. */
export interface CompanyActor {
  user_id: string
  name?: string
  granted_by?: string
  created_at: string
}

/**
 * Masks a canonical tax id for reading. The server stores it unmasked so two
 * spellings of one document cannot both be registered; people read the mask.
 */
export function formatTaxID(taxID: string, kind: TaxIDKind): string {
  if (kind === 'cnpj' && taxID.length === TAX_ID_CNPJ_LENGTH) {
    return `${taxID.slice(0, 2)}.${taxID.slice(2, 5)}.${taxID.slice(5, 8)}/${taxID.slice(8, 12)}-${taxID.slice(12)}`
  }
  if (kind === 'cpf' && taxID.length === TAX_ID_CPF_LENGTH) {
    return `${taxID.slice(0, 3)}.${taxID.slice(3, 6)}.${taxID.slice(6, 9)}-${taxID.slice(9)}`
  }
  return taxID
}

/**
 * Formats tax-id input without losing the alphanumeric CNPJ alphabet. Numeric
 * values read as CPF through 11 digits, then switch to the 14-character CNPJ
 * mask. The API remains authoritative for check-digit validation.
 */
export function formatTaxIDInput(value: string): string {
  const canonical = value
    .replace(/[^0-9A-Za-z]/g, '')
    .toUpperCase()
    .slice(0, TAX_ID_CNPJ_LENGTH)
  const kind: TaxIDKind = /[A-Z]/.test(canonical) || canonical.length > TAX_ID_CPF_LENGTH
    ? 'cnpj'
    : 'cpf'

  if (kind === 'cpf') {
    return canonical
      .replace(/^(\d{3})(\d)/, '$1.$2')
      .replace(/^(\d{3})\.(\d{3})(\d)/, '$1.$2.$3')
      .replace(/^(\d{3})\.(\d{3})\.(\d{3})(\d)/, '$1.$2.$3-$4')
  }

  return canonical
    .replace(/^([0-9A-Z]{2})([0-9A-Z])/, '$1.$2')
    .replace(/^([0-9A-Z]{2})\.([0-9A-Z]{3})([0-9A-Z])/, '$1.$2.$3')
    .replace(/^([0-9A-Z]{2})\.([0-9A-Z]{3})\.([0-9A-Z]{3})([0-9A-Z])/, '$1.$2.$3/$4')
    .replace(/^([0-9A-Z]{2})\.([0-9A-Z]{3})\.([0-9A-Z]{3})\/([0-9A-Z]{4})([0-9A-Z])/, '$1.$2.$3/$4-$5')
}
