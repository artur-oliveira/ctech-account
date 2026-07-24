package client

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"gopkg.aoctech.app/account/api/internal/crypto"
	"gopkg.aoctech.app/account/api/internal/scopes"
)

const (
	minOperatorClientIDLength = 3
	maxOperatorClientIDLength = 128
	maxOperatorClientNameLen  = 120
	SystemOwnerUserID         = "system"
)

var (
	operatorClientIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~-]*$`)

	ErrClientAlreadyExists = errors.New("oauth client already exists")
	ErrInvalidClientID     = errors.New("client_id must be 3-128 URL-safe characters and start with a letter or number")
	ErrInvalidClientName   = errors.New("name is required and must not exceed 120 characters")
	ErrScopesRequired      = errors.New("at least one scope is required")
	ErrOIDCScopeForM2M     = errors.New("OIDC identity scopes are not valid for machine-to-machine clients")
	ErrDuplicateScope      = errors.New("duplicate scope")
)

// OperatorScopeCatalog exposes the complete runtime catalog, including
// internal scopes hidden from self-service provisioning.
type OperatorScopeCatalog interface {
	Catalog(ctx context.Context) ([]scopes.ServiceScopes, error)
}

// OperatorService provisions trusted machine-to-machine clients. Keep this
// separate from Service: self-service creation must never set FirstParty or
// assign internal scopes.
type OperatorService struct {
	repo    Repository
	catalog OperatorScopeCatalog
}

func NewOperatorService(repo Repository, catalog OperatorScopeCatalog) *OperatorService {
	return &OperatorService{repo: repo, catalog: catalog}
}

// ValidateM2MInput performs all validation that does not require consulting
// DynamoDB. It is exported so operator CLIs can fail before opening AWS clients.
func ValidateM2MInput(clientID, name string, allowedScopes []string) error {
	clientID = strings.TrimSpace(clientID)
	name = strings.TrimSpace(name)

	if len(clientID) < minOperatorClientIDLength || len(clientID) > maxOperatorClientIDLength || !operatorClientIDPattern.MatchString(clientID) {
		return ErrInvalidClientID
	}
	if name == "" || len(name) > maxOperatorClientNameLen {
		return ErrInvalidClientName
	}
	if len(allowedScopes) == 0 {
		return ErrScopesRequired
	}

	seen := make(map[string]struct{}, len(allowedScopes))
	for _, scope := range allowedScopes {
		if !scopes.IsValid(scope) {
			return ErrInvalidScope{Scope: scope}
		}
		if scopes.IsOIDC(scope) {
			return fmt.Errorf("%w: %q", ErrOIDCScopeForM2M, scope)
		}
		if _, exists := seen[scope]; exists {
			return fmt.Errorf("%w: %q", ErrDuplicateScope, scope)
		}
		seen[scope] = struct{}{}
	}
	return nil
}

// CreateM2M creates a named, confidential first-party client and returns its
// raw secret exactly once. Token audiences are derived from the scope catalog.
func (s *OperatorService) CreateM2M(ctx context.Context, clientID, name string, allowedScopes []string) (*OAuthClient, string, error) {
	clientID = strings.TrimSpace(clientID)
	name = strings.TrimSpace(name)

	if err := ValidateM2MInput(clientID, name, allowedScopes); err != nil {
		return nil, "", err
	}

	catalog, err := s.catalog.Catalog(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("consulting scope catalog: %w", err)
	}
	registered := make(map[string]struct{})
	for _, service := range catalog {
		for _, entry := range service.Scopes {
			registered[entry.Scope] = struct{}{}
		}
	}
	for _, scope := range allowedScopes {
		if _, ok := registered[scope]; !ok {
			return nil, "", ErrInvalidScope{Scope: scope}
		}
	}

	if _, err := s.repo.GetByID(ctx, clientID); err == nil {
		return nil, "", ErrClientAlreadyExists
	} else if !errors.Is(err, ErrNotFound) {
		return nil, "", fmt.Errorf("checking existing client: %w", err)
	}

	rawSecret, _, err := crypto.GenerateOpaqueToken()
	if err != nil {
		return nil, "", fmt.Errorf("generating client secret: %w", err)
	}
	secretHash, err := crypto.HashPassword(rawSecret)
	if err != nil {
		return nil, "", fmt.Errorf("hashing client secret: %w", err)
	}

	client := &OAuthClient{
		PK:               BuildPK(clientID),
		Name:             name,
		ClientSecretHash: secretHash,
		ClientType:       TypeConfidential,
		RedirectURIs:     []string{},
		AllowedScopes:    append([]string(nil), allowedScopes...),
		FirstParty:       true,
		OwnerUserID:      SystemOwnerUserID,
	}
	if err := s.repo.Create(ctx, client); err != nil {
		return nil, "", fmt.Errorf("persisting client: %w", err)
	}
	return client, rawSecret, nil
}
