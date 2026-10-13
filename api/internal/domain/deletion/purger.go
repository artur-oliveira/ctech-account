package deletion

import (
	"context"
	"fmt"
	"time"

	"gopkg.aoctech.app/account/api/internal/domain/company"
	"gopkg.aoctech.app/account/api/internal/domain/mfa/passkey"
	"gopkg.aoctech.app/account/api/internal/domain/oauth/consent"
	"gopkg.aoctech.app/account/api/internal/domain/organization"
	"gopkg.aoctech.app/account/api/internal/domain/user"
)

type PurgeMemberships interface {
	ListForUser(ctx context.Context, userID string) ([]*organization.Membership, error)
	Remove(ctx context.Context, orgID, actorUserID, targetUserID string) error
}

type CompanyEdges interface {
	ListForUser(ctx context.Context, userID string) ([]*company.Actor, error)
	RemoveActor(ctx context.Context, orgID, companyID, userID string) error
}

type Consents interface {
	List(ctx context.Context, userID string) ([]*consent.Grant, error)
	Revoke(ctx context.Context, userID, clientID string) error
}

type TOTPRemover interface {
	Remove(ctx context.Context, userID string) error
}

type Passkeys interface {
	List(ctx context.Context, userID string) ([]*passkey.Credential, error)
	Delete(ctx context.Context, userID, credentialSK string) error
}

// UserPurgeFunc turns the user item into a tombstone (user.Service.Purge with
// the CPF keyed hash bound in).
type UserPurgeFunc func(ctx context.Context, userID, requestID string, at time.Time) error

// AccountPurger erases ctech-account's own data for a request, per data
// inventory §3. Each step lists what is left and removes it, so a re-run after
// a partial failure finishes the job. The tombstone is always last: until it
// is written the user item still carries what the earlier steps need.
// With the data erasers (WithDataErasers) the order is: the frozen workspaces
// (companies, then the workspace), support and audit, the memberships and
// credentials, the KYC documents, and the tombstone last.
type AccountPurger struct {
	orgs      PurgeMemberships
	companies CompanyEdges
	consents  Consents
	sessions  SessionRevoker
	keys      APIKeys
	totp      TOTPRemover
	passkeys  Passkeys
	purgeUser UserPurgeFunc
	now       func() time.Time

	orgEraser     OrgEraser
	companyEraser CompanyEraser
	support       SupportRedactor
	audit         AuditAnonymizer
	kyc           KYCStore
	users         Users
	ownerData     []OwnerEraser
}

func NewAccountPurger(orgs PurgeMemberships, companies CompanyEdges, consents Consents, sessions SessionRevoker,
	keys APIKeys, totp TOTPRemover, passkeys Passkeys, purgeUser UserPurgeFunc) *AccountPurger {
	return &AccountPurger{orgs: orgs, companies: companies, consents: consents, sessions: sessions,
		keys: keys, totp: totp, passkeys: passkeys, purgeUser: purgeUser, now: time.Now}
}

type OrgEraser interface {
	EraseOrganization(ctx context.Context, orgID string) error
}

type CompanyEraser interface {
	EraseForOrganization(ctx context.Context, orgID string) error
}

type SupportRedactor interface {
	RedactUser(ctx context.Context, userID string) error
}

type AuditAnonymizer interface {
	AnonymizeUser(ctx context.Context, userID string) error
}

// WithDataErasers adds the Phase 2b steps. kyc may be nil when no KYC bucket
// is configured (dev).
func (p *AccountPurger) WithDataErasers(orgs OrgEraser, companies CompanyEraser, support SupportRedactor,
	audit AuditAnonymizer, kyc KYCStore, users Users) *AccountPurger {
	p.orgEraser, p.companyEraser, p.support, p.audit, p.kyc, p.users = orgs, companies, support, audit, kyc, users
	return p
}

// OwnerEraser removes data keyed by the user that no other step covers (the
// plan-limit counters and the level queue rows).
type OwnerEraser interface {
	EraseOwner(ctx context.Context, userID string) error
}

// WithOwnerData adds erasers for owner-keyed rows, run before the tombstone.
func (p *AccountPurger) WithOwnerData(e ...OwnerEraser) *AccountPurger {
	p.ownerData = append(p.ownerData, e...)
	return p
}

func (p *AccountPurger) Purge(ctx context.Context, r *Request) error {
	u := r.UserID
	if p.orgEraser != nil {
		for _, orgID := range r.Organizations {
			// Companies first: their rows live under the org id, which stays
			// known from the request even after the org row is gone.
			if err := p.companyEraser.EraseForOrganization(ctx, orgID); err != nil {
				return fmt.Errorf("erasing companies of %s: %w", orgID, err)
			}
			if err := p.orgEraser.EraseOrganization(ctx, orgID); err != nil {
				return fmt.Errorf("erasing organization %s: %w", orgID, err)
			}
		}
		if err := p.support.RedactUser(ctx, u); err != nil {
			return fmt.Errorf("redacting support: %w", err)
		}
		if err := p.audit.AnonymizeUser(ctx, u); err != nil {
			return fmt.Errorf("anonymizing audit: %w", err)
		}
	}
	memberships, err := p.orgs.ListForUser(ctx, u)
	if err != nil {
		return fmt.Errorf("listing memberships: %w", err)
	}
	for _, ms := range memberships {
		if ms.Role == organization.RoleOwner {
			continue // single-member workspaces are erased whole (Phase 2b, ruling R6); shared ones were blockers
		}
		if err := p.orgs.Remove(ctx, ms.OrganizationID, u, u); err != nil {
			return fmt.Errorf("leaving organization: %w", err)
		}
	}
	edges, err := p.companies.ListForUser(ctx, u)
	if err != nil {
		return fmt.Errorf("listing company edges: %w", err)
	}
	for _, e := range edges {
		if err := p.companies.RemoveActor(ctx, e.OrganizationID, e.CompanyID, u); err != nil {
			return fmt.Errorf("removing company edge: %w", err)
		}
	}
	grants, err := p.consents.List(ctx, u)
	if err != nil {
		return fmt.Errorf("listing consents: %w", err)
	}
	for _, g := range grants {
		if err := p.consents.Revoke(ctx, u, g.ClientID()); err != nil {
			return fmt.Errorf("revoking consent: %w", err)
		}
	}
	if err := p.sessions.RevokeAll(ctx, u, ""); err != nil {
		return fmt.Errorf("revoking sessions: %w", err)
	}
	keys, err := p.keys.List(ctx, u)
	if err != nil {
		return fmt.Errorf("listing api keys: %w", err)
	}
	for _, k := range keys {
		if err := p.keys.Revoke(ctx, u, k.ID()); err != nil {
			return fmt.Errorf("revoking api key: %w", err)
		}
	}
	if err := p.totp.Remove(ctx, u); err != nil {
		return fmt.Errorf("removing totp: %w", err)
	}
	creds, err := p.passkeys.List(ctx, u)
	if err != nil {
		return fmt.Errorf("listing passkeys: %w", err)
	}
	for _, c := range creds {
		if err := p.passkeys.Delete(ctx, u, c.SK); err != nil {
			return fmt.Errorf("deleting passkey: %w", err)
		}
	}
	for _, e := range p.ownerData {
		if err := e.EraseOwner(ctx, u); err != nil {
			return fmt.Errorf("erasing owner data: %w", err)
		}
	}
	if p.kyc != nil {
		usr, err := p.users.GetByID(ctx, u)
		if err != nil {
			return fmt.Errorf("loading user for kyc documents: %w", err)
		}
		if usr.DeletionState != user.DeletionStatePurged { // the tombstone has no document keys
			if err := moveKYCDocuments(ctx, p.kyc, usr); err != nil {
				return err
			}
		}
	}
	return p.purgeUser(ctx, u, r.ID, p.now())
}
