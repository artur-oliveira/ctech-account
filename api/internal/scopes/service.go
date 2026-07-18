package scopes

import (
	"context"
	"strings"
	"time"

	"gopkg.aoctech.app/account/api/internal/cache"
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

func NewCatalogService(repo Repository, cacheClient *cache.Client) *CatalogService {
	return &CatalogService{repo: repo, cache: cacheClient}
}

// Catalog returns every service's scopes, cache-first.
func (s *CatalogService) Catalog(ctx context.Context) ([]ServiceScopes, error) {
	if s.cache != nil && s.cache.Enabled() {
		var cached []ServiceScopes
		if err := s.cache.Get(ctx, CatalogCacheKey, &cached); err == nil && len(cached) > 0 {
			return cached, nil
		}
	}

	services, err := s.repo.LoadCatalog(ctx)
	if err != nil {
		return nil, err
	}
	if s.cache != nil && s.cache.Enabled() && len(services) > 0 {
		_ = s.cache.Set(ctx, CatalogCacheKey, services, catalogCacheTTL)
	}
	return services, nil
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
	prefixes := make(map[string]struct{}, len(ss))
	for _, sc := range ss {
		prefixes[servicePrefix(sc)] = struct{}{}
	}
	var auds []string
	for _, svc := range services {
		if svc.Audience == "" {
			continue
		}
		if _, ok := prefixes[svc.Service]; ok {
			auds = append(auds, svc.Audience)
		}
	}
	return auds, nil
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
