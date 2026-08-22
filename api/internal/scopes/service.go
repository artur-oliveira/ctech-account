package scopes

import (
	"context"
	"errors"
	"strings"
	"time"

	"gopkg.aoctech.app/account/api/internal/cache"
	"gopkg.aoctech.app/account/api/internal/observability"
)

// CatalogCacheKey caches the loaded catalog in Valkey. After adding a scope to
// the DynamoDB table, invalidate manually with: DEL scope_catalog — or wait for
// the TTL.
const CatalogCacheKey = "scope_catalog"

// catalogCacheTTL bounds staleness after out-of-band catalog edits.
const catalogCacheTTL = 5 * time.Minute

// CatalogService serves the grantable-scope catalog from DynamoDB with a
// Valkey cache in front. All validation of user-supplied scopes goes through
// it, so a scope becomes selectable/grantable the moment its item lands in the
// table (plus at most catalogCacheTTL).
type CatalogService struct {
	repo  Repository
	cache *cache.Client
}

type resourceLoader interface {
	LoadResources(ctx context.Context) ([]ResourceServer, error)
}

func NewCatalogService(repo Repository, cacheClient *cache.Client) *CatalogService {
	return &CatalogService{repo: repo, cache: cacheClient}
}

// Catalog returns every service's scopes, cache-first.
func (s *CatalogService) Catalog(ctx context.Context) ([]ServiceScopes, error) {
	if s.cache != nil && s.cache.Enabled() {
		var cached []ServiceScopes
		if err := s.cache.Get(ctx, CatalogCacheKey, &cached); err == nil && len(cached) > 0 {
			return cached, nil
		} else if err != nil && !errors.Is(err, cache.ErrNotFound) {
			observability.Warn(ctx, "scope catalog: cache read failed; falling back to DynamoDB", err)
		}
	}

	services, err := s.repo.LoadCatalog(ctx)
	if err != nil {
		return nil, err
	}
	// V2 resources override their legacy service/internal:<service> seed rows.
	// Repositories used by older tests or alternate deployments need only the
	// original Repository interface, preserving backwards compatibility.
	if loader, ok := s.repo.(resourceLoader); ok {
		resources, loadErr := loader.LoadResources(ctx)
		if loadErr != nil {
			return nil, loadErr
		}
		services = mergeResourceServers(services, resources)
	}
	if s.cache != nil && s.cache.Enabled() && len(services) > 0 {
		if err := s.cache.Set(ctx, CatalogCacheKey, services, catalogCacheTTL); err != nil {
			observability.Warn(ctx, "scope catalog: failed to populate cache", err)
		}
	}
	return services, nil
}

// PutService writes a service's scopes to DynamoDB and invalidates the catalog
// cache so the change is visible without waiting for the TTL (CAC-019).
func (s *CatalogService) PutService(ctx context.Context, svc ServiceScopes) error {
	if err := s.repo.PutService(ctx, svc); err != nil {
		return err
	}
	if s.cache != nil && s.cache.Enabled() {
		if err := s.cache.Delete(ctx, CatalogCacheKey); err != nil {
			observability.Warn(ctx, "scope catalog: failed to invalidate cache", err)
		}
	}
	return nil
}

// InCatalog reports whether scope is grantable.
func (s *CatalogService) InCatalog(ctx context.Context, scope string) (bool, error) {
	services, err := s.Catalog(ctx)
	if err != nil {
		return false, err
	}
	for _, svc := range services {
		for _, e := range svc.Scopes {
			if e.Scope == scope {
				return true, nil
			}
		}
	}
	return false, nil
}

// ValidateGrantable returns the first scope in ss missing from the catalog, or
// "" when all are grantable. Creation endpoints fail closed on lookup errors.
// Internal services are excluded: their scopes are seed-assigned only and must
// never be claimable through self-service client or API key creation.
func (s *CatalogService) ValidateGrantable(ctx context.Context, ss []string) (string, error) {
	services, err := s.Catalog(ctx)
	if err != nil {
		return "", err
	}
	index := make(map[string]struct{})
	for _, svc := range services {
		if svc.Internal {
			continue
		}
		for _, e := range svc.Scopes {
			index[e.Scope] = struct{}{}
		}
	}
	for _, sc := range ss {
		if _, ok := index[sc]; !ok {
			return sc, nil
		}
	}
	return "", nil
}

// AudiencesFor returns the distinct audience identifiers of the services whose
// scopes appear in ss (e.g. a dfe:* scope pulls in dfe's SERVICE_AUDIENCE).
// Services without a configured audience contribute nothing.
func (s *CatalogService) AudiencesFor(ctx context.Context, ss []string) ([]string, error) {
	services, err := s.Catalog(ctx)
	if err != nil {
		return nil, err
	}
	requested := make(map[string]struct{}, len(ss))
	for _, sc := range ss {
		requested[sc] = struct{}{}
	}
	seen := make(map[string]struct{})
	var auds []string
	for _, svc := range services {
		if svc.Audience == "" {
			continue
		}
		matches := false
		for _, entry := range svc.Scopes {
			if _, ok := requested[entry.Scope]; ok {
				matches = true
				break
			}
		}
		if matches {
			if _, ok := seen[svc.Audience]; !ok {
				auds = append(auds, svc.Audience)
				seen[svc.Audience] = struct{}{}
			}
		}
	}
	return auds, nil
}

func mergeResourceServers(legacy []ServiceScopes, resources []ResourceServer) []ServiceScopes {
	managed := make(map[string]struct{}, len(resources))
	for _, resource := range resources {
		managed[resource.ID()] = struct{}{}
	}
	out := make([]ServiceScopes, 0, len(legacy)+len(resources)*2)
	for _, svc := range legacy {
		id := svc.Service
		if strings.HasPrefix(id, InternalServicePrefix+":") {
			id = strings.TrimPrefix(id, InternalServicePrefix+":")
		}
		if _, replaced := managed[id]; !replaced {
			out = append(out, svc)
		}
	}
	for _, resource := range resources {
		var public, internal []ScopeEntry
		for _, scope := range resource.Scopes {
			entry := ScopeEntry{Scope: scope.Scope, Description: scope.Description, DescriptionPT: scope.DescriptionPT}
			if scope.Status == StatusDeprecated {
				// Deprecated entries remain internal to token audience resolution;
				// public discovery and new grants must not expose them.
				internal = append(internal, entry)
				continue
			}
			if scope.Visibility == VisibilityInternal {
				internal = append(internal, entry)
			} else {
				public = append(public, entry)
			}
		}
		if len(public) > 0 {
			out = append(out, ServiceScopes{Service: resource.ID(), Name: resource.DisplayName, Audience: resource.Audience, Scopes: public})
		}
		if len(internal) > 0 {
			out = append(out, ServiceScopes{Service: InternalServicePrefix + ":" + resource.ID(), Name: "Internal — " + resource.DisplayName, Audience: resource.Audience, Scopes: internal, Internal: true})
		}
	}
	return out
}

// servicePrefix returns the catalog Service key a scope belongs to. Normal
// scopes key on their first segment ("dfe:nfes:read" -> "dfe"). Internal
// scopes are namespaced machine-to-machine grants shared across every CTech
// service ("internal:wallet:confirm-deposit"), so the real target — and its
// audience — is identified by the first two segments ("internal:wallet"),
// not just "internal".
func servicePrefix(scope string) string {
	first := strings.IndexByte(scope, ':')
	if first < 0 {
		return scope
	}
	if scope[:first] != InternalServicePrefix {
		return scope[:first]
	}
	if second := strings.IndexByte(scope[first+1:], ':'); second > 0 {
		return scope[:first+1+second]
	}
	return scope[:first]
}
