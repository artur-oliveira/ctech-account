package keystore

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"
)

func TestGenerateRoundTripsThroughJSON(t *testing.T) {
	for _, alg := range []string{AlgRS256, AlgES256} {
		t.Run(alg, func(t *testing.T) {
			k, err := Generate(time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC), alg)
			if err != nil {
				t.Fatal(err)
			}
			if len(k.KID) != 64 {
				t.Errorf("kid length = %d, want 64 (full SHA-256 hex)", len(k.KID))
			}
			if k.Alg != alg {
				t.Errorf("Alg = %q, want %q", k.Alg, alg)
			}
			j, err := k.ToJSON()
			if err != nil {
				t.Fatal(err)
			}
			back, err := ParseKey(j)
			if err != nil {
				t.Fatal(err)
			}
			if back.KID != k.KID || !back.CreatedAt.Equal(k.CreatedAt) || back.Alg != k.Alg {
				t.Errorf("round trip mismatch: %+v vs %+v", back, k)
			}
			switch alg {
			case AlgRS256:
				orig := k.Private.Public().(*rsa.PublicKey)
				got := back.Private.Public().(*rsa.PublicKey)
				if orig.N.Cmp(got.N) != 0 {
					t.Error("RSA private key mismatch after round trip")
				}
			case AlgES256:
				orig := k.Private.Public().(*ecdsa.PublicKey)
				got := back.Private.Public().(*ecdsa.PublicKey)
				if orig.X.Cmp(got.X) != 0 || orig.Y.Cmp(got.Y) != 0 {
					t.Error("EC private key mismatch after round trip")
				}
			}
		})
	}
}

func TestGenerateES256ProducesECKey(t *testing.T) {
	key, err := Generate(time.Now(), AlgES256)
	if err != nil {
		t.Fatalf("Generate(ES256): %v", err)
	}
	if key.Alg != AlgES256 {
		t.Fatalf("Alg = %q, want %q", key.Alg, AlgES256)
	}
	if _, ok := key.Private.Public().(*ecdsa.PublicKey); !ok {
		t.Fatalf("Private.Public() is %T, want *ecdsa.PublicKey", key.Private.Public())
	}
}

func TestDeriveKIDIsStable(t *testing.T) {
	k, err := Generate(time.Now(), AlgRS256)
	if err != nil {
		t.Fatal(err)
	}
	kid1, _ := DeriveKID(k.Private.Public())
	kid2, _ := DeriveKID(k.Private.Public())
	if kid1 != kid2 || kid1 != k.KID {
		t.Errorf("kid unstable: %s %s %s", kid1, kid2, k.KID)
	}
}

// SEC-044: KID must be derived from the full SHA-256 (≥128 bits), not the old
// 64-bit truncation.
func TestDeriveKIDFullSHA256(t *testing.T) {
	k, err := Generate(time.Now(), AlgRS256)
	if err != nil {
		t.Fatal(err)
	}
	kid, err := DeriveKID(k.Private.Public())
	if err != nil {
		t.Fatal(err)
	}
	if len(kid) < 32 {
		t.Errorf("kid too short: %q (len %d), want ≥32 hex chars (128 bits)", kid, len(kid))
	}
	// A different key must derive a different KID.
	other, _ := Generate(time.Now(), AlgRS256)
	otherKID, _ := DeriveKID(other.Private.Public())
	if otherKID == kid {
		t.Error("distinct keys produced identical KIDs")
	}
}

func TestParseKeyRejectsGarbage(t *testing.T) {
	if _, err := ParseKey(KeyJSON{KID: "x", PEM: "not-pem", CreatedAt: "2026-07-10T00:00:00Z"}); err == nil {
		t.Error("expected error for invalid PEM")
	}
	k, _ := Generate(time.Now(), AlgRS256)
	j, _ := k.ToJSON()
	j.CreatedAt = "not-a-date"
	if _, err := ParseKey(j); err == nil {
		t.Error("expected error for invalid created_at")
	}
}

func TestParseKeyAcceptsLegacyPKCS1RSAWireFormat(t *testing.T) {
	// Regression: keys already stored in SSM before this migration are PKCS1
	// RSA with no "alg" field. ParseKey must keep reading them unchanged.
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)}
	legacyJSON := KeyJSON{
		KID:       "legacy-kid",
		PEM:       string(pem.EncodeToMemory(block)),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	key, err := ParseKey(legacyJSON)
	if err != nil {
		t.Fatalf("ParseKey(legacy PKCS1): %v", err)
	}
	if key.Alg != AlgRS256 {
		t.Fatalf("Alg = %q, want %q", key.Alg, AlgRS256)
	}
	if _, ok := key.Private.Public().(*rsa.PublicKey); !ok {
		t.Fatalf("Private.Public() is %T, want *rsa.PublicKey", key.Private.Public())
	}
}

func TestParseKeyRejectsAlgMismatch(t *testing.T) {
	key, err := Generate(time.Now(), AlgES256)
	if err != nil {
		t.Fatalf("Generate(ES256): %v", err)
	}
	j, err := key.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	j.Alg = AlgRS256 // corrupt: claims RSA but the PEM is an EC key

	if _, err := ParseKey(j); err == nil {
		t.Fatal("ParseKey accepted a KeyJSON whose stored Alg does not match its key material")
	}
}
