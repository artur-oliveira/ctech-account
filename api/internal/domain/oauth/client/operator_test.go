package client

import (
	"context"
	"errors"
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
func (*operatorRepoStub) Update(context.Context, string, map[string]any) error { return nil }
func (*operatorRepoStub) Delete(context.Context, string) error                 { return nil }

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
