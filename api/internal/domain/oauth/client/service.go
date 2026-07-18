package client

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"gopkg.aoctech.app/account/api/internal/crypto"
)

// Client types.
const (
	TypePublic       = "public"
	TypeConfidential = "confidential"
)

var ErrForbidden = errors.New("client does not belong to this user")
var ErrInvalidClientType = errors.New("client_type must be public or confidential")
var ErrInvalidRedirectURI = errors.New("redirect URIs must be absolute https URLs (http allowed for localhost only)")
var ErrNotConfidential = errors.New("only confidential clients have a secret")

// ErrInvalidScope wraps the first malformed scope for caller-facing messages.
type ErrInvalidScope struct{ Scope string }

func (e ErrInvalidScope) Error() string { return fmt.Sprintf("invalid scope %q", e.Scope) }

// ScopeValidator checks user-supplied scopes against the platform catalog.
// Satisfied by *scopes.CatalogService.
type ScopeValidator interface {
	// ValidateGrantable returns the first non-grantable scope, or "" when all
	// are grantable. A non-nil error means the catalog could not be consulted.
	ValidateGrantable(ctx context.Context, ss []string) (string, error)
}

// Service owns the business rules for self-service OAuth client management.
type Service struct {
	repo   Repository
	scopes ScopeValidator
}

func NewService(repo Repository, scopeValidator ScopeValidator) *Service {
	return &Service{repo: repo, scopes: scopeValidator}
}

func (s *Service) List(ctx context.Context, ownerUserID string) ([]*OAuthClient, error) {
	return s.repo.ListByOwner(ctx, ownerUserID)
}

// Create registers a new OAuth client owned by ownerUserID. For confidential
// clients the returned string is the raw client secret — shown exactly once.
func (s *Service) Create(ctx context.Context, ownerUserID, name, clientType string, redirectURIs, allowedScopes, audience []string) (*OAuthClient, string, error) {
	if clientType != TypePublic && clientType != TypeConfidential {
		return nil, "", ErrInvalidClientType
	}
	if err := validateRedirectURIs(redirectURIs); err != nil {
		return nil, "", err
	}
	bad, err := s.scopes.ValidateGrantable(ctx, allowedScopes)
	if err != nil {
		return nil, "", fmt.Errorf("consulting scope catalog: %w", err)
	}
	if bad != "" {
		return nil, "", ErrInvalidScope{Scope: bad}
	}

	c := &OAuthClient{
		Name:          name,
		ClientType:    clientType,
		RedirectURIs:  redirectURIs,
		AllowedScopes: allowedScopes,
		Audience:      audience,
		OwnerUserID:   ownerUserID,
	}

	rawSecret := ""
	if clientType == TypeConfidential {
		var err error
		rawSecret, err = generateSecret()
		if err != nil {
			return nil, "", err
		}
		hash, err := crypto.HashPassword(rawSecret)
		if err != nil {
			return nil, "", fmt.Errorf("hashing client secret: %w", err)
		}
		c.ClientSecretHash = hash
	}

	if err := s.repo.Create(ctx, c); err != nil {
		return nil, "", fmt.Errorf("persisting client: %w", err)
	}
	return c, rawSecret, nil
}

// Update replaces the mutable fields of an owned client.
func (s *Service) Update(ctx context.Context, ownerUserID, clientID, name string, redirectURIs, allowedScopes, audience []string) (*OAuthClient, error) {
	c, err := s.getOwned(ctx, ownerUserID, clientID)
	if err != nil {
		return nil, err
	}
	if err := validateRedirectURIs(redirectURIs); err != nil {
		return nil, err
	}
	bad, err := s.scopes.ValidateGrantable(ctx, allowedScopes)
	if err != nil {
		return nil, fmt.Errorf("consulting scope catalog: %w", err)
	}
	if bad != "" {
		return nil, ErrInvalidScope{Scope: bad}
	}

	if err := s.repo.Update(ctx, clientID, map[string]any{
		"name":           name,
		"redirect_uris":  redirectURIs,
		"allowed_scopes": allowedScopes,
		"audience":       audience,
	}); err != nil {
		return nil, fmt.Errorf("updating client: %w", err)
	}

	c.Name = name
	c.RedirectURIs = redirectURIs
	c.AllowedScopes = allowedScopes
	c.Audience = audience
	return c, nil
}

func (s *Service) Delete(ctx context.Context, ownerUserID, clientID string) error {
	if _, err := s.getOwned(ctx, ownerUserID, clientID); err != nil {
		return err
	}
	return s.repo.Delete(ctx, clientID)
}

// RegenerateSecret replaces a confidential client's secret and returns the new
// raw value — shown exactly once. The old secret stops working immediately.
func (s *Service) RegenerateSecret(ctx context.Context, ownerUserID, clientID string) (string, error) {
	c, err := s.getOwned(ctx, ownerUserID, clientID)
	if err != nil {
		return "", err
	}
	if c.IsPublic() {
		return "", ErrNotConfidential
	}

	rawSecret, err := generateSecret()
	if err != nil {
		return "", err
	}
	hash, err := crypto.HashPassword(rawSecret)
	if err != nil {
		return "", fmt.Errorf("hashing client secret: %w", err)
	}
	if err := s.repo.Update(ctx, clientID, map[string]any{"client_secret_hash": hash}); err != nil {
		return "", fmt.Errorf("updating client secret: %w", err)
	}
	return rawSecret, nil
}

// getOwned fetches a client and enforces ownership.
func (s *Service) getOwned(ctx context.Context, ownerUserID, clientID string) (*OAuthClient, error) {
	c, err := s.repo.GetByID(ctx, clientID)
	if err != nil {
		return nil, err
	}
	if c.OwnerUserID != ownerUserID {
		return nil, ErrForbidden
	}
	return c, nil
}

func generateSecret() (string, error) {
	raw, _, err := crypto.GenerateOpaqueToken()
	if err != nil {
		return "", fmt.Errorf("generating client secret: %w", err)
	}
	return raw, nil
}

// validateRedirectURIs enforces absolute https redirect URIs, permitting plain
// http only for localhost development callbacks.
func validateRedirectURIs(uris []string) error {
	for _, u := range uris {
		parsed, err := url.Parse(u)
		if err != nil || parsed.Host == "" {
			return ErrInvalidRedirectURI
		}
		switch parsed.Scheme {
		case "https":
		case "http":
			host := parsed.Hostname()
			if host != "localhost" && !strings.HasPrefix(host, "127.") {
				return ErrInvalidRedirectURI
			}
		default:
			return ErrInvalidRedirectURI
		}
	}
	return nil
}
