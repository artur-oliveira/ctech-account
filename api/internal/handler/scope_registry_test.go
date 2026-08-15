package handler

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/api/internal/domain/audit"
	oauthclient "gopkg.aoctech.app/account/api/internal/domain/oauth/client"
	"gopkg.aoctech.app/account/api/internal/middleware"
	"gopkg.aoctech.app/account/api/internal/scopes"
)

type scopeRegistryRepoStub struct{ resource *scopes.ResourceServer }

func (r *scopeRegistryRepoStub) LoadResources(context.Context) ([]scopes.ResourceServer, error) {
	return nil, nil
}
func (r *scopeRegistryRepoStub) GetResource(context.Context, string) (*scopes.ResourceServer, error) {
	if r.resource == nil {
		return nil, scopes.ErrResourceNotFound
	}
	copy := *r.resource
	return &copy, nil
}
func (r *scopeRegistryRepoStub) CreateResource(_ context.Context, resource *scopes.ResourceServer) error {
	copy := *resource
	r.resource = &copy
	return nil
}
func (r *scopeRegistryRepoStub) ReconcileResource(_ context.Context, previous, next *scopes.ResourceServer) error {
	if r.resource.Revision != previous.Revision {
		return scopes.ErrRevisionConflict
	}
	copy := *next
	r.resource = &copy
	return nil
}
func (*scopeRegistryRepoStub) GetRevision(context.Context, string, int64) (*scopes.ResourceRevision, error) {
	return nil, scopes.ErrResourceNotFound
}

type scopeClientRepoStub struct{ client *oauthclient.OAuthClient }

func (r scopeClientRepoStub) GetByID(context.Context, string) (*oauthclient.OAuthClient, error) {
	return r.client, nil
}

type scopeAuditRepoStub struct{ events []*audit.Event }

func (r *scopeAuditRepoStub) Put(_ context.Context, event *audit.Event) error {
	r.events = append(r.events, event)
	return nil
}
func (*scopeAuditRepoStub) QueryByUser(context.Context, string, string, int32) ([]*audit.Event, string, error) {
	return nil, "", nil
}

func newScopeRegistryHTTP(t *testing.T, managedResource string) (*fiber.App, *scopeRegistryRepoStub, *scopeAuditRepoStub) {
	t.Helper()
	repo := &scopeRegistryRepoStub{}
	registry := scopes.NewRegistryService(repo, nil)
	if _, err := registry.Provision(context.Background(), "dfe", "CTech DF-e", "https://dfe.example.test", "scope-publisher-dfe"); err != nil {
		t.Fatal(err)
	}
	audits := &scopeAuditRepoStub{}
	h := NewScopeRegistryHandler(registry, scopeClientRepoStub{client: &oauthclient.OAuthClient{PK: oauthclient.BuildPK("scope-publisher-dfe"), ManagedResourceID: managedResource}}, audit.NewService(audits))
	app := fiber.New()
	v1 := app.Group("/v1.0")
	h.Register(v1, func(c fiber.Ctx) error {
		c.Locals(middleware.LocalUserID, "scope-publisher-dfe")
		c.Locals(middleware.LocalClientID, "scope-publisher-dfe")
		c.Locals(middleware.LocalScopes, scopes.InternalAccountScopeRegistryWrite)
		return c.Next()
	})
	return app, repo, audits
}

func TestScopeRegistryHTTPPublishAndETag(t *testing.T) {
	app, repo, audits := newScopeRegistryHTTP(t, "dfe")
	body := `{"schema_version":1,"resource_server_id":"dfe","display_name":"CTech DF-e","scopes":[{"name":"dfe:nfes:read","descriptions":{"en":"Read invoices","pt-BR":"Consultar notas"},"visibility":"public","status":"active"}]}`
	req := httptest.NewRequest("PUT", "/v1.0/internal/resource-servers/dfe/manifest", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("If-Match", `"0:"`)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("ETag") == "" || repo.resource.Revision != 1 {
		t.Fatalf("status=%d etag=%q resource=%+v", resp.StatusCode, resp.Header.Get("ETag"), repo.resource)
	}
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || result["changed"] != true {
		t.Fatalf("response=%v err=%v", result, err)
	}
	if len(audits.events) != 1 || audits.events[0].EventType != audit.EventScopeManifestPublished {
		t.Fatalf("audit events=%+v", audits.events)
	}
}

func TestScopeRegistryHTTPRejectsWrongBindingAndMissingPrecondition(t *testing.T) {
	app, _, _ := newScopeRegistryHTTP(t, "wallet")
	resp, err := app.Test(httptest.NewRequest("GET", "/v1.0/internal/resource-servers/dfe/manifest", nil))
	if err != nil || resp.StatusCode != 403 {
		t.Fatalf("wrong binding status=%d err=%v", resp.StatusCode, err)
	}

	app, _, _ = newScopeRegistryHTTP(t, "dfe")
	req := httptest.NewRequest("PUT", "/v1.0/internal/resource-servers/dfe/manifest", nil)
	resp, err = app.Test(req)
	if err != nil || resp.StatusCode != 412 {
		t.Fatalf("missing If-Match status=%d err=%v", resp.StatusCode, err)
	}

	app, _, _ = newScopeRegistryHTTP(t, "dfe")
	req = httptest.NewRequest("PUT", "/v1.0/internal/resource-servers/dfe/manifest", nil)
	req.Header.Set("If-Match", `"0:not-the-current-hash"`)
	resp, err = app.Test(req)
	if err != nil || resp.StatusCode != 412 {
		t.Fatalf("wrong ETag status=%d err=%v", resp.StatusCode, err)
	}
}
