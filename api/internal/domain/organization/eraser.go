package organization

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"gopkg.aoctech.app/account/api/internal/database"
)

// Eraser removes an organization whole: every membership (the owner's
// included), every pending invitation, then the organization row. Used only
// by account deletion for workspaces whose sole member is being deleted
// (D4); never reachable from an HTTP route.
type Eraser struct {
	orgs, memberships, invitations database.Base
}

func NewEraser(db *dynamodb.Client, tablePrefix string) *Eraser {
	return &Eraser{
		orgs:        database.NewBase(db, tablePrefix, orgsTable),
		memberships: database.NewBase(db, tablePrefix, membershipsTable),
		invitations: database.NewBase(db, tablePrefix, invitationsTable),
	}
}

func (e *Eraser) EraseOrganization(ctx context.Context, orgID string) error {
	if err := database.DeleteAllUnder(ctx, &e.memberships, orgPK(orgID), memberSKPrefix); err != nil {
		return err
	}
	if err := database.DeleteAllUnder(ctx, &e.invitations, orgPK(orgID), inviteSKPrefix); err != nil {
		return err
	}
	_, err := e.orgs.DeleteItem(ctx, orgPK(orgID), metaSK)
	return err
}

// EraseOwner removes the plan-limit guard counters of a deleted user
// (docs/specs/2026-10-10-space-plan-limits.md § 3.2: OWNER#{sub} rows). They
// hold counts only, but they are keyed by the sub and have no reason to outlive
// the account.
func (e *Eraser) EraseOwner(ctx context.Context, userID string) error {
	return database.DeleteAllUnder(ctx, &e.orgs, ownerPK(userID), "")
}
