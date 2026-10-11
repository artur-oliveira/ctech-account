package handler_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"gopkg.aoctech.app/account/api/internal/crypto"
	oauthclient "gopkg.aoctech.app/account/api/internal/domain/oauth/client"
	userDomain "gopkg.aoctech.app/account/api/internal/domain/user"
)

// seedDeveloperClient stores a confidential, NOT first-party client owned by
// ownerID, as the developer page creates it, and returns its secret.
func seedDeveloperClient(t *testing.T, ta *oauthTestApp, id, ownerID string, allowed []string) string {
	t.Helper()
	hash, err := crypto.HashPassword(m2mSecret)
	if err != nil {
		t.Fatal(err)
	}
	if err := ta.clientRepo.Create(context.Background(), &oauthclient.OAuthClient{
		PK: oauthclient.BuildPK(id), Name: id, ClientType: oauthclient.TypeConfidential,
		ClientSecretHash: hash, AllowedScopes: allowed, OwnerUserID: ownerID,
	}); err != nil {
		t.Fatal(err)
	}
	return m2mSecret
}

func seedOwner(t *testing.T, ta *oauthTestApp, email string) *userDomain.User {
	t.Helper()
	u := &userDomain.User{Email: email, IsEnabled: true, EmailVerified: true}
	if err := ta.userRepo.Create(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	return u
}

func assertOAuthError(t *testing.T, resp *http.Response, status int, code, mentions string) {
	t.Helper()
	if resp.StatusCode != status {
		t.Fatalf("status = %d, want %d: %s", resp.StatusCode, status, bodyString(resp))
	}
	var body map[string]any
	readJSON(t, resp, &body)
	if body["error"] != code {
		t.Fatalf("error = %v, want %s (body %v)", body["error"], code, body)
	}
	if mentions != "" && !strings.Contains(body["detail"].(string), mentions) {
		t.Fatalf("detail %q does not name %q", body["detail"], mentions)
	}
}

func TestClientCredentialsIssuesATokenToADeveloperClientWithOnlyM2MScopes(t *testing.T) {
	ta := newOAuthTestApp(t)
	owner := seedOwner(t, ta, "merchant@example.com")
	secret := seedDeveloperClient(t, ta, "cli_merchant", owner.ID(), []string{"billing:invoices:write", "billing:customers:read"})

	resp := ta.postForm("/v1.0/token", clientCredentialsForm("cli_merchant", secret, "billing:invoices:write"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, bodyString(resp))
	}
	var body map[string]any
	readJSON(t, resp, &body)
	if body["scope"] != "billing:invoices:write" {
		t.Fatalf("scope = %v", body["scope"])
	}
	if _, hasRefresh := body["refresh_token"]; hasRefresh {
		t.Fatal("client_credentials must not issue a refresh token")
	}
	claims := decodeJWTPayload(t, body["access_token"].(string))
	if claims["sub"] != "cli_merchant" || claims["azp"] != "cli_merchant" || claims["sid"] != "" {
		t.Fatalf("sub/azp/sid = %v/%v/%v", claims["sub"], claims["azp"], claims["sid"])
	}
	auds, _ := claims["aud"].([]any)
	found := false
	for _, a := range auds {
		found = found || a == "https://billing.example.test"
	}
	if !found {
		t.Fatalf("aud %v lacks billing's audience", claims["aud"])
	}
}

// One user scope on the client refuses the grant, even when only m2m scopes
// are requested: one client for sign-in, another for machine access.
func TestClientCredentialsRefusesADeveloperClientHoldingAUserScope(t *testing.T) {
	ta := newOAuthTestApp(t)
	owner := seedOwner(t, ta, "mixed@example.com")
	secret := seedDeveloperClient(t, ta, "cli_mixed", owner.ID(), []string{"billing:invoices:write", "billing:my-invoices:read"})

	resp := ta.postForm("/v1.0/token", clientCredentialsForm("cli_mixed", secret, "billing:invoices:write"))
	assertOAuthError(t, resp, http.StatusForbidden, "unauthorized_client", "billing:my-invoices:read")
}

// Refuse, never clamp: a developer client asking for a scope it does not hold
// gets no token at all.
func TestClientCredentialsRefusesADeveloperClientAskingForAnUngrantedScope(t *testing.T) {
	ta := newOAuthTestApp(t)
	owner := seedOwner(t, ta, "ask@example.com")
	secret := seedDeveloperClient(t, ta, "cli_ask", owner.ID(), []string{"billing:invoices:write"})

	resp := ta.postForm("/v1.0/token", clientCredentialsForm("cli_ask", secret, "billing:invoices:write billing:customers:write"))
	assertOAuthError(t, resp, http.StatusBadRequest, "invalid_scope", "billing:customers:write")
}

func TestClientCredentialsNeverGivesADeveloperClientAnInternalScope(t *testing.T) {
	ta := newOAuthTestApp(t)
	owner := seedOwner(t, ta, "internal@example.com")
	// Not creatable through self-service (ValidateGrantable skips internal
	// services), but if a row ever held one the grant must still refuse.
	secret := seedDeveloperClient(t, ta, "cli_internal", owner.ID(), []string{"billing:invoices:write", "internal:account:kyc"})

	resp := ta.postForm("/v1.0/token", clientCredentialsForm("cli_internal", secret, "internal:account:kyc"))
	assertOAuthError(t, resp, http.StatusForbidden, "unauthorized_client", "internal:account:kyc")
}

func TestClientCredentialsRefusesADeveloperClientWhoseOwnerIsNotActive(t *testing.T) {
	ta := newOAuthTestApp(t)
	locked := seedOwner(t, ta, "locked@example.com")
	locked.DeletionState = userDomain.DeletionStatePending
	disabled := seedOwner(t, ta, "disabled@example.com")
	disabled.IsEnabled = false

	for id, owner := range map[string]string{
		"cli_locked":   locked.ID(),
		"cli_disabled": disabled.ID(),
		"cli_orphan":   "no-such-user",
	} {
		secret := seedDeveloperClient(t, ta, id, owner, []string{"billing:invoices:write"})
		resp := ta.postForm("/v1.0/token", clientCredentialsForm(id, secret, "billing:invoices:write"))
		assertOAuthError(t, resp, http.StatusForbidden, "unauthorized_client", "not active")
	}
}

// The secret is checked before anything about scopes or owners is revealed.
func TestClientCredentialsChecksADeveloperClientsSecretFirst(t *testing.T) {
	ta := newOAuthTestApp(t)
	owner := seedOwner(t, ta, "secret@example.com")
	seedDeveloperClient(t, ta, "cli_secret", owner.ID(), []string{"billing:my-invoices:read"})

	resp := ta.postForm("/v1.0/token", clientCredentialsForm("cli_secret", "wrong-secret", "billing:my-invoices:read"))
	assertOAuthError(t, resp, http.StatusUnauthorized, "invalid_client", "")
}

// First-party clients keep today's behaviour: no m2m requirement, internal
// scopes allowed, unrequested-but-unheld scopes clamped away silently.
func TestClientCredentialsFirstPartyIsUnchanged(t *testing.T) {
	ta := newOAuthTestApp(t)
	secret := seedM2MClient(t, ta.clientRepo, "wallet", "confidential", true, []string{"internal:account:kyc", "billing:my-invoices:read"})

	resp := ta.postForm("/v1.0/token", clientCredentialsForm("wallet", secret, "internal:account:kyc billing:my-invoices:read billing:customers:write"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, bodyString(resp))
	}
	var body map[string]any
	readJSON(t, resp, &body)
	if body["scope"] != "internal:account:kyc billing:my-invoices:read" {
		t.Fatalf("scope = %v, want the held scopes clamped as before", body["scope"])
	}
}
