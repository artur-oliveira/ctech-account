package passkey

import (
	"context"
	"errors"
	"testing"

	"github.com/go-webauthn/webauthn/webauthn"
	"gopkg.aoctech.app/account/api/internal/cache"
)

// consumeSession must reject a session token whose bound user does not match
// the caller (SEC-014), and must fail on a second consume of the same token
// (CON-015, GetDel semantics).
func TestConsumeSessionBindingAndAtomicity(t *testing.T) {
	c := cache.NewInMemory()
	svc := &Service{cache: c}
	ctx := context.Background()

	rawToken, err := svc.storeSession(ctx, "userA", &webauthn.SessionData{})
	if err != nil {
		t.Fatalf("storeSession: %v", err)
	}

	// First consume by the correct user succeeds.
	if _, err := svc.consumeSession(ctx, "userA", rawToken); err != nil {
		t.Fatalf("first consume (matching user): %v", err)
	}

	// Second consume of the same token fails (already consumed).
	if _, err := svc.consumeSession(ctx, "userA", rawToken); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("second consume should be ErrSessionExpired, got %v", err)
	}

	// A session bound to userA must not be consumable by userB.
	rawToken2, err := svc.storeSession(ctx, "userA", &webauthn.SessionData{})
	if err != nil {
		t.Fatalf("storeSession: %v", err)
	}
	if _, err := svc.consumeSession(ctx, "userB", rawToken2); !errors.Is(err, ErrSessionUserMismatch) {
		t.Fatalf("mismatched user should be ErrSessionUserMismatch, got %v", err)
	}
}

// A discoverable-login session (no bound user) is consumable without a user
// assertion, and a user-bound session is consumable by its own user.
func TestConsumeSessionDiscoverableFlow(t *testing.T) {
	c := cache.NewInMemory()
	svc := &Service{cache: c}
	ctx := context.Background()

	// Discoverable session: stored with empty userID, consumed with empty expected.
	disc, err := svc.storeSession(ctx, "", &webauthn.SessionData{})
	if err != nil {
		t.Fatalf("storeSession: %v", err)
	}
	if _, err := svc.consumeSession(ctx, "", disc); err != nil {
		t.Fatalf("discoverable consume: %v", err)
	}

	// User-bound session consumed by the correct user.
	bound, err := svc.storeSession(ctx, "userX", &webauthn.SessionData{})
	if err != nil {
		t.Fatalf("storeSession: %v", err)
	}
	if _, err := svc.consumeSession(ctx, "userX", bound); err != nil {
		t.Fatalf("user-bound consume: %v", err)
	}
}
