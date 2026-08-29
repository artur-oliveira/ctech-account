package handler_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"gopkg.aoctech.app/account/api/internal/apierror"
	oauthclient "gopkg.aoctech.app/account/api/internal/domain/oauth/client"
	"gopkg.aoctech.app/account/api/internal/handler"
)

type handoffTestApp struct {
	app     *fiber.App
	clients *memClientRepo
}

func newHandoffTestApp(t *testing.T) *handoffTestApp {
	t.Helper()
	clients := newMemClientRepo()
	// The product doing the handoff: first-party, one registered origin.
	clients.clients["dfe"] = &oauthclient.OAuthClient{
		PK:           oauthclient.BuildPK("dfe"),
		Name:         "DF-e",
		FirstParty:   true,
		RedirectURIs: []string{"https://dfe.example/callback"},
	}
	// A third party with the same origin registered, to prove that origin
	// alone is not what grants the handoff.
	clients.clients["terceiro"] = &oauthclient.OAuthClient{
		PK:           oauthclient.BuildPK("terceiro"),
		Name:         "Terceiro",
		FirstParty:   false,
		RedirectURIs: []string{"https://dfe.example/callback"},
	}

	app := fiber.New(fiber.Config{
		ErrorHandler: func(c fiber.Ctx, err error) error {
			if problem, ok := errors.AsType[*apierror.Problem](err); ok {
				return problem.Send(c)
			}
			return apierror.ServerError(c.Path()).Send(c)
		},
	})
	app.Use(recover.New())
	orgs := app.Group("/v1.0/organizations")
	// Mounted in main.go's order, and the order is the point: the organization
	// handler owns GET /:id, and Fiber matches in registration order. A literal
	// segment registered after a parameter is swallowed by it.
	handler.NewHandoffHandler(clients).Register(orgs)
	orgs.Get("/:id", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"reached": "the :id route", "id": c.Params("id")})
	})
	return &handoffTestApp{app: app, clients: clients}
}

func (a *handoffTestApp) get(t *testing.T, query string) *http.Response {
	t.Helper()
	resp, err := a.app.Test(httptest.NewRequest(http.MethodGet, "/v1.0/organizations/handoff"+query, nil),
		fiber.TestConfig{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	return resp
}

func TestAHandoffOnARegisteredOriginIsAccepted(t *testing.T) {
	a := newHandoffTestApp(t)
	resp := a.get(t, "?client_id=dfe&return_to=https://dfe.example/empresas/vincular")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, bodyString(resp))
	}
	var got struct {
		ClientName string `json:"client_name"`
		ReturnTo   string `json:"return_to"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	// The name comes from the server. A banner reading a query parameter is a
	// banner anybody can make say "Banco do Brasil".
	if got.ClientName != "DF-e" {
		t.Errorf("client_name = %q, want DF-e", got.ClientName)
	}
	if got.ReturnTo != "https://dfe.example/empresas/vincular" {
		t.Errorf("return_to = %q", got.ReturnTo)
	}
}

// The open-redirect surface. Each of these is a real attempt somebody makes.
func TestAHandoffOffTheRegisteredOriginIsRefused(t *testing.T) {
	a := newHandoffTestApp(t)
	for _, returnTo := range []string{
		"http://dfe.example/callback",           // downgraded scheme
		"https://outro.example/callback",        // another host
		"https://dfe.example.evil.com/callback", // lookalike suffix
		"https://evil.com/https://dfe.example",  // the host in the path
		"https://dfe.example:8443/callback",     // another port
		"//dfe.example/callback",                // scheme-relative
		"/empresas/vincular",                    // relative
		"javascript:alert(1)",
	} {
		resp := a.get(t, "?client_id=dfe&return_to="+returnTo)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%q: status = %d, want 422", returnTo, resp.StatusCode)
		}
	}
}

// FirstParty is the gate, not the origin. A third party with the very same
// registered origin is still refused.
func TestAThirdPartyClientCannotHandOff(t *testing.T) {
	a := newHandoffTestApp(t)
	resp := a.get(t, "?client_id=terceiro&return_to=https://dfe.example/callback")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", resp.StatusCode, bodyString(resp))
	}
}

// Four causes, one answer. Telling them apart would make this route a probe for
// which client ids are real.
func TestEveryHandoffRefusalLooksTheSame(t *testing.T) {
	a := newHandoffTestApp(t)
	queries := []string{
		"?client_id=nao_existe&return_to=https://dfe.example/callback", // unknown client
		"?client_id=terceiro&return_to=https://dfe.example/callback",   // third party
		"?client_id=dfe&return_to=https://evil.example/x",              // unregistered origin
		"?client_id=dfe&return_to=%3A%3Anot%20a%20url",                 // malformed
		"?client_id=dfe", // missing return_to
		"?return_to=https://dfe.example/callback", // missing client_id
	}
	var first apierror.Problem
	for i, q := range queries {
		resp := a.get(t, q)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("%q: status = %d, want 422", q, resp.StatusCode)
		}
		p := problemOf(t, resp)
		if i == 0 {
			first = p
			if !strings.HasSuffix(p.Type, "/organization-handoff-invalid") {
				t.Fatalf("type = %q, want the handoff type", p.Type)
			}
			continue
		}
		if p.Type != first.Type || p.Detail != first.Detail {
			t.Errorf("%q answers %q/%q; the first answers %q/%q — the two are distinguishable",
				q, p.Type, p.Detail, first.Type, first.Detail)
		}
	}
}

// A fragment never reaches the server, so it cannot be part of what was
// validated. Echoing one back would hand the product a value this route did not
// check — and the screen redirects to the echoed value.
func TestTheEchoedReturnToDropsAFragment(t *testing.T) {
	a := newHandoffTestApp(t)
	resp := a.get(t, "?client_id=dfe&return_to=https://dfe.example/vincular%23/algo")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, bodyString(resp))
	}
	var got struct {
		ReturnTo string `json:"return_to"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if strings.Contains(got.ReturnTo, "#") {
		t.Errorf("return_to = %q, want the fragment gone", got.ReturnTo)
	}
}

// The state is opaque and capped. It is never parsed, so size is the only thing
// worth bounding — and an unbounded echo is an amplification primitive.
func TestAnOverlongStateIsRefused(t *testing.T) {
	a := newHandoffTestApp(t)
	long := strings.Repeat("a", 513)
	resp := a.get(t, "?client_id=dfe&return_to=https://dfe.example/callback&state="+long)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
}

// No token, ever, on the redirect. This route answers only the two things the
// screen needs, and a regression that added a credential here would be silent.
func TestTheHandoffResponseCarriesNothingButTheNameAndTheURL(t *testing.T) {
	a := newHandoffTestApp(t)
	resp := a.get(t, "?client_id=dfe&return_to=https://dfe.example/callback")
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("body has %d fields (%v), want exactly client_name and return_to", len(got), got)
	}
	for _, key := range []string{"client_name", "return_to"} {
		if _, ok := got[key]; !ok {
			t.Errorf("missing %q", key)
		}
	}
}

// The route order regression. /handoff is a literal segment on a group whose
// other GET is /:id — registered second, it answers as an organization lookup
// for an organization called "handoff", which is a 403 nobody can debug.
func TestHandoffIsARouteAndNotAnOrganizationID(t *testing.T) {
	a := newHandoffTestApp(t)
	resp := a.get(t, "?client_id=dfe&return_to=https://dfe.example/callback")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, bodyString(resp))
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if _, wrong := got["reached"]; wrong {
		t.Fatalf("the :id route answered: %v", got)
	}
}
