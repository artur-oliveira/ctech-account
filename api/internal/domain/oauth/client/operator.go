package client

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
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
	return s.createM2M(ctx, clientID, name, allowedScopes, "")
}

// CreateResourcePublisher creates a dedicated M2M client that can publish
// only the Resource Server named by managedResourceID. The HTTP registry
// verifies this binding in addition to the token scope.
func (s *OperatorService) CreateResourcePublisher(ctx context.Context, clientID, name, managedResourceID string) (*OAuthClient, string, error) {
	if strings.TrimSpace(managedResourceID) == "" {
		return nil, "", fmt.Errorf("managed resource id is required")
	}
	return s.createM2M(ctx, clientID, name, []string{scopes.InternalAccountScopeRegistryWrite}, strings.TrimSpace(managedResourceID))
}

// EnsureFirstPartyPublicClient creates or reconciles a first-party public
// client — originally just the Authorization Server's own SPA, now also used
// for native/CLI clients of another resource server (see
// cmd/createpublicclient). It is intentionally operator-only: promoting an
// arbitrary self-service client to FirstParty would bypass consent.
//
// audience is optional (nil/empty is valid and preserves the original
// behavior): every issued access token's aud claim is
// [this IdP's own audience, client.EffectiveAudience()...], and
// EffectiveAudience falls back to the client_id itself when Audience is
// unset. The Account SPA needs nothing else — its own audience is always
// prepended. A client acting on a *different* resource server's API (e.g.
// poker-cli calling the poker API) MUST pass that server's ServiceAudience
// here, or the resource server's JWT verifier rejects every token with a 401
// (aud mismatch) before scope/first-party checks are ever reached — the
// symptom is indistinguishable from a bad token, not an authorization
// failure, which is why this bit us silently until traced end to end
// (docs/resource-server-scope-registry.md, "Native / CLI clients").
func (s *OperatorService) EnsureFirstPartyPublicClient(ctx context.Context, clientID, name, redirectURI string, requiredScopes, audience []string) (*OAuthClient, bool, error) {
	clientID = strings.TrimSpace(clientID)
	name = strings.TrimSpace(name)
	if len(clientID) < minOperatorClientIDLength || len(clientID) > maxOperatorClientIDLength || !operatorClientIDPattern.MatchString(clientID) {
		return nil, false, ErrInvalidClientID
	}
	if name == "" || len(name) > maxOperatorClientNameLen {
		return nil, false, ErrInvalidClientName
	}
	if err := validateRedirectURIs([]string{redirectURI}); err != nil {
		return nil, false, err
	}
	if len(requiredScopes) == 0 || scopes.Validate(requiredScopes) != "" {
		return nil, false, ErrScopesRequired
	}

	client, err := s.repo.GetByID(ctx, clientID)
	if errors.Is(err, ErrNotFound) {
		client = &OAuthClient{
			PK: BuildPK(clientID), Name: name, ClientType: TypePublic,
			RedirectURIs: []string{redirectURI}, AllowedScopes: append([]string(nil), requiredScopes...),
			Audience:   append([]string(nil), audience...),
			FirstParty: true, OwnerUserID: SystemOwnerUserID,
		}
		if err := s.repo.Create(ctx, client); err != nil {
			return nil, false, fmt.Errorf("creating first-party public client: %w", err)
		}
		return client, true, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("loading first-party public client: %w", err)
	}
	if !client.IsPublic() || !client.FirstParty || client.OwnerUserID != SystemOwnerUserID {
		return nil, false, fmt.Errorf("existing self client %q is not a system-owned first-party public client", clientID)
	}

	redirects := append([]string(nil), client.RedirectURIs...)
	if !slices.Contains(redirects, redirectURI) {
		redirects = append(redirects, redirectURI)
	}
	allowed := append([]string(nil), client.AllowedScopes...)
	for _, scope := range requiredScopes {
		if !slices.Contains(allowed, scope) {
			allowed = append(allowed, scope)
		}
	}
	auds := append([]string(nil), client.Audience...)
	for _, a := range audience {
		if !slices.Contains(auds, a) {
			auds = append(auds, a)
		}
	}
	if client.Name == name && slices.Equal(redirects, client.RedirectURIs) && slices.Equal(allowed, client.AllowedScopes) && slices.Equal(auds, client.Audience) {
		return client, false, nil
	}
	if err := s.repo.Update(ctx, clientID, map[string]any{
		"name": name, "redirect_uris": redirects, "allowed_scopes": allowed, "audience": auds,
	}); err != nil {
		return nil, false, fmt.Errorf("updating first-party public client: %w", err)
	}
	client.Name = name
	client.RedirectURIs = redirects
	client.AllowedScopes = allowed
	client.Audience = auds
	return client, true, nil
}

// EnsureFirstPartyPublicClientScopes appends newly published public scopes to
// the existing first-party UI client with the same ID as its Resource Server.
// A missing client is valid for API-only resources. Redirect URIs, explicit
// audiences and existing grants are never replaced or removed.
func (s *OperatorService) EnsureFirstPartyPublicClientScopes(ctx context.Context, clientID string, requiredScopes []string) (*OAuthClient, bool, error) {
	clientID = strings.TrimSpace(clientID)
	if len(requiredScopes) == 0 {
		return nil, false, nil
	}
	for _, scope := range requiredScopes {
		if !scopes.IsValid(scope) || !strings.HasPrefix(scope, clientID+":") || strings.HasPrefix(scope, scopes.InternalServicePrefix+":") {
			return nil, false, ErrInvalidScope{Scope: scope}
		}
	}

	client, err := s.repo.GetByID(ctx, clientID)
	if errors.Is(err, ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("loading first-party resource client: %w", err)
	}
	if !client.IsPublic() || !client.FirstParty {
		return nil, false, fmt.Errorf("existing resource client %q is not a first-party public client", clientID)
	}

	allowed := append([]string(nil), client.AllowedScopes...)
	for _, scope := range requiredScopes {
		if !slices.Contains(allowed, scope) {
			allowed = append(allowed, scope)
		}
	}
	if slices.Equal(allowed, client.AllowedScopes) {
		return client, false, nil
	}
	if err := s.repo.Update(ctx, clientID, map[string]any{"allowed_scopes": allowed}); err != nil {
		return nil, false, fmt.Errorf("updating first-party resource client scopes: %w", err)
	}
	client.AllowedScopes = allowed
	return client, true, nil
}

func (s *OperatorService) createM2M(ctx context.Context, clientID, name string, allowedScopes []string, managedResourceID string) (*OAuthClient, string, error) {
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
		PK:                BuildPK(clientID),
		Name:              name,
		ClientSecretHash:  secretHash,
		ClientType:        TypeConfidential,
		RedirectURIs:      []string{},
		AllowedScopes:     append([]string(nil), allowedScopes...),
		FirstParty:        true,
		OwnerUserID:       SystemOwnerUserID,
		ManagedResourceID: managedResourceID,
	}
	if err := s.repo.Create(ctx, client); err != nil {
		return nil, "", fmt.Errorf("persisting client: %w", err)
	}
	return client, rawSecret, nil
}
