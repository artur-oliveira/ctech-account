package crypto

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

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
