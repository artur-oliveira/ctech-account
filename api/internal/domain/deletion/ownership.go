package deletion

import (
	"context"

	"gopkg.aoctech.app/account/api/internal/domain/oauth/client"
	"gopkg.aoctech.app/account/api/internal/domain/organization"
	"gopkg.aoctech.app/api-commons/erasure"
)

const (
	BlockerSharedOrganization = "account.organization_shared_owner"
	BlockerOwnedClient        = "account.oauth_client_owned"
)

type OrgMemberships interface {
	ListForUser(ctx context.Context, userID string) ([]*organization.Membership, error)
	ListMembers(ctx context.Context, orgID, actorUserID string) ([]*organization.Membership, error)
	KindOf(ctx context.Context, orgID string) (string, error)
}

type OwnedClients interface {
	ListByOwner(ctx context.Context, ownerUserID string) ([]*client.OAuthClient, error)
}

// AccountOwnership answers what ctech-account itself blocks: workspaces the
// user owns together with other members (ownership must be transferred) and
// OAuth clients the user registered (ruling R4). Workspaces the user owns
// alone, of either kind, are returned to be erased with them (D4).
type AccountOwnership struct {
	orgs    OrgMemberships
	clients OwnedClients
}

func NewAccountOwnership(orgs OrgMemberships, clients OwnedClients) *AccountOwnership {
	return &AccountOwnership{orgs: orgs, clients: clients}
}

func (o *AccountOwnership) Assess(ctx context.Context, userID string) ([]Workspace, []erasure.Blocker, error) {
	memberships, err := o.orgs.ListForUser(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	var sole []Workspace
	var blockers []erasure.Blocker
	for _, ms := range memberships {
		if ms.Role != organization.RoleOwner {
			continue
		}
		members, err := o.orgs.ListMembers(ctx, ms.OrganizationID, userID)
		if err != nil {
			return nil, nil, err
		}
		if len(members) <= 1 {
			kind, err := o.orgs.KindOf(ctx, ms.OrganizationID)
			if err != nil {
				return nil, nil, err
			}
			sole = append(sole, Workspace{ID: ms.OrganizationID, Kind: kind})
			continue
		}
		blockers = append(blockers, erasure.Blocker{Code: BlockerSharedOrganization,
			Detail: map[string]any{"organization_id": ms.OrganizationID}})
	}
	owned, err := o.clients.ListByOwner(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	for _, c := range owned {
		if c.FirstParty {
			continue
		}
		blockers = append(blockers, erasure.Blocker{Code: BlockerOwnedClient, Detail: map[string]any{"client_id": c.ID()}})
	}
	return sole, blockers, nil
}
