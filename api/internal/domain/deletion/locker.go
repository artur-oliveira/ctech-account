package deletion

import (
	"context"
	"fmt"
	"strings"
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

// ClientTokenRevoker drops the refresh tokens of the given OAuth clients only
// (service unlink); sessions stay.
type ClientTokenRevoker interface {
	RevokeClients(ctx context.Context, userID string, clientIDs []string) error
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
	// serviceClients maps a product to the OAuth clients people sign in to it
	// with (config.ErasureParticipant.LoginClients), for scope service.
	serviceClients func(service string) []string
	clientTokens   ClientTokenRevoker
}

// WithServiceClients enables scope service on the locker.
func (l *AccountLocker) WithServiceClients(f func(service string) []string, tokens ClientTokenRevoker) *AccountLocker {
	l.serviceClients, l.clientTokens = f, tokens
	return l
}

func NewAccountLocker(users UserBlocker, sessions SessionRevoker, keys APIKeys, tokens TokenRevoker, pub Publisher, services []string) *AccountLocker {
	return &AccountLocker{users: users, sessions: sessions, keys: keys, tokens: tokens, pub: pub, services: services}
}

func (l *AccountLocker) Lock(ctx context.Context, r *Request) error {
	if r.Scope == ScopeService {
		return l.lockService(ctx, r)
	}
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
	return l.publish(ctx, r, erasure.TypeLocked, parseTime(r.ConfirmedAt), nil, nil, nil)
}

// Unlock re-enables sign-in. Revoked sessions, API keys and the token
// revocation entry stay as they are: the user signs in again, and the entry
// expires on its own (jwtverify.RevocationTTL).
func (l *AccountLocker) Unlock(ctx context.Context, r *Request) error {
	if r.Scope == ScopeService {
		// Nothing was marked on the user, so there is nothing to clear: only the
		// product's lock is lifted. Revoked refresh tokens stay revoked; the
		// person signs in to the product again.
		return l.publish(ctx, r, erasure.TypeUnlocked, parseTime(r.CancelledAt), nil, nil, nil)
	}
	if err := l.users.ClearPendingDeletion(ctx, r.UserID, r.ID); err != nil {
		return fmt.Errorf("unblocking sign-in: %w", err)
	}
	return l.publish(ctx, r, erasure.TypeUnlocked, parseTime(r.CancelledAt), nil, nil, nil)
}

func (l *AccountLocker) Erase(ctx context.Context, r *Request, services []string) error {
	return l.publish(ctx, r, erasure.TypeErase, parseTime(r.LockedAt), r.Organizations, r.Workspaces, services)
}

// publish uses the request's own timestamps as issued_at, so a retried
// publish is a duplicate the participants ignore, never a newer event.
func (l *AccountLocker) publish(ctx context.Context, r *Request, typ erasure.Type, at time.Time, organizations []string, workspaces []Workspace, services []string) error {
	scope := erasure.ScopeAccount
	if r.Scope == ScopeService {
		// One product, never a workspace (spec U1): whatever the request holds.
		scope, services, organizations, workspaces = erasure.ScopeService, []string{r.Service}, nil, nil
	}
	if len(services) == 0 {
		services = l.services
	}
	if len(services) == 0 {
		return nil // no participant subscribed yet (Phase 1)
	}
	return l.pub.Publish(ctx, erasure.Message{
		Version: erasure.Version, Type: typ, RequestID: r.ID, Sub: r.UserID,
		Scope: scope, Services: services, Organizations: organizations, Workspaces: toErasureWorkspaces(workspaces),
		Attempt: 1, IssuedAt: at,
	})
}

// JWTRevoker writes the shared revocation list read by every service.
type JWTRevoker struct{ c commoncache.Backend }

func NewJWTRevoker(c commoncache.Backend) *JWTRevoker { return &JWTRevoker{c: c} }

func (r *JWTRevoker) Revoke(ctx context.Context, sub string, cutoff time.Time) error {
	return jwtverify.Revoke(ctx, r.c, sub, cutoff, jwtverify.RevocationTTL)
}

func toErasureWorkspaces(ws []Workspace) []erasure.Workspace {
	if len(ws) == 0 {
		return nil
	}
	out := make([]erasure.Workspace, 0, len(ws))
	for _, w := range ws {
		out = append(out, erasure.Workspace{ID: w.ID, Kind: erasure.WorkspaceKind(w.Kind)})
	}
	return out
}

// lockService cuts one product: its refresh tokens and the API keys holding one
// of its scopes, then the product's own lock. The account stays signed in and
// the jwtverify revocation entry (keyed by sub alone) is never written.
func (l *AccountLocker) lockService(ctx context.Context, r *Request) error {
	if l.serviceClients == nil || l.clientTokens == nil {
		return fmt.Errorf("deletion: scope service is not configured")
	}
	if err := l.clientTokens.RevokeClients(ctx, r.UserID, l.serviceClients(r.Service)); err != nil {
		return fmt.Errorf("revoking product refresh tokens: %w", err)
	}
	keys, err := l.keys.List(ctx, r.UserID)
	if err != nil {
		return fmt.Errorf("listing api keys: %w", err)
	}
	for _, k := range keys {
		if !keyHoldsServiceScope(k.Scopes, r.Service) {
			continue
		}
		if err := l.keys.Revoke(ctx, r.UserID, k.ID()); err != nil {
			return fmt.Errorf("revoking api key: %w", err)
		}
	}
	return l.publish(ctx, r, erasure.TypeLocked, parseTime(r.ConfirmedAt), nil, nil, nil)
}

// keyHoldsServiceScope reports a scope of the product: "<service>:..." or the
// internal form "internal:<service>:...".
func keyHoldsServiceScope(scopes []string, service string) bool {
	for _, sc := range scopes {
		if strings.HasPrefix(sc, service+":") || strings.HasPrefix(sc, "internal:"+service+":") {
			return true
		}
	}
	return false
}
