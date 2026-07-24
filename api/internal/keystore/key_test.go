package keystore

import (
	"testing"
	"time"
)

func TestGenerateRoundTripsThroughJSON(t *testing.T) {
	k, err := Generate(time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(k.KID) != 64 {
		t.Errorf("kid length = %d, want 64 (full SHA-256 hex)", len(k.KID))
	}
	j, err := k.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseKey(j)
	if err != nil {
		t.Fatal(err)
	}
	if back.KID != k.KID || !back.CreatedAt.Equal(k.CreatedAt) {
		t.Errorf("round trip mismatch: %+v vs %+v", back, k)
	}
	if back.Private.N.Cmp(k.Private.N) != 0 {
		t.Error("private key mismatch after round trip")
	}
}

func TestDeriveKIDIsStable(t *testing.T) {
	k, err := Generate(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	kid1, _ := DeriveKID(&k.Private.PublicKey)
	kid2, _ := DeriveKID(&k.Private.PublicKey)
	if kid1 != kid2 || kid1 != k.KID {
		t.Errorf("kid unstable: %s %s %s", kid1, kid2, k.KID)
	}
}

// SEC-044: KID must be derived from the full SHA-256 (≥128 bits), not the old
// 64-bit truncation.
func TestDeriveKIDFullSHA256(t *testing.T) {
	k, err := Generate(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	kid, err := DeriveKID(&k.Private.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(kid) < 32 {
		t.Errorf("kid too short: %q (len %d), want ≥32 hex chars (128 bits)", kid, len(kid))
	}
	// A different key must derive a different KID.
	other, _ := Generate(time.Now())
	otherKID, _ := DeriveKID(&other.Private.PublicKey)
	if otherKID == kid {
		t.Error("distinct keys produced identical KIDs")
	}
}

func TestParseKeyRejectsGarbage(t *testing.T) {
	if _, err := ParseKey(KeyJSON{KID: "x", PEM: "not-pem", CreatedAt: "2026-07-10T00:00:00Z"}); err == nil {
		t.Error("expected error for invalid PEM")
	}
	k, _ := Generate(time.Now())
	j, _ := k.ToJSON()
	j.CreatedAt = "not-a-date"
	if _, err := ParseKey(j); err == nil {
		t.Error("expected error for invalid created_at")
	}
}
