package crypto

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"gopkg.aoctech.app/account/api/internal/config"
	"gopkg.aoctech.app/account/api/internal/keystore"
	"gopkg.aoctech.app/account/api/internal/utils"
)

// JWTService signs with the active key and verifies against active+previous,
// resolved by the token's kid header. Keys are hot-reloadable (Reload) so the
// rotation loop can swap them without a restart. Signs RS256 or ES256
// depending on the active key's Alg; verification accepts either, resolved
// per-kid.
type JWTService struct {
	mu             sync.RWMutex
	active         *keystore.Key
	previous       *keystore.Key // nil until first rotation
	selfAudience   string        // Verify() rejects tokens whose aud doesn't contain this value
	issuer         string        // Verify() rejects tokens whose iss doesn't match this value
	accessTokenTTL time.Duration
	idTokenTTL     time.Duration
}

// NewJWTService wraps the single key loaded by config (RSA_PRIVATE_KEY or
// EC_PRIVATE_KEY env — dev mode, no rotation) as the active key.
func NewJWTService(cfg *config.Config) (*JWTService, error) {
	if cfg.SigningKey == nil {
		return nil, fmt.Errorf("signing key is nil")
	}
	active := &keystore.Key{KID: cfg.PublicKeyKID, Alg: cfg.SigningKeyAlg, Private: cfg.SigningKey, CreatedAt: time.Now().UTC()}
	return NewJWTServiceWithKeys(cfg, active, nil)
}

// NewJWTServiceWithKeys builds the service from explicit key material
// (SSM-loaded in production). previous may be nil.
func NewJWTServiceWithKeys(cfg *config.Config, active, previous *keystore.Key) (*JWTService, error) {
	if active == nil || active.Private == nil {
		return nil, fmt.Errorf("active signing key is nil")
	}
	accessTTL := cfg.AccessTokenTTL
	if accessTTL == 0 {
		accessTTL = 15 * time.Minute
	}
	return &JWTService{
		active:         active,
		previous:       previous,
		selfAudience:   cfg.Audience,
		issuer:         cfg.AppURL,
		accessTokenTTL: accessTTL,
		idTokenTTL:     time.Hour,
	}, nil
}

// Reload swaps the key set. Safe for concurrent use with signing/verification.
func (j *JWTService) Reload(active, previous *keystore.Key) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.active, j.previous = active, previous
}

// SignAccessToken creates a 15-minute JWT access token, signed with the
// active key's algorithm (RS256 or ES256).
// audience identifies the resource server(s) (backend API URLs).
// clientID is the OAuth client_id; set as azp (authorized party) claim.
// authTime/lastMFAAt/amr mirror the session's step-up state (RFC 8176/OIDC);
// zero values are omitted — api_key-derived tokens carry none of them and can
// therefore never pass a step-up check.
// kycLevel is the user's identity verification level; empty omits the claim
// (callers pass it only when the kyc scope was granted).
func (j *JWTService) SignAccessToken(userID, sessionID, clientID string, scopes []string, issuer string, audience []string, authTime, lastMFAAt int64, amr []string, kycLevel string) (string, error) {
	now := time.Now().UTC()
	claims := jwt.MapClaims{
		"sub":       userID,
		"sid":       sessionID,
		"scope":     strings.Join(scopes, " "),
		"iss":       issuer,
		"aud":       utils.DeduplicateInPlace(audience),
		"azp":       clientID,
		"token_use": "access",
		"iat":       now.Unix(),
		"exp":       now.Add(j.accessTokenTTL).Unix(),
	}
	if authTime > 0 {
		claims["auth_time"] = authTime
	}
	if lastMFAAt > 0 {
		claims["last_mfa_at"] = lastMFAAt
	}
	if len(amr) > 0 {
		claims["amr"] = amr
	}
	if kycLevel != "" {
		claims["kyc_level"] = kycLevel
	}
	return j.sign(claims)
}

// SignIDToken creates a 1-hour JWT id_token per OIDC spec, signed with the
// active key's algorithm (RS256 or ES256).
// kycLevel is included as the kyc_level claim when non-empty.
func (j *JWTService) SignIDToken(userID, email, name, preferredUsername, givenName, familyName string, emailVerified bool, clientID, nonce, issuer, kycLevel string) (string, error) {
	now := time.Now().UTC()
	claims := jwt.MapClaims{
		"sub":                userID,
		"email":              email,
		"name":               name,
		"preferred_username": preferredUsername,
		"given_name":         givenName,
		"family_name":        familyName,
		"iss":                issuer,
		"aud":                []string{clientID},
		"token_use":          "id_token",
		"iat":                now.Unix(),
		"exp":                now.Add(j.idTokenTTL).Unix(),
		"email_verified":     emailVerified,
	}
	if nonce != "" {
		claims["nonce"] = nonce
	}
	if kycLevel != "" {
		claims["kyc_level"] = kycLevel
	}
	return j.sign(claims)
}

func (j *JWTService) sign(claims jwt.MapClaims) (string, error) {
	j.mu.RLock()
	key := j.active
	j.mu.RUnlock()

	method := jwt.GetSigningMethod(key.Alg)
	if method == nil {
		return "", fmt.Errorf("unsupported signing algorithm %q for kid %s", key.Alg, key.KID)
	}
	token := jwt.NewWithClaims(method, claims)
	token.Header["kid"] = key.KID
	return token.SignedString(key.Private)
}

// keyForKID returns the public key and its algorithm matching kid, or
// (nil, "") when unknown.
func (j *JWTService) keyForKID(kid string) (crypto.PublicKey, string) {
	j.mu.RLock()
	defer j.mu.RUnlock()
	if j.active != nil && j.active.KID == kid {
		return j.active.Private.Public(), j.active.Alg
	}
	if j.previous != nil && j.previous.KID == kid {
		return j.previous.Private.Public(), j.previous.Alg
	}
	return nil, ""
}

// Verify validates a JWT (RS256 or ES256) and returns its claims. The
// verification key is resolved by the token's kid header; tokens signed
// with an unknown kid are rejected. The token's actual algorithm is
// cross-checked against the algorithm that specific kid was issued under —
// never trusted from the header alone (alg-confusion guard). It also
// rejects tokens whose aud claim does not contain j.selfAudience, and whose
// iss claim does not match this service's issuer.
func (j *JWTService) Verify(tokenStr string) (jwt.MapClaims, error) {
	opts := []jwt.ParserOption{jwt.WithAudience(j.selfAudience)}
	if j.issuer != "" {
		opts = append(opts, jwt.WithIssuer(j.issuer))
	}
	parsed, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		kid, _ := token.Header["kid"].(string)
		pub, alg := j.keyForKID(kid)
		if pub == nil {
			return nil, fmt.Errorf("unknown kid %q", kid)
		}
		if token.Method.Alg() != alg {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return pub, nil
	}, append(opts, jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg(), jwt.SigningMethodES256.Alg()}))...)
	if err != nil {
		return nil, fmt.Errorf("verifying token: %w", err)
	}

	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok || !parsed.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}
	// SEC-011: this service only ever validates bearer access tokens. Reject
	// anything that isn't explicitly an access token (e.g. an id_token replayed
	// as a bearer credential). id_tokens are verified by resource servers.
	if tu, ok := claims["token_use"].(string); !ok || tu != "access" {
		return nil, fmt.Errorf("token_use claim missing or not \"access\"")
	}
	return claims, nil
}

// jwkFor renders one public key as a JWK map. Supports RSA and EC (P-256)
// public keys — the only two types keystore.Key.Private can ever hold
// (enforced by keystore.Generate/ParseKey), so an unreachable third type here
// indicates a broken invariant elsewhere, not a runtime input to validate.
func jwkFor(pub crypto.PublicKey, alg, kid string) map[string]any {
	switch key := pub.(type) {
	case *rsa.PublicKey:
		n := base64.RawURLEncoding.EncodeToString(key.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())
		return map[string]any{
			"kty": "RSA",
			"use": "sig",
			"alg": alg,
			"kid": kid,
			"n":   n,
			"e":   e,
		}
	case *ecdsa.PublicKey:
		size := (key.Curve.Params().BitSize + 7) / 8
		x := make([]byte, size)
		y := make([]byte, size)
		key.X.FillBytes(x)
		key.Y.FillBytes(y)
		return map[string]any{
			"kty": "EC",
			"use": "sig",
			"alg": alg,
			"kid": kid,
			"crv": "P-256",
			"x":   base64.RawURLEncoding.EncodeToString(x),
			"y":   base64.RawURLEncoding.EncodeToString(y),
		}
	default:
		panic(fmt.Sprintf("jwkFor: unsupported public key type %T for kid %s", pub, kid))
	}
}

// PublicKeyJWKs returns the JWKS key set: active first, previous second (when
// present). The previous key stays served for a full rotation period so
// tokens signed just before a rotation always verify downstream.
func (j *JWTService) PublicKeyJWKs() []map[string]any {
	j.mu.RLock()
	defer j.mu.RUnlock()
	keys := []map[string]any{jwkFor(j.active.Private.Public(), j.active.Alg, j.active.KID)}
	if j.previous != nil {
		keys = append(keys, jwkFor(j.previous.Private.Public(), j.previous.Alg, j.previous.KID))
	}
	return keys
}

// KID returns the current active key ID.
func (j *JWTService) KID() string {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.active.KID
}

// AccessTokenTTLSeconds returns the configured access-token lifetime in
// seconds, for advertising in the token endpoint's expires_in (BUG-027).
func (j *JWTService) AccessTokenTTLSeconds() int {
	return int(j.accessTokenTTL.Seconds())
}
