export type User = {
  user_id: string
  email: string
  first_name: string
  last_name: string
  display_name: string | null
  avatar_url: string | null
  email_verified: boolean
  created_at: string
}

export type Session = {
  session_id: string
  device_name: string
  ip_address: string
  created_at: string
  last_used_at: string
  is_current: boolean
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

export type ProblemDetail = {
  type: string
  title: string
  status: number
  detail: string
  instance: string
}
