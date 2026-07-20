package crypto

import "testing"

func TestSealOpenRoundTrip(t *testing.T) {
	ct, err := Seal("my-secret-totp-base32-value")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if ct == "my-secret-totp-base32-value" {
		t.Fatal("ciphertext must not equal plaintext")
	}
	pt, err := Open(ct)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if pt != "my-secret-totp-base32-value" {
		t.Fatalf("round-trip mismatch: got %q", pt)
	}
}

func TestSealOpenEmpty(t *testing.T) {
	ct, err := Seal("")
	if err != nil || ct != "" {
		t.Fatalf("seal empty: ct=%q err=%v", ct, err)
	}
	pt, err := Open("")
	if err != nil || pt != "" {
		t.Fatalf("open empty: pt=%q err=%v", pt, err)
	}
}

func TestSealUsesEnvKey(t *testing.T) {
	t.Setenv("SECRET_ENC_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	ct, err := Seal("plaintext")
	if err != nil {
		t.Fatalf("seal with env key: %v", err)
	}
	pt, err := Open(ct)
	if err != nil {
		t.Fatalf("open with env key: %v", err)
	}
	if pt != "plaintext" {
		t.Fatalf("env-key round-trip mismatch: got %q", pt)
	}
}
