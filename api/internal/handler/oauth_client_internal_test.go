package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	oauthclient "gopkg.aoctech.app/account/api/internal/domain/oauth/client"
	"gopkg.aoctech.app/account/api/internal/handler"
	"gopkg.aoctech.app/account/api/internal/middleware"
	"gopkg.aoctech.app/account/api/internal/scopes"
	"gopkg.aoctech.app/api-commons/authguard"
)

// newOAuthClientInternalApp mounts the route the way main.go does, with the
// same guards, so the tests exercise the gate and not just the handler.
func newOAuthClientInternalApp(t *testing.T) *testApp {
	t.Helper()
	ta := newTestApp(t)
	handler.NewOAuthClientInternalHandler(oauthclient.NewInternalService(ta.clientRepo, ta.userSvc)).
		RegisterInternal(ta.app.Group("/v1.0"),
			middleware.RequireAuth(ta.jwtSvc),
			authguard.RequireInternalScope(scopes.InternalAccountOAuthClientRead))
	return ta
}

func seedInternalClient(t *testing.T, ta *testApp, c *oauthclient.OAuthClient) {
	t.Helper()
	if err := ta.clientRepo.Create(context.Background(), c); err != nil {
		t.Fatalf("seeding client: %v", err)
	}
}

func merchantOAuthClient(id, owner string) *oauthclient.OAuthClient {
	return &oauthclient.OAuthClient{
		PK:               oauthclient.BuildPK(id),
		Name:             "Loja da Ana",
		ClientType:       oauthclient.TypeConfidential,
		ClientSecretHash: "hash-must-not-leak",
		RedirectURIs:     []string{"https://loja.example.com/callback"},
		AllowedScopes:    []string{"billing:invoices:write", "billing:customers:read"},
		Audience:         []string{"https://billing.aoctech.app"},
		OwnerUserID:      owner,
	}
}

const internalClientPath = "/v1.0/internal/oauth-clients/"

func TestTheOAuthClientReadNeedsItsOwnInternalScope(t *testing.T) {
	ta := newOAuthClientInternalApp(t)
	owner := ta.registerUser(t, "client-gate@example.com", "Sup3rSecret!pass", "Ana")
	seedInternalClient(t, ta, merchantOAuthClient("cli_gate", owner.ID()))
	path := internalClientPath + "cli_gate"

	// A signed-in person's token never reaches a service route, whatever it carries.
	if resp := ta.doWithToken(http.MethodGet, path, nil, ta.issueToken(t, owner.ID())); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a user token read a client: status %d", resp.StatusCode)
	}
	for name, scope := range map[string]string{
		"org-member":         scopes.InternalAccountOrgMember,
		"user-organizations": scopes.InternalAccountUserOrganizations,
		"company-actor":      scopes.InternalAccountCompanyActor,
		"kyc":                scopes.InternalAccountKYC,
	} {
		tok := ta.issueServiceToken(t, []string{scope})
		if resp := ta.doWithToken(http.MethodGet, path, nil, tok); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("the %s scope read a client: status %d", name, resp.StatusCode)
		}
	}
	ok := ta.issueServiceToken(t, []string{scopes.InternalAccountOAuthClientRead})
	if resp := ta.doWithToken(http.MethodGet, path, nil, ok); resp.StatusCode != http.StatusOK {
		t.Fatalf("the right scope: status %d, want 200", resp.StatusCode)
	}
}

func TestTheOAuthClientReadAnswersTheContractAndNeverTheSecret(t *testing.T) {
	ta := newOAuthClientInternalApp(t)
	owner := ta.registerUser(t, "client-contract@example.com", "Sup3rSecret!pass", "Ana")
	seedInternalClient(t, ta, merchantOAuthClient("cli_contract", owner.ID()))
	tok := ta.issueServiceToken(t, []string{scopes.InternalAccountOAuthClientRead})

	resp := ta.doWithToken(http.MethodGet, internalClientPath+"cli_contract", nil, tok)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	raw := bodyString(resp)
	if strings.Contains(raw, "hash-must-not-leak") || strings.Contains(raw, "secret") {
		t.Fatalf("the response carries secret material: %s", raw)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("decoding %s: %v", raw, err)
	}
	if len(body) != 4 {
		t.Fatalf("got keys %v, want exactly client_id, owner_user_id, allowed_scopes, active", body)
	}
	if body["client_id"] != "cli_contract" || body["owner_user_id"] != owner.ID() || body["active"] != true {
		t.Fatalf("got %+v", body)
	}
	got, _ := body["allowed_scopes"].([]any)
	if len(got) != 2 || got[0] != "billing:invoices:write" || got[1] != "billing:customers:read" {
		t.Fatalf("allowed_scopes = %v", body["allowed_scopes"])
	}
}

func TestAnUnknownOAuthClientIs404(t *testing.T) {
	ta := newOAuthClientInternalApp(t)
	tok := ta.issueServiceToken(t, []string{scopes.InternalAccountOAuthClientRead})
	for _, id := range []string{"cli_missing", strings.Repeat("a", 200)} {
		resp := ta.doWithToken(http.MethodGet, internalClientPath+id, nil, tok)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("id of length %d: status %d, want 404", len(id), resp.StatusCode)
		}
	}
}

func TestTheOAuthClientIsInactiveWhenItsOwnerCannotSignIn(t *testing.T) {
	ta := newOAuthClientInternalApp(t)
	ctx := context.Background()
	tok := ta.issueServiceToken(t, []string{scopes.InternalAccountOAuthClientRead})

	leaving := ta.registerUser(t, "client-leaving@example.com", "Sup3rSecret!pass", "Bia")
	if err := ta.userSvc.MarkPendingDeletion(ctx, leaving.ID(), "req_1"); err != nil {
		t.Fatalf("MarkPendingDeletion: %v", err)
	}
	disabled := ta.registerUser(t, "client-disabled@example.com", "Sup3rSecret!pass", "Caio")
	ta.userRepo.byID[disabled.ID()].IsEnabled = false
	live := ta.registerUser(t, "client-live@example.com", "Sup3rSecret!pass", "Dora")
	public := merchantOAuthClient("cli_public", live.ID())
	public.ClientType, public.ClientSecretHash = oauthclient.TypePublic, ""

	seedInternalClient(t, ta, merchantOAuthClient("cli_leaving", leaving.ID()))
	seedInternalClient(t, ta, merchantOAuthClient("cli_disabled", disabled.ID()))
	seedInternalClient(t, ta, merchantOAuthClient("cli_system", oauthclient.SystemOwnerUserID))
	seedInternalClient(t, ta, public)

	for id, want := range map[string]bool{
		"cli_leaving":  false,
		"cli_disabled": false,
		"cli_public":   false,
		"cli_system":   true,
	} {
		resp := ta.doWithToken(http.MethodGet, internalClientPath+id, nil, tok)
		var body struct {
			Active bool `json:"active"`
		}
		readJSON(t, resp, &body)
		if body.Active != want {
			t.Fatalf("%s: active = %v, want %v", id, body.Active, want)
		}
	}
}

// The reverse gate: holding oauth-client-read must not answer membership or
// enumeration questions just because both routes live under /internal.
func TestTheOAuthClientScopeOpensNoOtherInternalRoute(t *testing.T) {
	a := newInternalMembershipApp(t)
	orgID, ownerID, _ := a.seedOrg(t, "client-scope-sep@example.com")
	tok := a.issueServiceToken(t, []string{scopes.InternalAccountOAuthClientRead})
	for _, path := range []string{
		"/v1.0/internal/organizations/" + orgID + "/members/" + ownerID,
		"/v1.0/internal/users/" + ownerID + "/organizations",
	} {
		if resp := a.do(t, http.MethodGet, path, tok, ""); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s answered the oauth-client-read scope: status %d", path, resp.StatusCode)
		}
	}
}
