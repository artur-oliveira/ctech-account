package config

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func setWebAuthnTestEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"RSA_PRIVATE_KEY", "PUBLIC_KEY_KID", "WEBAUTHN_RPID", "ALLOWED_ORIGINS",
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
	if cfg.Audience != "https://accountsapi.aoctech.app" {
		t.Fatalf("Audience = %q, want API origin", cfg.Audience)
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

func TestRegistrableMatchRejectsPublicSuffix(t *testing.T) {
	if isRegistrableMatch("com", []string{"https://accounts.example.com"}) {
		t.Fatal("public suffix must not be accepted as an RP ID")
	}
	if isRegistrableMatch("co.uk", []string{"https://co.uk"}) {
		t.Fatal("exact public suffix must not be accepted as an RP ID")
	}
}
