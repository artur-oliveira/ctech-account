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

func TestSealerMAC(t *testing.T) {
	a := &Sealer{key: []byte("0123456789abcdef0123456789abcdef")}
	b := &Sealer{key: []byte("fedcba9876543210fedcba9876543210")}
	m := a.MAC("cpf-hmac", "12345678909")
	if len(m) != 64 || m != a.MAC("cpf-hmac", "12345678909") {
		t.Fatalf("MAC must be deterministic hex-sha256, got %q", m)
	}
	if m == a.MAC("other-label", "12345678909") {
		t.Fatal("labels must separate keys")
	}
	if m == b.MAC("cpf-hmac", "12345678909") {
		t.Fatal("different sealing keys must give different MACs")
	}
}
