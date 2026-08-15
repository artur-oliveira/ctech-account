package config

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"log"
	"strings"
	"testing"

	"gopkg.aoctech.app/account/api/internal/keystore"
)

func setWebAuthnTestEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"RSA_PRIVATE_KEY", "EC_PRIVATE_KEY", "PUBLIC_KEY_KID", "WEBAUTHN_RPID", "ALLOWED_ORIGINS",
		"APP_URL", "BASE_URL", "TABLE_PREFIX", "ENVIRONMENT",
	} {
		t.Setenv(key, "")
	}
}

func TestLoadWebAuthnDefaultsToBrowserFacingAppURL(t *testing.T) {
	setWebAuthnTestEnv(t)
	t.Setenv("BASE_URL", "https://accountsapi.aoctech.app")
	t.Setenv("APP_URL", "https://accounts.aoctech.app")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RPID != "accounts.aoctech.app" {
		t.Fatalf("RPID = %q, want accounts.aoctech.app", cfg.RPID)
	}
	if len(cfg.RPOrigins) != 1 || cfg.RPOrigins[0] != "https://accounts.aoctech.app" {
		t.Fatalf("RPOrigins = %v, want only the SPA origin", cfg.RPOrigins)
	}
	if cfg.Audience != "https://accounts.aoctech.app" {
		t.Fatalf("Audience = %q, want Account public resource identifier", cfg.Audience)
	}
}

func TestLoadWebAuthnExplicitRegistrableDomainAllowsSPA(t *testing.T) {
	setWebAuthnTestEnv(t)
	t.Setenv("BASE_URL", "https://accountsapi.aoctech.app")
	t.Setenv("APP_URL", "https://accounts.aoctech.app")
	t.Setenv("WEBAUTHN_RPID", "aoctech.app")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RPID != "aoctech.app" || !isRegistrableMatch(cfg.RPID, cfg.RPOrigins) {
		t.Fatalf("RPID %q should match %v", cfg.RPID, cfg.RPOrigins)
	}
}

func TestLoadWebAuthnWarnsForMismatchedRPID(t *testing.T) {
	setWebAuthnTestEnv(t)
	t.Setenv("BASE_URL", "https://accountsapi.aoctech.app")
	t.Setenv("APP_URL", "https://accounts.aoctech.app")
	t.Setenv("WEBAUTHN_RPID", "example.net")

	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })

	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "WebAuthn ceremonies will fail") {
		t.Fatalf("expected WebAuthn mismatch warning, got %q", logs.String())
	}
}

func TestLoadSigningKeyFromRSAPrivateKeyEnv(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)}
	t.Setenv("RSA_PRIVATE_KEY", string(pem.EncodeToMemory(block)))
	t.Setenv("EC_PRIVATE_KEY", "")

	signer, alg, kid, err := loadSigningKey()
	if err != nil {
		t.Fatalf("loadSigningKey: %v", err)
	}
	if alg != keystore.AlgRS256 {
		t.Fatalf("alg = %q, want %q", alg, keystore.AlgRS256)
	}
	if _, ok := signer.Public().(*rsa.PublicKey); !ok {
		t.Fatalf("signer.Public() is %T, want *rsa.PublicKey", signer.Public())
	}
	if kid == "" {
		t.Fatal("kid must be derived when PUBLIC_KEY_KID is unset")
	}
}

func TestLoadSigningKeyFromECPrivateKeyEnv(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating EC key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("marshaling EC key: %v", err)
	}
	block := &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	t.Setenv("RSA_PRIVATE_KEY", "")
	t.Setenv("EC_PRIVATE_KEY", string(pem.EncodeToMemory(block)))

	signer, alg, _, err := loadSigningKey()
	if err != nil {
		t.Fatalf("loadSigningKey: %v", err)
	}
	if alg != keystore.AlgES256 {
		t.Fatalf("alg = %q, want %q", alg, keystore.AlgES256)
	}
	if _, ok := signer.Public().(*ecdsa.PublicKey); !ok {
		t.Fatalf("signer.Public() is %T, want *ecdsa.PublicKey", signer.Public())
	}
}

func TestLoadSigningKeyRejectsBothEnvVarsSet(t *testing.T) {
	t.Setenv("RSA_PRIVATE_KEY", "dummy")
	t.Setenv("EC_PRIVATE_KEY", "dummy")

	if _, _, _, err := loadSigningKey(); err == nil {
		t.Fatal("loadSigningKey accepted both RSA_PRIVATE_KEY and EC_PRIVATE_KEY set simultaneously")
	}
}

func TestLoadSigningKeyReturnsNilWhenUnset(t *testing.T) {
	t.Setenv("RSA_PRIVATE_KEY", "")
	t.Setenv("EC_PRIVATE_KEY", "")

	signer, alg, kid, err := loadSigningKey()
	if err != nil {
		t.Fatalf("loadSigningKey: %v", err)
	}
	if signer != nil || alg != "" || kid != "" {
		t.Fatalf("expected all-zero return when neither env var is set, got signer=%v alg=%q kid=%q", signer, alg, kid)
	}
}

func TestRegistrableMatchRejectsPublicSuffix(t *testing.T) {
	if isRegistrableMatch("com", []string{"https://accounts.example.com"}) {
		t.Fatal("public suffix must not be accepted as an RP ID")
	}
	if isRegistrableMatch("co.uk", []string{"https://co.uk"}) {
		t.Fatal("exact public suffix must not be accepted as an RP ID")
	}
}
