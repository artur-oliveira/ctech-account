package deletion

import (
	"context"
	"fmt"
	"slices"

	"gopkg.aoctech.app/account/api/internal/domain/audit"
	"gopkg.aoctech.app/account/api/internal/domain/oauth/consent"
	"gopkg.aoctech.app/account/api/internal/scopes"
	"gopkg.aoctech.app/api-commons/observability"
)

// ConsentRevoker removes the grant a person gave an OAuth client.
type ConsentRevoker interface {
	Revoke(ctx context.Context, userID, clientID string) error
	List(ctx context.Context, userID string) ([]*consent.Grant, error)
}

// UnlinkRecorder remembers on the user which products they left, so the next
// sign-in to one of them can tell it the person is back (spec § 7).
type UnlinkRecorder interface {
	AddUnlinkedService(ctx context.Context, userID, service string) error
}

// UnlinkAudit records the end of an unlink. audit.Service satisfies it.
type UnlinkAudit interface {
	Record(ctx context.Context, e audit.Entry)
}

// serviceUnlinker is the account side of a finished service unlink (spec § 6).
// Nothing of the account is erased: the grants for the product's login clients
// go, and the product is remembered on the user.
type serviceUnlinker struct {
	consents ConsentRevoker
	tokens   ClientTokenRevoker
	keys     APIKeys
	users    UnlinkRecorder
	profiles Users
	audit    UnlinkAudit
	mail     Mailer
	clients  func(service string) []string
}

func NewServiceUnlinker(consents ConsentRevoker, tokens ClientTokenRevoker, keys APIKeys, users UnlinkRecorder, profiles Users, a UnlinkAudit, mail Mailer, clients func(service string) []string) ServiceUnlinker {
	return &serviceUnlinker{consents: consents, tokens: tokens, keys: keys, users: users, profiles: profiles, audit: a, mail: mail, clients: clients}
}

// Finish is idempotent up to the audit row, which is written after every step
// that can fail, so a retry never writes it twice.
func (u *serviceUnlinker) Finish(ctx context.Context, r *Request) error {
	// The product's login clients, and any other client the person authorized
	// for one of the product's scopes: otherwise coming back would silently
	// restore that delegation.
	clients := u.clients(r.Service)
	grants, err := u.consents.List(ctx, r.UserID)
	if err != nil {
		return fmt.Errorf("listing consents: %w", err)
	}
	for _, g := range grants {
		if slices.Contains(clients, g.ClientID()) ||
			slices.ContainsFunc(g.Scopes, func(sc string) bool { return scopes.BelongsToService(sc, r.Service) }) {
			if err := u.consents.Revoke(ctx, r.UserID, g.ClientID()); err != nil {
				return fmt.Errorf("revoking consent for %s: %w", g.ClientID(), err)
			}
		}
	}
	// Refresh tokens issued during the grace period (the account kept issuing
	// them) would outlive the consent: sweep them again now.
	if err := u.tokens.RevokeClients(ctx, r.UserID, clients, r.Service); err != nil {
		return fmt.Errorf("revoking product refresh tokens: %w", err)
	}
	// Same for API keys created during the grace period.
	if err := revokeServiceKeys(ctx, u.keys, r.UserID, r.Service); err != nil {
		return err
	}
	if err := u.users.AddUnlinkedService(ctx, r.UserID, r.Service); err != nil {
		return fmt.Errorf("recording unlinked service: %w", err)
	}
	u.audit.Record(ctx, audit.Entry{
		UserID: r.UserID, Type: audit.EventDeletionServiceUnlinked,
		Metadata: map[string]string{"service": r.Service, "request_id": r.ID},
	})
	if usr, err := u.profiles.GetByID(ctx, r.UserID); err == nil {
		if mErr := u.mail.SendServiceUnlinkedEmail(ctx, usr.Email, usr.FirstName, r.Service); mErr != nil {
			observability.Error(ctx, "deletion: service unlinked e-mail failed", mErr, "request_id", r.ID)
		}
	}
	return nil
}
