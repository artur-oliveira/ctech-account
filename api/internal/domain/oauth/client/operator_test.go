package client

import (
	"context"
	"errors"
	"slices"
	"testing"

	"gopkg.aoctech.app/account/api/internal/crypto"
	"gopkg.aoctech.app/account/api/internal/scopes"
)

type operatorRepoStub struct {
	clients map[string]*OAuthClient
}

func (r *operatorRepoStub) GetByID(_ context.Context, id string) (*OAuthClient, error) {
	client, ok := r.clients[id]
	if !ok {
		return nil, ErrNotFound
	}
	return client, nil
}
func (r *operatorRepoStub) Create(_ context.Context, client *OAuthClient) error {
	r.clients[client.ID()] = client
	return nil
}
func (*operatorRepoStub) ListByOwner(context.Context, string) ([]*OAuthClient, error) {
	return nil, nil
}
func (r *operatorRepoStub) Update(_ context.Context, id string, updates map[string]any) error {
	client, ok := r.clients[id]
	if !ok {
		return ErrNotFound
	}
	if value, ok := updates["name"].(string); ok {
		client.Name = value
	}
	if value, ok := updates["redirect_uris"].([]string); ok {
		client.RedirectURIs = value
	}
	if value, ok := updates["allowed_scopes"].([]string); ok {
		client.AllowedScopes = value
	}
	return nil
}
func (*operatorRepoStub) Delete(context.Context, string) error { return nil }

type operatorCatalogStub struct {
	services []scopes.ServiceScopes
	err      error
}

func (c operatorCatalogStub) Catalog(context.Context) ([]scopes.ServiceScopes, error) {
	return c.services, c.err
}

func newOperatorService() (*OperatorService, *operatorRepoStub) {
	repo := &operatorRepoStub{clients: make(map[string]*OAuthClient)}
	catalog := operatorCatalogStub{services: []scopes.ServiceScopes{{
		Service: "internal:wallet", Internal: true,
		Scopes: []scopes.ScopeEntry{{Scope: "internal:wallet:credit"}},
	}}}
	return NewOperatorService(repo, catalog), repo
}

func TestOperatorCreateM2M(t *testing.T) {
	service, repo := newOperatorService()
	created, secret, err := service.CreateM2M(context.Background(), "wallet-worker", " Wallet worker ", []string{"internal:wallet:credit"})
	if err != nil {
		t.Fatalf("CreateM2M: %v", err)
	}
	if secret == "" || created.ClientType != TypeConfidential || !created.FirstParty {
		t.Fatalf("client was not confidential first-party or secret is empty: %+v", created)
	}
	if created.Name != "Wallet worker" || len(created.RedirectURIs) != 0 || created.ID() != "wallet-worker" {
		t.Fatalf("unexpected client: %+v", created)
	}
	if created.OwnerUserID != SystemOwnerUserID {
		t.Fatalf("owner_user_id = %q, want %q", created.OwnerUserID, SystemOwnerUserID)
	}
	if ok, err := crypto.VerifyPassword(secret, created.ClientSecretHash); err != nil || !ok {
		t.Fatalf("stored hash does not verify returned secret: ok=%v err=%v", ok, err)
	}
	if repo.clients["wallet-worker"] != created {
		t.Fatal("client was not persisted")
	}
}

func TestOperatorCreateM2MValidation(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		client  string
		scopes  []string
		wantErr error
	}{
		{"client id required", "", "worker", []string{"internal:wallet:credit"}, ErrInvalidClientID},
		{"name required", "worker", " ", []string{"internal:wallet:credit"}, ErrInvalidClientName},
		{"scope required", "worker", "worker", nil, ErrScopesRequired},
		{"OIDC rejected", "worker", "worker", []string{scopes.OpenID}, ErrOIDCScopeForM2M},
		{"malformed scope", "worker", "worker", []string{"NOT VALID"}, ErrInvalidScope{Scope: "NOT VALID"}},
		{"unregistered scope", "worker", "worker", []string{"wallet:unknown:read"}, ErrInvalidScope{Scope: "wallet:unknown:read"}},
		{"duplicate scope", "worker", "worker", []string{"internal:wallet:credit", "internal:wallet:credit"}, ErrDuplicateScope},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, _ := newOperatorService()
			_, _, err := service.CreateM2M(context.Background(), tt.id, tt.client, tt.scopes)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestOperatorCreateM2MDoesNotOverwrite(t *testing.T) {
	service, repo := newOperatorService()
	repo.clients["worker"] = &OAuthClient{PK: BuildPK("worker")}
	_, _, err := service.CreateM2M(context.Background(), "worker", "Worker", []string{"internal:wallet:credit"})
	if !errors.Is(err, ErrClientAlreadyExists) {
		t.Fatalf("error = %v, want %v", err, ErrClientAlreadyExists)
	}
}

func TestOperatorCreateResourcePublisherBindsNamespace(t *testing.T) {
	repo := &operatorRepoStub{clients: make(map[string]*OAuthClient)}
	catalog := operatorCatalogStub{services: []scopes.ServiceScopes{{
		Service: "internal:account", Internal: true,
		Scopes: []scopes.ScopeEntry{{Scope: scopes.InternalAccountScopeRegistryWrite}},
	}}}
	service := NewOperatorService(repo, catalog)
	created, secret, err := service.CreateResourcePublisher(context.Background(), "scope-publisher-dfe", "DFe publisher", "dfe")
	if err != nil {
		t.Fatal(err)
	}
	if secret == "" || created.ManagedResourceID != "dfe" || len(created.AllowedScopes) != 1 || created.AllowedScopes[0] != scopes.InternalAccountScopeRegistryWrite {
		t.Fatalf("unexpected publisher: %+v", created)
	}
}

func TestEnsureFirstPartyPublicClientCreatesAndReconciles(t *testing.T) {
	service, repo := newOperatorService()
	required := append([]string{scopes.OpenID, scopes.Profile, scopes.Email}, scopes.AccountUserScopes()...)
	created, changed, err := service.EnsureFirstPartyPublicClient(
		context.Background(), "accounts", "CTech Account", "https://accounts.example.test/login/callback", required, nil,
	)
	if err != nil || !changed || !created.FirstParty || !created.IsPublic() {
		t.Fatalf("create: client=%+v changed=%v err=%v", created, changed, err)
	}

	created.AllowedScopes = []string{scopes.OpenID}
	created.RedirectURIs = []string{"https://legacy.example.test/callback"}
	updated, changed, err := service.EnsureFirstPartyPublicClient(
		context.Background(), "accounts", "CTech Account", "https://accounts.example.test/login/callback", required, nil,
	)
	if err != nil || !changed {
		t.Fatalf("reconcile: changed=%v err=%v", changed, err)
	}
	for _, scope := range required {
		if !updated.HasScope(scope) {
			t.Errorf("missing reconciled scope %q", scope)
		}
	}
	if !updated.IsRedirectURIAllowed("https://legacy.example.test/callback") || !updated.IsRedirectURIAllowed("https://accounts.example.test/login/callback") {
		t.Fatalf("redirect URIs were not merged: %v", updated.RedirectURIs)
	}
	if repo.clients["accounts"] != updated {
		t.Fatal("reconciled client was not persisted")
	}
}

func TestEnsureFirstPartyPublicClientSetsAndMergesAudience(t *testing.T) {
	service, _ := newOperatorService()
	required := []string{"poker:rooms:read"}

	created, changed, err := service.EnsureFirstPartyPublicClient(
		context.Background(), "poker-cli", "CTech Poker CLI", "http://127.0.0.1:51789/callback", required,
		[]string{"https://poker.aoctech.app"},
	)
	if err != nil || !changed {
		t.Fatalf("create: changed=%v err=%v", changed, err)
	}
	if len(created.Audience) != 1 || created.Audience[0] != "https://poker.aoctech.app" {
		t.Fatalf("audience not set on create: %+v", created.Audience)
	}

	updated, changed, err := service.EnsureFirstPartyPublicClient(
		context.Background(), "poker-cli", "CTech Poker CLI", "http://127.0.0.1:51789/callback", required,
		[]string{"https://poker.aoctech.app", "https://extra.example.test"},
	)
	if err != nil || !changed {
		t.Fatalf("reconcile: changed=%v err=%v", changed, err)
	}
	if len(updated.Audience) != 2 {
		t.Fatalf("audience not merged: %+v", updated.Audience)
	}

	_, changed, err = service.EnsureFirstPartyPublicClient(
		context.Background(), "poker-cli", "CTech Poker CLI", "http://127.0.0.1:51789/callback", required,
		[]string{"https://poker.aoctech.app"},
	)
	if err != nil || changed {
		t.Fatalf("idempotent re-run with a subset audience must not report a change: changed=%v err=%v", changed, err)
	}
}

func TestEnsureFirstPartyPublicClientScopesAppendsPublishedScopes(t *testing.T) {
	service, repo := newOperatorService()
	repo.clients["wallet"] = &OAuthClient{
		PK: BuildPK("wallet"), ClientType: TypePublic, FirstParty: true,
		RedirectURIs:  []string{"https://wallet.example.test/callback"},
		AllowedScopes: []string{scopes.OpenID, scopes.Profile, scopes.KYC},
		Audience:      []string{"https://wallet.example.test"},
	}

	client, changed, err := service.EnsureFirstPartyPublicClientScopes(
		context.Background(), "wallet", []string{"wallet:balances:read", "wallet:ledger:read"},
	)
	if err != nil || !changed {
		t.Fatalf("append scopes: client=%+v changed=%v err=%v", client, changed, err)
	}
	for _, scope := range []string{scopes.OpenID, scopes.Profile, scopes.KYC, "wallet:balances:read", "wallet:ledger:read"} {
		if !client.HasScope(scope) {
			t.Errorf("missing scope %q", scope)
		}
	}
	if !client.IsRedirectURIAllowed("https://wallet.example.test/callback") || len(client.Audience) != 1 {
		t.Fatalf("unrelated client configuration changed: %+v", client)
	}

	_, changed, err = service.EnsureFirstPartyPublicClientScopes(
		context.Background(), "wallet", []string{"wallet:balances:read", "wallet:ledger:read"},
	)
	if err != nil || changed {
		t.Fatalf("idempotent append: changed=%v err=%v", changed, err)
	}
}

func TestEnsureFirstPartyPublicClientScopesAllowsAPIOnlyResource(t *testing.T) {
	service, _ := newOperatorService()
	client, changed, err := service.EnsureFirstPartyPublicClientScopes(context.Background(), "billing", []string{"billing:invoices:read"})
	if err != nil || changed || client != nil {
		t.Fatalf("missing UI client: client=%+v changed=%v err=%v", client, changed, err)
	}
}

// updateCountingRepo proves a no-op grant writes nothing.
type updateCountingRepo struct {
	*operatorRepoStub
	updates int
}

func (r *updateCountingRepo) Update(ctx context.Context, id string, updates map[string]any) error {
	r.updates++
	return r.operatorRepoStub.Update(ctx, id, updates)
}

func newGrantOperatorService(t *testing.T) (*OperatorService, *updateCountingRepo) {
	t.Helper()
	repo := &updateCountingRepo{operatorRepoStub: &operatorRepoStub{clients: make(map[string]*OAuthClient)}}
	catalog := operatorCatalogStub{services: []scopes.ServiceScopes{{
		Service: "internal:account", Internal: true,
		Scopes: []scopes.ScopeEntry{
			{Scope: scopes.InternalAccountOrgMember},
			{Scope: scopes.InternalAccountUserOrganizations},
			{Scope: scopes.InternalAccountOAuthClientRead},
		},
	}}}
	service := NewOperatorService(repo, catalog)
	if _, _, err := service.CreateM2M(context.Background(), "billing-account", "Billing",
		[]string{scopes.InternalAccountOrgMember, scopes.InternalAccountUserOrganizations}); err != nil {
		t.Fatalf("CreateM2M: %v", err)
	}
	return service, repo
}

func TestGrantM2MScopesAddsOnlyWhatIsMissingAndKeepsTheRest(t *testing.T) {
	service, repo := newGrantOperatorService(t)
	client, added, err := service.GrantM2MScopes(context.Background(), "billing-account",
		[]string{scopes.InternalAccountOrgMember, scopes.InternalAccountOAuthClientRead})
	if err != nil {
		t.Fatalf("GrantM2MScopes: %v", err)
	}
	if len(added) != 1 || added[0] != scopes.InternalAccountOAuthClientRead {
		t.Fatalf("added = %v, want only the new scope", added)
	}
	want := []string{scopes.InternalAccountOrgMember, scopes.InternalAccountUserOrganizations, scopes.InternalAccountOAuthClientRead}
	stored := repo.clients["billing-account"].AllowedScopes
	if !slices.Equal(client.AllowedScopes, want) || !slices.Equal(stored, want) {
		t.Fatalf("returned %v, stored %v, want %v", client.AllowedScopes, stored, want)
	}
}

func TestGrantM2MScopesIsIdempotent(t *testing.T) {
	service, repo := newGrantOperatorService(t)
	ctx := context.Background()
	if _, _, err := service.GrantM2MScopes(ctx, "billing-account", []string{scopes.InternalAccountOAuthClientRead}); err != nil {
		t.Fatalf("first grant: %v", err)
	}
	before := repo.updates
	_, added, err := service.GrantM2MScopes(ctx, "billing-account", []string{scopes.InternalAccountOAuthClientRead})
	if err != nil {
		t.Fatalf("second grant: %v", err)
	}
	if added != nil || repo.updates != before {
		t.Fatalf("a repeated grant added %v and wrote %d times, want nothing", added, repo.updates-before)
	}
}

// A self-service client must never be handed an internal scope: that is a
// third party holding a service-to-service grant.
func TestGrantM2MScopesRefusesSelfServiceAndPublicClients(t *testing.T) {
	service, repo := newGrantOperatorService(t)
	repo.clients["cli_merchant"] = &OAuthClient{PK: BuildPK("cli_merchant"), ClientType: TypeConfidential, OwnerUserID: "usr_1", AllowedScopes: []string{}}
	repo.clients["cli_spa"] = &OAuthClient{PK: BuildPK("cli_spa"), ClientType: TypePublic, FirstParty: true, AllowedScopes: []string{}}
	for _, id := range []string{"cli_merchant", "cli_spa"} {
		_, _, err := service.GrantM2MScopes(context.Background(), id, []string{scopes.InternalAccountOAuthClientRead})
		if !errors.Is(err, ErrNotM2MClient) {
			t.Fatalf("%s: err = %v, want ErrNotM2MClient", id, err)
		}
		if len(repo.clients[id].AllowedScopes) != 0 {
			t.Fatalf("%s was changed: %v", id, repo.clients[id].AllowedScopes)
		}
	}
}

func TestGrantM2MScopesRefusesAPublisher(t *testing.T) {
	service, repo := newGrantOperatorService(t)
	repo.clients["dfe-publisher"] = &OAuthClient{
		PK: BuildPK("dfe-publisher"), ClientType: TypeConfidential, FirstParty: true,
		ManagedResourceID: "dfe", AllowedScopes: []string{scopes.InternalAccountScopeRegistryWrite},
	}
	_, _, err := service.GrantM2MScopes(context.Background(), "dfe-publisher", []string{scopes.InternalAccountOAuthClientRead})
	if !errors.Is(err, ErrPublisherClient) {
		t.Fatalf("err = %v, want ErrPublisherClient", err)
	}
}

func TestGrantM2MScopesRejectsBadInput(t *testing.T) {
	service, _ := newGrantOperatorService(t)
	ctx := context.Background()
	if _, _, err := service.GrantM2MScopes(ctx, "no-such-client", []string{scopes.InternalAccountOAuthClientRead}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown client: err = %v, want ErrNotFound", err)
	}
	var scopeErr ErrInvalidScope
	if _, _, err := service.GrantM2MScopes(ctx, "billing-account", []string{"internal:account:not-published"}); !errors.As(err, &scopeErr) {
		t.Fatalf("unpublished scope: err = %v, want ErrInvalidScope", err)
	}
	if _, _, err := service.GrantM2MScopes(ctx, "billing-account", []string{"openid"}); !errors.Is(err, ErrOIDCScopeForM2M) {
		t.Fatalf("OIDC scope: err = %v, want ErrOIDCScopeForM2M", err)
	}
	if _, _, err := service.GrantM2MScopes(ctx, "billing-account", nil); !errors.Is(err, ErrScopesRequired) {
		t.Fatalf("no scopes: err = %v, want ErrScopesRequired", err)
	}
}
