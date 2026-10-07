package deletion

import (
	"context"
	"fmt"
	"time"

	"gopkg.aoctech.app/account/api/internal/domain/apikey"
	commoncache "gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/erasure"
	"gopkg.aoctech.app/api-commons/jwtverify"
)

type UserBlocker interface {
	MarkPendingDeletion(ctx context.Context, userID, requestID string) error
	ClearPendingDeletion(ctx context.Context, userID, requestID string) error
}

type SessionRevoker interface {
	RevokeAll(ctx context.Context, userID, exceptSessionID string) error
}

type APIKeys interface {
	List(ctx context.Context, userID string) ([]*apikey.APIKey, error)
	Revoke(ctx context.Context, userID, keyID string) error
}

// TokenRevoker cuts live access tokens. There is no undo on purpose: tokens
// issued after a cancel are newer than the cutoff, and removing the entry
// early could lift the revocation of a newer request.
type TokenRevoker interface {
	Revoke(ctx context.Context, sub string, cutoff time.Time) error
}

type Publisher interface {
	Publish(ctx context.Context, m erasure.Message) error
}

// AccountLocker is the production Locker: it blocks sign-in, kills every
// credential, cuts live access tokens and tells the participants.
type AccountLocker struct {
	users    UserBlocker
	sessions SessionRevoker
	keys     APIKeys
	tokens   TokenRevoker
	pub      Publisher
	services []string
}

func NewAccountLocker(users UserBlocker, sessions SessionRevoker, keys APIKeys, tokens TokenRevoker, pub Publisher, services []string) *AccountLocker {
	return &AccountLocker{users: users, sessions: sessions, keys: keys, tokens: tokens, pub: pub, services: services}
}

func (l *AccountLocker) Lock(ctx context.Context, r *Request) error {
	if err := l.users.MarkPendingDeletion(ctx, r.UserID, r.ID); err != nil {
		return fmt.Errorf("blocking sign-in: %w", err)
	}
	if err := l.sessions.RevokeAll(ctx, r.UserID, ""); err != nil {
		return fmt.Errorf("revoking sessions: %w", err)
	}
	keys, err := l.keys.List(ctx, r.UserID)
	if err != nil {
		return fmt.Errorf("listing api keys: %w", err)
	}
	for _, k := range keys {
		if err := l.keys.Revoke(ctx, r.UserID, k.ID()); err != nil {
			return fmt.Errorf("revoking api key: %w", err)
		}
	}
	if err := l.tokens.Revoke(ctx, r.UserID, time.Now()); err != nil {
		return fmt.Errorf("revoking access tokens: %w", err)
	}
	return l.publish(ctx, r, erasure.TypeLocked, parseTime(r.ConfirmedAt))
}

// Unlock re-enables sign-in. Revoked sessions, API keys and the token
// revocation entry stay as they are: the user signs in again, and the entry
// expires on its own (jwtverify.RevocationTTL).
func (l *AccountLocker) Unlock(ctx context.Context, r *Request) error {
	if err := l.users.ClearPendingDeletion(ctx, r.UserID, r.ID); err != nil {
		return fmt.Errorf("unblocking sign-in: %w", err)
	}
	return l.publish(ctx, r, erasure.TypeUnlocked, parseTime(r.CancelledAt))
}

func (l *AccountLocker) Erase(ctx context.Context, r *Request) error {
	return l.publish(ctx, r, erasure.TypeErase, parseTime(r.LockedAt))
}

// publish uses the request's own timestamps as issued_at, so a retried
// publish is a duplicate the participants ignore, never a newer event.
func (l *AccountLocker) publish(ctx context.Context, r *Request, typ erasure.Type, at time.Time) error {
	if len(l.services) == 0 {
		return nil // no participant subscribed yet (Phase 1)
	}
	return l.pub.Publish(ctx, erasure.Message{
		Version: erasure.Version, Type: typ, RequestID: r.ID, Sub: r.UserID,
		Scope: erasure.ScopeAccount, Services: l.services, Attempt: 1, IssuedAt: at,
	})
}

// JWTRevoker writes the shared revocation list read by every service.
type JWTRevoker struct{ c commoncache.Backend }

func NewJWTRevoker(c commoncache.Backend) *JWTRevoker { return &JWTRevoker{c: c} }

func (r *JWTRevoker) Revoke(ctx context.Context, sub string, cutoff time.Time) error {
	return jwtverify.Revoke(ctx, r.c, sub, cutoff, jwtverify.RevocationTTL)
}
