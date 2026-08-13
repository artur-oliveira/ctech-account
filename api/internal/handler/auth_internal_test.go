package handler

import (
	"testing"

	"gopkg.aoctech.app/account/api/internal/domain/session"
)

func TestMFASessionAMRPreservesWebAuthnPrimary(t *testing.T) {
	got := mfaSessionAMR(mfaTokenPayload{PrimaryAMR: session.AMRWebAuthn})
	if len(got) != 2 || got[0] != session.AMRWebAuthn || got[1] != session.AMRTOTP {
		t.Fatalf("AMR = %v, want [webauthn otp]", got)
	}
}

func TestMFASessionAMRPreservesPasswordPrimary(t *testing.T) {
	got := mfaSessionAMR(mfaTokenPayload{PrimaryAMR: session.AMRPassword})
	if len(got) != 2 || got[0] != session.AMRPassword || got[1] != session.AMRTOTP {
		t.Fatalf("AMR = %v, want [pwd otp]", got)
	}
}

func TestMFASessionAMRHandlesLegacyPasskeyToken(t *testing.T) {
	got := mfaSessionAMR(mfaTokenPayload{DeviceName: "Passkey"})
	if len(got) != 2 || got[0] != session.AMRWebAuthn || got[1] != session.AMRTOTP {
		t.Fatalf("legacy AMR = %v, want [webauthn otp]", got)
	}
}
