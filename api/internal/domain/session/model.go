package session

import (
	"strings"
	"time"
)

type Session struct {
	PK string `dynamodbav:"pk"`
	SK string `dynamodbav:"sk"`
	// RefreshTokenHash holds the SSO session token (ctech_session cookie). It is set
	// once at login and never rotated by OAuth token grants — per-client refresh
	// tokens live in their own RefreshToken items so one client's rotation can
	// never invalidate the browser SSO session or another client's tokens.
	RefreshTokenHash string  `dynamodbav:"refresh_token_hash"`
	DeviceName       string  `dynamodbav:"device_name"`
	IPAddress        string  `dynamodbav:"ip_address"`
	UserAgent        string  `dynamodbav:"user_agent"`
	CreatedAt        string  `dynamodbav:"created_at"`
	LastUsedAt       string  `dynamodbav:"last_used_at"`
	ExpiresAt        int64   `dynamodbav:"expires_at"` // Unix epoch — DynamoDB TTL attribute
	GeoCity          string  `dynamodbav:"geo_city,omitempty"`
	GeoRegion        string  `dynamodbav:"geo_region,omitempty"`
	GeoLatitude      float64 `dynamodbav:"geo_latitude,omitempty"`
	GeoLongitude     float64 `dynamodbav:"geo_longitude,omitempty"`

	// AuthTime is when the user actively authenticated (login), Unix epoch.
	AuthTime int64 `dynamodbav:"auth_time,omitempty"`
	// AMR lists the authentication methods used on this session (RFC 8176).
	AMR []string `dynamodbav:"amr,omitempty"`
	// LastMFAAt is the last successful MFA proof (login gate or step-up
	// challenge), Unix epoch. 0 when MFA was never proven on this session.
	LastMFAAt int64 `dynamodbav:"last_mfa_at,omitempty"`
}

// AMR (Authentication Methods References) values, RFC 8176 where applicable.
const (
	AMRPassword = "pwd"
	AMRTOTP     = "otp"
	AMRWebAuthn = "webauthn"
	AMRGoogle   = "google"
)

// IsMFAMethod reports whether m counts as a multi-factor proof.
func IsMFAMethod(m string) bool { return m == AMRTOTP || m == AMRWebAuthn }

const SessionTTL = 90 * 24 * time.Hour

func BuildPK(userID string) string {
	return "USER_" + userID
}

func BuildSK(sessionID string) string {
	return "SESSION_" + sessionID
}

func (s *Session) ID() string {
	return strings.TrimPrefix(s.SK, "SESSION_")
}

func (s *Session) UserID() string {
	return strings.TrimPrefix(s.PK, "USER_")
}

func (s *Session) IsExpired() bool {
	return time.Now().Unix() > s.ExpiresAt
}

// RefreshToken is a per-(session, client) OAuth refresh token with its own
// rotation chain. Stored in the sessions table under the session's user with
// SK REFRESH_{session_id}#{client_id}; the refresh_token_hash attribute shares
// the token-hash-index GSI with Session items (disambiguated by SK prefix).
type RefreshToken struct {
	PK               string `dynamodbav:"pk"` // USER_{user_id}
	SK               string `dynamodbav:"sk"` // REFRESH_{session_id}#{client_id}
	RefreshTokenHash string `dynamodbav:"refresh_token_hash"`
	SessionID        string `dynamodbav:"session_id"`
	ClientID         string `dynamodbav:"client_id"`
	// Scopes are the scopes granted at the authorization that issued this token.
	// Refreshes are clamped to these so a refresh can never widen the grant to
	// the client's full allowed-scope set (scope/kyc_level escalation).
	Scopes     []string `dynamodbav:"scopes,omitempty"`
	CreatedAt  string   `dynamodbav:"created_at"`
	LastUsedAt string   `dynamodbav:"last_used_at"`
	ExpiresAt  int64    `dynamodbav:"expires_at"` // Unix epoch — DynamoDB TTL attribute
}

const refreshSKPrefix = "REFRESH_"
const sessionSKPrefix = "SESSION_"
// consumedSKPrefix marks the sk of a consumed-token marker (see ConsumedToken).
// It is distinct from refreshSKPrefix so the marker never collides with the
// active per-(session,client) refresh item under the same pk.
const consumedSKPrefix = "CONSUMED_"

// refreshSKSeparator joins session and client IDs in the refresh token SK. Both
// are UUIDs / slug-style identifiers, so '#' can never appear inside either part.
const refreshSKSeparator = "#"

func BuildRefreshSK(sessionID, clientID string) string {
	return refreshSKPrefix + sessionID + refreshSKSeparator + clientID
}

func (t *RefreshToken) UserID() string {
	return strings.TrimPrefix(t.PK, "USER_")
}

func (t *RefreshToken) IsExpired() bool {
	return time.Now().Unix() > t.ExpiresAt
}

// ConsumedToken is a marker written when a refresh token is rotated. It maps the
// just-superseded hash back to its session, so a later replay of the old token
// (a token-reuse attempt) can immediately revoke the compromised grant. The
// stale hash is no longer the active refresh_token_hash after rotation, so
// without this marker a reused token could never be traced to a session.
// expires_at carries the session's TTL so DynamoDB prunes the marker
// automatically once the grant is past its useful life.
type ConsumedToken struct {
	PK               string `dynamodbav:"pk"` // USER_{user_id}
	SK               string `dynamodbav:"sk"` // CONSUMED_{superseded_hash}
	RefreshTokenHash string `dynamodbav:"refresh_token_hash"`
	UserID           string `dynamodbav:"user_id"`
	SessionID        string `dynamodbav:"session_id"`
	ClientID         string `dynamodbav:"client_id"`
	ExpiresAt        int64  `dynamodbav:"expires_at"` // Unix epoch — DynamoDB TTL attribute
}
