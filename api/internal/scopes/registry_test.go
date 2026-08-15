package scopes

import (
	"context"
	"errors"
	"testing"
)

type registryRepoStub struct {
	resource *ResourceServer
	history  map[int64]*ResourceRevision
}

func (r *registryRepoStub) LoadResources(context.Context) ([]ResourceServer, error) {
	if r.resource == nil {
		return nil, nil
	}
	return []ResourceServer{*r.resource}, nil
}
func (r *registryRepoStub) GetResource(context.Context, string) (*ResourceServer, error) {
	if r.resource == nil {
		return nil, ErrResourceNotFound
	}
	copy := *r.resource
	copy.Scopes = append([]ScopeDefinition(nil), r.resource.Scopes...)
	return &copy, nil
}
func (r *registryRepoStub) CreateResource(_ context.Context, resource *ResourceServer) error {
	if r.resource != nil {
		return ErrResourceAlreadyExists
	}
	copy := *resource
	r.resource = &copy
	return nil
}
func (r *registryRepoStub) ReconcileResource(_ context.Context, previous, next *ResourceServer) error {
	if r.resource == nil || r.resource.Revision != previous.Revision {
		return ErrRevisionConflict
	}
	copy := *next
	copy.Scopes = append([]ScopeDefinition(nil), next.Scopes...)
	r.resource = &copy
	if r.history == nil {
		r.history = make(map[int64]*ResourceRevision)
	}
	r.history[next.Revision] = &ResourceRevision{ResourceServerID: next.ID(), Revision: next.Revision, DisplayName: next.DisplayName, Scopes: append([]ScopeDefinition(nil), next.Scopes...), ManifestHash: next.ManifestHash}
	return nil
}
func (r *registryRepoStub) GetRevision(_ context.Context, _ string, revision int64) (*ResourceRevision, error) {
	item := r.history[revision]
	if item == nil {
		return nil, ErrResourceNotFound
	}
	return item, nil
}

func dfeManifest(scopes ...ScopeDefinition) Manifest {
	return Manifest{SchemaVersion: 1, ResourceServerID: "dfe", DisplayName: "CTech DF-e", Scopes: scopes}
}

func publicScope(name string) ScopeDefinition {
	return ScopeDefinition{Scope: name, Descriptions: map[string]string{"en": "English description", "pt-BR": "Descricao em portugues"}, Visibility: VisibilityPublic, Status: StatusActive}
}

func TestRegistryPublishIsBoundIdempotentAndRevisioned(t *testing.T) {
	repo := &registryRepoStub{}
	service := NewRegistryService(repo, nil)
	ctx := context.Background()
	if _, err := service.Provision(ctx, "dfe", "CTech DF-e", "https://dfe.example.test", "scope-publisher-dfe"); err != nil {
		t.Fatal(err)
	}
	manifest := dfeManifest(publicScope("dfe:nfes:read"))
	if _, _, err := service.Publish(ctx, "another-client", manifest, 0, "repo", "sha"); !errors.Is(err, ErrInvalidResource) {
		t.Fatalf("foreign publisher error = %v", err)
	}
	created, changed, err := service.Publish(ctx, "scope-publisher-dfe", manifest, 0, "ctech-dfe", "abc123")
	if err != nil || !changed || created.Revision != 1 {
		t.Fatalf("first publish: resource=%+v changed=%v err=%v", created, changed, err)
	}
	if created.Scopes[0].Description == "" || created.Scopes[0].DescriptionPT == "" || created.Scopes[0].Descriptions != nil {
		t.Fatalf("descriptions were not canonicalized: %+v", created.Scopes[0])
	}
	same, changed, err := service.Publish(ctx, "scope-publisher-dfe", manifest, 1, "ctech-dfe", "def456")
	if err != nil || changed || same.Revision != 1 {
		t.Fatalf("idempotent publish: resource=%+v changed=%v err=%v", same, changed, err)
	}
	if _, _, err := service.Publish(ctx, "scope-publisher-dfe", manifest, 0, "", ""); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale revision error = %v", err)
	}
}

func TestRegistryRequiresDeprecationBeforeRemoval(t *testing.T) {
	repo := &registryRepoStub{}
	service := NewRegistryService(repo, nil)
	ctx := context.Background()
	_, _ = service.Provision(ctx, "dfe", "CTech DF-e", "https://dfe.example.test", "publisher")
	first := dfeManifest(publicScope("dfe:nfes:read"), publicScope("dfe:nfes:write"))
	_, _, _ = service.Publish(ctx, "publisher", first, 0, "", "")
	removed := dfeManifest(publicScope("dfe:nfes:read"))
	if _, _, err := service.Publish(ctx, "publisher", removed, 1, "", ""); !errors.Is(err, ErrScopeRemoval) {
		t.Fatalf("direct removal error = %v", err)
	}
	deprecated := dfeManifest(publicScope("dfe:nfes:read"), publicScope("dfe:nfes:write"))
	deprecated.Scopes[1].Status = StatusDeprecated
	if _, _, err := service.Publish(ctx, "publisher", deprecated, 1, "", ""); err != nil {
		t.Fatalf("explicit deprecation: %v", err)
	}
	if _, _, err := service.Publish(ctx, "publisher", removed, 2, "", ""); err != nil {
		t.Fatalf("removing already deprecated scope: %v", err)
	}
}

func TestRegistryRestoreCreatesANewRevision(t *testing.T) {
	repo := &registryRepoStub{}
	service := NewRegistryService(repo, nil)
	ctx := context.Background()
	_, _ = service.Provision(ctx, "dfe", "CTech DF-e", "https://dfe.example.test", "publisher")
	first := dfeManifest(publicScope("dfe:nfes:read"))
	_, _, _ = service.Publish(ctx, "publisher", first, 0, "repo", "one")
	second := dfeManifest(publicScope("dfe:nfes:read"), publicScope("dfe:ctes:read"))
	_, _, _ = service.Publish(ctx, "publisher", second, 1, "repo", "two")
	restored, err := service.Restore(ctx, "dfe", 1, 2, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if restored.Revision != 3 || len(restored.Scopes) != 1 || restored.Scopes[0].Scope != "dfe:nfes:read" {
		t.Fatalf("unexpected restored revision: %+v", restored)
	}
}

func TestValidateManifestEnforcesOwnedNamespace(t *testing.T) {
	tests := []ScopeDefinition{
		publicScope("wallet:balance:read"),
		{Scope: "internal:wallet:credit", Descriptions: map[string]string{"en": "x", "pt-BR": "x"}, Visibility: VisibilityInternal, Status: StatusActive},
		{Scope: "dfe:*:read", Descriptions: map[string]string{"en": "x", "pt-BR": "x"}, Visibility: VisibilityPublic, Status: StatusActive},
	}
	for _, scope := range tests {
		if err := ValidateManifest(dfeManifest(scope)); !errors.Is(err, ErrInvalidResource) {
			t.Errorf("scope %q validation error = %v", scope.Scope, err)
		}
	}
}

type catalogV2RepoStub struct {
	legacy   []ServiceScopes
	resource []ResourceServer
}

func (r catalogV2RepoStub) LoadCatalog(context.Context) ([]ServiceScopes, error) {
	return r.legacy, nil
}
func (catalogV2RepoStub) PutService(context.Context, ServiceScopes) error { return nil }
func (r catalogV2RepoStub) LoadResources(context.Context) ([]ResourceServer, error) {
	return r.resource, nil
}

func TestCatalogV2OverridesLegacyAndResolvesExactAudiences(t *testing.T) {
	repo := catalogV2RepoStub{
		legacy: []ServiceScopes{{Service: "dfe", Audience: "legacy", Scopes: []ScopeEntry{{Scope: "dfe:old:read"}}}},
		resource: []ResourceServer{
			{SK: "dfe", DisplayName: "DF-e", Audience: "https://dfe.example.test", Scopes: []ScopeDefinition{
				{Scope: "dfe:nfes:read", Description: "read", DescriptionPT: "ler", Visibility: VisibilityPublic, Status: StatusActive},
				{Scope: "dfe:nfes:write", Description: "write", DescriptionPT: "gravar", Visibility: VisibilityPublic, Status: StatusDeprecated},
			}},
		},
	}
	service := NewCatalogService(repo, nil)
	if missing, err := service.ValidateGrantable(context.Background(), []string{"dfe:nfes:read"}); err != nil || missing != "" {
		t.Fatalf("active public scope not grantable: missing=%q err=%v", missing, err)
	}
	if missing, err := service.ValidateGrantable(context.Background(), []string{"dfe:nfes:write"}); err != nil || missing == "" {
		t.Fatalf("deprecated scope must not be grantable: missing=%q err=%v", missing, err)
	}
	audiences, err := service.AudiencesFor(context.Background(), []string{"dfe:nfes:write", "dfe:unknown:read"})
	if err != nil || len(audiences) != 1 || audiences[0] != "https://dfe.example.test" {
		t.Fatalf("audiences = %v, err=%v", audiences, err)
	}
}
