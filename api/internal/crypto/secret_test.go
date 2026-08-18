package crypto

import (
	"testing"

	"gopkg.aoctech.app/account/api/internal/config"
)

func TestSealOpenRoundTrip(t *testing.T) {
	s, _ := NewSealer(&config.Config{Environment: "dev"})
	ct, err := s.Seal("my-secret-totp-base32-value")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if ct == "my-secret-totp-base32-value" {
		t.Fatal("ciphertext must not equal plaintext")
	}
	pt, err := s.Unseal(ct)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if pt != "my-secret-totp-base32-value" {
		t.Fatalf("round-trip mismatch: got %q", pt)
	}
}

func TestSealOpenEmpty(t *testing.T) {
	s, _ := NewSealer(&config.Config{Environment: "dev"})

	ct, err := s.Seal("")
	if err != nil || ct != "" {
		t.Fatalf("seal empty: ct=%q err=%v", ct, err)
	}
	pt, err := s.Unseal("")
	if err != nil || pt != "" {
		t.Fatalf("open empty: pt=%q err=%v", pt, err)
	}
}

func TestSealUsesEnvKey(t *testing.T) {
	t.Setenv("SECRET_ENC_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	s, _ := NewSealer(&config.Config{Environment: "dev"})

	ct, err := s.Seal("plaintext")
	if err != nil {
		t.Fatalf("seal with env key: %v", err)
	}
	pt, err := s.Unseal(ct)
	if err != nil {
		t.Fatalf("open with env key: %v", err)
	}
	if pt != "plaintext" {
		t.Fatalf("env-key round-trip mismatch: got %q", pt)
	}
}
