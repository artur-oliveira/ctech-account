package client

import (
	"context"
	"errors"
	"fmt"
)

// OwnerStatus answers whether a client's owner account can still be acted for.
// user.Service satisfies it (IsActive). The interface keeps this package free
// of the user domain.
type OwnerStatus interface {
	IsActive(ctx context.Context, userID string) (bool, error)
}

// InternalView is everything another service may know about a client. It never
// holds the secret hash, the redirect URIs, the audience, first-party status
// or the managed resource.
type InternalView struct {
	ClientID      string
	OwnerUserID   string
	AllowedScopes []string
	Active        bool
}

// InternalService serves the service-to-service read of one client
// (GET /v1.0/internal/oauth-clients/:client_id; ctech-billing console
// self-service spec § 6.3).
type InternalService struct {
	repo   Repository
	owners OwnerStatus
}

func NewInternalService(repo Repository, owners OwnerStatus) *InternalService {
	return &InternalService{repo: repo, owners: owners}
}

// Describe returns the client's owner, granted scopes and whether it is
// active. ErrNotFound covers an unknown client and an id no creation path can
// produce (empty or longer than maxOperatorClientIDLength); the second kind is
// refused without a read.
//
// Active means the client can still authenticate as itself for a live owner:
// it is confidential with a secret, and its owner is the platform
// (SystemOwnerUserID) or an account that exists, is enabled and is not locked
// by a deletion request. Account deletion does not touch OAuth clients, so
// without the owner check a locked person's client would still look usable.
func (s *InternalService) Describe(ctx context.Context, clientID string) (*InternalView, error) {
	if clientID == "" || len(clientID) > maxOperatorClientIDLength {
		return nil, ErrNotFound
	}
	c, err := s.repo.GetByID(ctx, clientID)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("loading client: %w", err)
	}
	active, err := s.Active(ctx, c)
	if err != nil {
		return nil, err
	}
	return &InternalView{
		ClientID:      c.ID(),
		OwnerUserID:   c.OwnerUserID,
		AllowedScopes: append([]string{}, c.AllowedScopes...),
		Active:        active,
	}, nil
}

// Active is the derivation Describe reports, exported because the token
// endpoint applies the same rule to developer-page clients asking for
// client_credentials (Task 6). One rule, so the route and the token endpoint
// cannot disagree about whether a client is usable.
func (s *InternalService) Active(ctx context.Context, c *OAuthClient) (bool, error) {
	if c.IsPublic() || c.ClientSecretHash == "" {
		return false, nil
	}
	switch c.OwnerUserID {
	case SystemOwnerUserID:
		return true, nil
	case "":
		return false, nil
	}
	ok, err := s.owners.IsActive(ctx, c.OwnerUserID)
	if err != nil {
		return false, fmt.Errorf("checking client owner: %w", err)
	}
	return ok, nil
}
