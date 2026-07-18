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
  created_at: string
  terms_pending: TermsPending
}

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

export type KYCLevel = '' | 'verified'

/** Verification is document-only — kept as a type for forward compatibility. */
export type KYCMethod = '' | 'document'

/** Derived by the API from document status — branch on this. */
export type KYCState = 'not_started' | 'awaiting_files' | 'under_review' | 'rejected' | 'verified'

/**
 * The four selfie poses replace a single static photo: a printed photo or
 * looped video can't turn on command, so this is the liveness signal — the
 * reviewer still judges real-vs-photo, no server-side ML.
 */
export type KYCDocumentType = 'id_front' | 'id_back' | 'selfie_up' | 'selfie_down' | 'selfie_left' | 'selfie_right'

export interface Address {
  zip_code: string
  street: string
  number: string
  complement?: string
  district: string
  city: string
  state: string
}

export interface KYCDocument {
  id: string
  type: KYCDocumentType
  uploaded_at: string
}

export interface KYCStatus {
  state: KYCState
  level: KYCLevel
  method?: KYCMethod
  cpf_masked?: string
  legal_name?: string
  birth_date?: string
  address?: Address
  documents?: KYCDocument[]
  rejection_reason?: string
  submitted_at?: string
  expires_at?: string
  verified_at?: string
}

export interface KYCSubmission {
  cpf: string
  legal_name: string
  birth_date: string
  address: Address
}

export interface PresignedUpload {
  document_id: string
  upload_url: string
  expires_in: number
  max_bytes: number
  content_type: string
}
