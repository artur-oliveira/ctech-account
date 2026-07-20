package scopes

import (
	"context"
	"testing"

	"gopkg.aoctech.app/account/api/internal/cache"
)

func TestIsValid(t *testing.T) {
	valid := []string{
		"openid", "profile", "email",
		"dfe:nfes:write", "dfe:nfes:read", "dfe:nfe", "dfe:*:read", "dfe:nfe:*",
		"account:profile:read", "poker-online:table_92:join",
	}
	for _, s := range valid {
		if !IsValid(s) {
			t.Errorf("expected %q to be valid", s)
		}
	}

	invalid := []string{
		"", "read", "write", "admin", // bare words are not service scopes
		"Dfe:nfe:read",        // uppercase
		"dfe:nfes:read:extra", // too many segments
		"dfe::read",           // empty segment
		":nfe:read",           // empty service
		"*:nfe:read",          // wildcard service
		"dfe:nfe :read",       // whitespace
		"Not A Scope!",
	}
	for _, s := range invalid {
		if IsValid(s) {
			t.Errorf("expected %q to be invalid", s)
		}
	}
}

func TestValidate(t *testing.T) {
	if bad := Validate([]string{"openid", "dfe:nfes:read"}); bad != "" {
		t.Errorf("expected all valid, got %q", bad)
	}
	if bad := Validate([]string{"openid", "bogus"}); bad != "bogus" {
		t.Errorf("expected first invalid scope, got %q", bad)
	}
}

func TestKYCIsIdentityScope(t *testing.T) {
	if !IsOIDC(KYC) {
		t.Fatal("kyc must be a valid identity scope")
	}
	if !IsValid(KYC) {
		t.Fatal("kyc must be valid")
	}
}

func TestInternalWalletConfirmDepositMatchesServiceGrammar(t *testing.T) {
	if !IsValid(InternalWalletConfirmDeposit) {
		t.Fatal("internal:wallet:confirm-deposit must be a valid service scope")
	}
}

func TestFilterPublicHidesInternalServices(t *testing.T) {
	in := []ServiceScopes{
		{Service: "account"},
		{Service: InternalServicePrefix, Internal: true},
	}
	out := FilterPublic(in)
	if len(out) != 1 || out[0].Service != "account" {
		t.Fatalf("expected only account service, got %+v", out)
	}
}

func TestDefaultCatalogContainsKYCAndInternal(t *testing.T) {
	var hasKYC, hasInternal bool
	for _, svc := range DefaultCatalog() {
		for _, s := range svc.Scopes {
			if s.Scope == KYC {
				hasKYC = true
			}
			if s.Scope == InternalWalletConfirmDeposit && svc.Internal {
				hasInternal = true
			}
		}
	}
	if !hasKYC || !hasInternal {
		t.Fatalf("catalog missing kyc=%v internal:wallet:confirm-deposit(internal)=%v", hasKYC, hasInternal)
	}
}

func TestValidateGrantableRejectsInternalScopes(t *testing.T) {
	// Internal scopes exist in the catalog but must never be grantable through
	// self-service creation endpoints (OAuth clients, API keys) — assignment is
	// seed/operator only.
	svc := newSeededCatalogService()
	if bad, _ := svc.ValidateGrantable(context.Background(), []string{InternalWalletConfirmDeposit}); bad != InternalWalletConfirmDeposit {
		t.Fatalf("internal:wallet:confirm-deposit must not be grantable, got %q", bad)
	}
}

// staticRepo is an in-memory catalog Repository serving the default seed.
type staticRepo struct{ services []ServiceScopes }

func (r *staticRepo) LoadCatalog(_ context.Context) ([]ServiceScopes, error) {
	return r.services, nil
}

func (r *staticRepo) PutService(_ context.Context, svc ServiceScopes) error {
	r.services = append(r.services, svc)
	return nil
}

func newSeededCatalogService() *CatalogService {
	disabledCache, _ := cache.New("")
	return NewCatalogService(&staticRepo{services: DefaultCatalog()}, disabledCache)
}

func TestCatalog(t *testing.T) {
	ctx := context.Background()
	svc := newSeededCatalogService()

	services, err := svc.Catalog(ctx)
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	if len(services) == 0 {
		t.Fatal("catalog must not be empty")
	}
	// Every seed scope must satisfy the grammar (or be an OIDC scope).
	for _, s := range services {
		for _, e := range s.Scopes {
			if !IsValid(e.Scope) {
				t.Errorf("catalog scope %q violates the scope grammar", e.Scope)
			}
			if e.Description == "" || e.DescriptionPT == "" {
				t.Errorf("catalog scope %q missing descriptions", e.Scope)
			}
		}
	}

	if ok, _ := svc.InCatalog(ctx, "dfe:nfes:read"); !ok {
		t.Error("dfe:nfes:read should be in catalog")
	}
	if ok, _ := svc.InCatalog(ctx, "dfe:bogus:read"); ok {
		t.Error("dfe:bogus:read should not be in catalog")
	}
	if bad, _ := svc.ValidateGrantable(ctx, []string{"openid", "dfe:nfes:write"}); bad != "" {
		t.Errorf("expected grantable, got %q", bad)
	}
	if bad, _ := svc.ValidateGrantable(ctx, []string{"dfe:nfes:read", "account:*:read"}); bad != "account:*:read" {
		t.Errorf("wildcards are not grantable via catalog, got %q", bad)
	}

	auds, _ := svc.AudiencesFor(ctx, []string{"dfe:nfes:read", "account:profile:read"})
	if len(auds) != 1 || auds[0] != "https://dfe-api.aoctech.app" {
		t.Errorf("expected dfe audience only, got %v", auds)
	}
}

// newCachedCatalogService builds a CatalogService backed by an in-memory cache
// and a repo seeded from DefaultCatalog so cache hits/misses are observable.
func newCachedCatalogService() *CatalogService {
	return NewCatalogService(&staticRepo{services: DefaultCatalog()}, cache.NewInMemory())
}

// TestPutServiceInvalidatesCatalogCache is the regression test for CAC-019: a
// service written via PutService must surface through Catalog immediately, not
// after the cache TTL. The first Catalog() call populates the cache, so without
// the invalidation the second Catalog() would still return the stale cached set.
func TestPutServiceInvalidatesCatalogCache(t *testing.T) {
	ctx := context.Background()
	svc := newCachedCatalogService()

	// Prime the cache.
	if _, err := svc.Catalog(ctx); err != nil {
		t.Fatalf("catalog: %v", err)
	}
	if bad, _ := svc.ValidateGrantable(ctx, []string{"poker:table:read"}); bad != "poker:table:read" {
		t.Fatalf("expected poker:table:read absent before PutService, got %q", bad)
	}

	newSvc := ServiceScopes{
		Service:  "poker",
		Name:     "CTech Poker",
		Audience: "https://poker-api.aoctech.app",
		Scopes:   []ScopeEntry{{Scope: "poker:table:read", Description: "Read tables", DescriptionPT: "Ler mesas"}},
	}
	if err := svc.PutService(ctx, newSvc); err != nil {
		t.Fatalf("PutService: %v", err)
	}

	// Cache must have been invalidated and repopulated with the new service.
	services, err := svc.Catalog(ctx)
	if err != nil {
		t.Fatalf("catalog after PutService: %v", err)
	}
	var found bool
	for _, s := range services {
		if s.Service == "poker" {
			found = true
		}
	}
	if !found {
		t.Fatal("catalog still missing poker service after PutService — cache not invalidated")
	}
	if bad, _ := svc.ValidateGrantable(ctx, []string{"poker:table:read"}); bad != "" {
		t.Errorf("expected poker:table:read grantable after PutService, got %q", bad)
	}
}

func TestAudiencesForResolvesInternalScopeBySubService(t *testing.T) {
	// Regression: internal:<service>:<action> scopes must resolve the target
	// service's own audience, not a shared "internal" bucket — otherwise the
	// downstream service (e.g. ctech-wallet) can't validate the token is
	// actually meant for it.
	svc := newSeededCatalogService()
	auds, err := svc.AudiencesFor(context.Background(), []string{InternalWalletConfirmDeposit})
	if err != nil {
		t.Fatalf("AudiencesFor: %v", err)
	}
	if len(auds) != 1 || auds[0] != "https://wallet-api.aoctech.app" {
		t.Fatalf("expected wallet audience only, got %v", auds)
	}
}
