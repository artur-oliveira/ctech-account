package crypto

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"gopkg.aoctech.app/account/api/internal/config"
	"gopkg.aoctech.app/account/api/internal/keystore"
)

const testIssuer = "http://localhost"

func newTestJWTService(t *testing.T) *JWTService {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		RSAPrivateKey: key,
		PublicKeyKID:  "test-kid",
		Audience:      testIssuer,
		BaseURL:       testIssuer,
	}
	svc, err := NewJWTService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestAccessTokenCarriesStepUpClaims(t *testing.T) {
	svc := newTestJWTService(t)
	tok, err := svc.SignAccessToken("u1", "s1", "web", []string{"openid"}, testIssuer, []string{testIssuer}, 1000, 2000, []string{"pwd", "otp"}, "")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := svc.Verify(tok)
	if err != nil {
		t.Fatal(err)
	}
	if claims["auth_time"].(float64) != 1000 || claims["last_mfa_at"].(float64) != 2000 {
		t.Errorf("claims: %v", claims)
	}
	amr, ok := claims["amr"].([]any)
	if !ok || len(amr) != 2 || amr[1].(string) != "otp" {
		t.Errorf("amr: %v", claims["amr"])
	}
}

func TestZeroStepUpClaimsAreOmitted(t *testing.T) {
	svc := newTestJWTService(t)
	tok, err := svc.SignAccessToken("u1", "s1", "web", nil, testIssuer, []string{testIssuer}, 0, 0, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := svc.Verify(tok)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"auth_time", "last_mfa_at", "amr"} {
		if _, present := claims[k]; present {
			t.Errorf("claim %s should be omitted when zero", k)
		}
	}
}

func newJWTServiceWithKeys(t *testing.T, active, previous *keystore.Key) *JWTService {
	t.Helper()
	cfg := &config.Config{Audience: testIssuer, BaseURL: testIssuer}
	svc, err := NewJWTServiceWithKeys(cfg, active, previous)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func parseHeader(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	var header map[string]any
	if err := json.Unmarshal(raw, &header); err != nil {
		t.Fatal(err)
	}
	return header
}

func TestVerifyAcceptsPreviousKeyAfterReload(t *testing.T) {
	oldKey, _ := keystore.Generate(time.Now().Add(-91 * 24 * time.Hour))
	newKey, _ := keystore.Generate(time.Now())

	svc := newJWTServiceWithKeys(t, oldKey, nil)
	tok, err := svc.SignAccessToken("u1", "s1", "web", nil, testIssuer, []string{testIssuer}, 0, 0, nil, "")
	if err != nil {
		t.Fatal(err)
	}

	svc.Reload(newKey, oldKey) // rotation happened
	if _, err := svc.Verify(tok); err != nil {
		t.Errorf("token signed by previous key must still verify: %v", err)
	}

	tok2, _ := svc.SignAccessToken("u1", "s1", "web", nil, testIssuer, []string{testIssuer}, 0, 0, nil, "")
	if header := parseHeader(t, tok2); header["kid"] != newKey.KID {
		t.Errorf("new tokens must be signed with active kid, got %v", header["kid"])
	}
}

func TestVerifyRejectsUnknownKID(t *testing.T) {
	a, _ := keystore.Generate(time.Now())
	b, _ := keystore.Generate(time.Now())
	svcA := newJWTServiceWithKeys(t, a, nil)
	svcB := newJWTServiceWithKeys(t, b, nil)
	tok, _ := svcA.SignAccessToken("u1", "s1", "web", nil, testIssuer, []string{testIssuer}, 0, 0, nil, "")
	if _, err := svcB.Verify(tok); err == nil {
		t.Error("token with unknown kid must be rejected")
	}
}

func TestJWKSListsActiveThenPrevious(t *testing.T) {
	a, _ := keystore.Generate(time.Now())
	p, _ := keystore.Generate(time.Now().Add(-time.Hour))
	svc := newJWTServiceWithKeys(t, a, p)
	jwks := svc.PublicKeyJWKs()
	if len(jwks) != 2 || jwks[0]["kid"] != a.KID || jwks[1]["kid"] != p.KID {
		t.Errorf("jwks: %v", jwks)
	}
	single := newJWTServiceWithKeys(t, a, nil)
	if got := single.PublicKeyJWKs(); len(got) != 1 {
		t.Errorf("expected 1 jwk without previous, got %d", len(got))
	}
}

func TestSignAccessTokenIncludesKYCLevel(t *testing.T) {
	svc := newTestJWTService(t)
	token, err := svc.SignAccessToken("u1", "s1", "c1", []string{"openid", "kyc"}, testIssuer, []string{testIssuer}, 0, 0, nil, "verified")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := svc.Verify(token)
	if err != nil {
		t.Fatal(err)
	}
	if claims["kyc_level"] != "verified" {
		t.Fatalf("kyc_level = %v", claims["kyc_level"])
	}
}

func TestSignAccessTokenOmitsEmptyKYCLevel(t *testing.T) {
	svc := newTestJWTService(t)
	token, _ := svc.SignAccessToken("u1", "s1", "c1", []string{"openid"}, testIssuer, []string{testIssuer}, 0, 0, nil, "")
	claims, _ := svc.Verify(token)
	if _, present := claims["kyc_level"]; present {
		t.Fatal("empty kyc_level must be omitted")
	}
}

func TestSignIDTokenIncludesKYCLevel(t *testing.T) {
	svc := newTestJWTService(t)
	token, err := svc.SignIDToken("u1", "a@b.c", "Fulano", "Fulano", "Fulano", "", true, "c1", "", testIssuer, "basic")
	if err != nil {
		t.Fatal(err)
	}
	// id_token aud is the client, not this IdP — decode the payload directly
	// instead of Verify (which enforces the self audience).
	parts := strings.Split(token, ".")
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatal(err)
	}
	if claims["kyc_level"] != "basic" {
		t.Fatalf("kyc_level = %v", claims["kyc_level"])
	}
}

// SEC-011: a token shaped like an id_token (token_use != "access") must be
// rejected by Verify even when its aud/iss are otherwise valid.
func TestVerifyRejectsIDTokenShapedToken(t *testing.T) {
	svc := newTestJWTService(t)
	// id_token with aud/iss equal to the IdP's own audience/issuer so the
	// aud/iss validators pass and the token_use guard is the one that bites.
	idTok, err := svc.SignIDToken("u1", "a@b.c", "F", "F", "F", "", true, testIssuer, "", testIssuer, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Verify(idTok); err == nil {
		t.Error("id_token must be rejected by Verify (token_use != access)")
	}

	// And a token with no token_use claim at all must also be rejected.
	noUse, err := svc.sign(jwt.MapClaims{
		"sub":  "u1",
		"iss":  testIssuer,
		"aud":  []string{testIssuer},
		"iat":  time.Now().UTC().Unix(),
		"exp":  time.Now().UTC().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Verify(noUse); err == nil {
		t.Error("token without token_use must be rejected by Verify")
	}
}

// SEC-011 / BUG-027: an access token carries token_use:"access" and verifies.
func TestSignAccessTokenVerifiesWithTokenUse(t *testing.T) {
	svc := newTestJWTService(t)
	tok, err := svc.SignAccessToken("u1", "s1", "web", nil, testIssuer, []string{testIssuer}, 0, 0, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := svc.Verify(tok)
	if err != nil {
		t.Fatalf("valid access token must verify: %v", err)
	}
	if claims["token_use"] != "access" {
		t.Errorf("expected token_use=access, got %v", claims["token_use"])
	}
}

// BUG-027: signed exp and advertised TTL must agree, including a custom TTL.
func TestAccessTokenTTLWiredThrough(t *testing.T) {
	custom := 30 * time.Minute
	base := newTestJWTService(t)
	key := &keystore.Key{KID: "test-kid", Private: base.active.Private}
	cfg := &config.Config{
		RSAPrivateKey: key.Private,
		PublicKeyKID:  "test-kid",
		Audience:      testIssuer,
		BaseURL:       testIssuer,
		AccessTokenTTL: custom,
	}
	svc, err := NewJWTServiceWithKeys(cfg, key, nil)
	if err != nil {
		t.Fatal(err)
	}

	if got := svc.AccessTokenTTLSeconds(); got != 1800 {
		t.Errorf("AccessTokenTTLSeconds = %d, want 1800", got)
	}

	tok, err := svc.SignAccessToken("u1", "s1", "web", nil, testIssuer, []string{testIssuer}, 0, 0, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ".")
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatal(err)
	}
	exp := int64(claims["exp"].(float64))
	iat := int64(claims["iat"].(float64))
	if got := exp - iat; got != 1800 {
		t.Errorf("signed exp-iat = %d, want 1800", got)
	}
}

// BUG-027: zero AccessTokenTTL defaults to 15 minutes.
func TestAccessTokenTTLDefaultsToFifteenMinutes(t *testing.T) {
	svc := newTestJWTService(t) // cfg has no AccessTokenTTL → default
	if got := svc.AccessTokenTTLSeconds(); got != 900 {
		t.Errorf("default AccessTokenTTLSeconds = %d, want 900", got)
	}
}
