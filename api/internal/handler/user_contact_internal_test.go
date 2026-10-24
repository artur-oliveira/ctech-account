package handler_test

import (
	"net/http"
	"strings"
	"testing"

	"gopkg.aoctech.app/account/api/internal/handler"
	"gopkg.aoctech.app/account/api/internal/middleware"
	"gopkg.aoctech.app/account/api/internal/scopes"
	"gopkg.aoctech.app/api-commons/authguard"
)

func newUserContactApp(t *testing.T) *testApp {
	t.Helper()
	ta := newTestApp(t)
	handler.NewUserContactInternalHandler(ta.userSvc).RegisterInternal(ta.app.Group("/v1.0"),
		middleware.RequireAuth(ta.jwtSvc), authguard.RequireInternalScope(scopes.InternalAccountUserContactRead))
	return ta
}

func TestTheUserContactReadNeedsItsOwnInternalScope(t *testing.T) {
	ta := newUserContactApp(t)
	u := ta.registerUser(t, "contact-gate@example.com", "Sup3rSecret!pass", "Ana")
	path := "/v1.0/internal/users/" + u.ID() + "/contact"

	if resp := ta.doWithToken(http.MethodGet, path, nil, ta.issueToken(t, u.ID())); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a user token read a contact: status %d", resp.StatusCode)
	}
	for name, scope := range map[string]string{
		"org-member": scopes.InternalAccountOrgMember, "user-organizations": scopes.InternalAccountUserOrganizations,
		"oauth-client-read": scopes.InternalAccountOAuthClientRead, "kyc": scopes.InternalAccountKYC,
	} {
		if resp := ta.doWithToken(http.MethodGet, path, nil, ta.issueServiceToken(t, []string{scope})); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("the %s scope read a contact: status %d", name, resp.StatusCode)
		}
	}
}

func TestTheUserContactIsOnlyEmailFirstNameAndVerification(t *testing.T) {
	ta := newUserContactApp(t)
	u := ta.registerUser(t, "contact@example.com", "Sup3rSecret!pass", "Ana")
	tok := ta.issueServiceToken(t, []string{scopes.InternalAccountUserContactRead})

	resp := ta.doWithToken(http.MethodGet, "/v1.0/internal/users/"+u.ID()+"/contact", nil, tok)
	body := bodyString(resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"email":"contact@example.com"`) || !strings.Contains(body, `"first_name":"Ana"`) {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
	for _, leak := range []string{"password", "hash", "cpf", "phone", "address", "mfa"} {
		if strings.Contains(strings.ToLower(body), leak) {
			t.Errorf("the contact leaks %q: %s", leak, body)
		}
	}
	if resp := ta.doWithToken(http.MethodGet, "/v1.0/internal/users/nobody/contact", nil, tok); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("an unknown user: status %d, want 404", resp.StatusCode)
	}
}
