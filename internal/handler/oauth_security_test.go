package handler_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"gopkg.aoctech.app/account/internal/apierror"
	"gopkg.aoctech.app/account/internal/cache"
	"gopkg.aoctech.app/account/internal/config"
	"gopkg.aoctech.app/account/internal/crypto"
	apikeyDomain "gopkg.aoctech.app/account/internal/domain/apikey"
	oauthclient "gopkg.aoctech.app/account/internal/domain/oauth/client"
	authcode "gopkg.aoctech.app/account/internal/domain/oauth/code"
	consentDomain "gopkg.aoctech.app/account/internal/domain/oauth/consent"
	sessionDomain "gopkg.aoctech.app/account/internal/domain/session"
	userDomain "gopkg.aoctech.app/account/internal/domain/user"
	"gopkg.aoctech.app/account/internal/handler"
	"gopkg.aoctech.app/account/internal/legal"
	"gopkg.aoctech.app/account/internal/middleware"
)

// memClientRepo is an in-memory oauthclient.Repository for tests.
type memClientRepo struct {
	clients map[string]*oauthclient.OAuthClient
	nextID  int
}

func newMemClientRepo() *memClientRepo {
	return &memClientRepo{clients: map[string]*oauthclient.OAuthClient{}}
}

func (r *memClientRepo) GetByID(_ context.Context, clientID string) (*oauthclient.OAuthClient, error) {
	c, ok := r.clients[clientID]
	if !ok {
		return nil, oauthclient.ErrNotFound
	}
	return c, nil
}

func (r *memClientRepo) Create(_ context.Context, c *oauthclient.OAuthClient) error {
	if c.PK == "" {
		r.nextID++
		c.PK = oauthclient.BuildPK(fmt.Sprintf("client-%d", r.nextID))
	}
	r.clients[c.ID()] = c
	return nil
}

func (r *memClientRepo) ListByOwner(_ context.Context, ownerUserID string) ([]*oauthclient.OAuthClient, error) {
	var result []*oauthclient.OAuthClient
	for _, c := range r.clients {
		if c.OwnerUserID == ownerUserID {
			result = append(result, c)
		}
	}
	return result, nil
}

func (r *memClientRepo) Update(_ context.Context, clientID string, updates map[string]any) error {
	c, ok := r.clients[clientID]
	if !ok {
		return oauthclient.ErrNotFound
	}
	if v, ok := updates["name"].(string); ok {
		c.Name = v
	}
	if v, ok := updates["redirect_uris"].([]string); ok {
		c.RedirectURIs = v
	}
	if v, ok := updates["allowed_scopes"].([]string); ok {
		c.AllowedScopes = v
	}
	if v, ok := updates["audience"].([]string); ok {
		c.Audience = v
	}
	if v, ok := updates["client_secret_hash"].(string); ok {
		c.ClientSecretHash = v
	}
	return nil
}

func (r *memClientRepo) Delete(_ context.Context, clientID string) error {
	delete(r.clients, clientID)
	return nil
}

// memConsentRepo is an in-memory consent.Repository for tests.
type memConsentRepo struct {
	grants map[string]*consentDomain.Grant
}

func newMemConsentRepo() *memConsentRepo {
	return &memConsentRepo{grants: map[string]*consentDomain.Grant{}}
}

func (r *memConsentRepo) Get(_ context.Context, userID, clientID string) (*consentDomain.Grant, error) {
	g, ok := r.grants[userID+"|"+clientID]
	if !ok {
		return nil, consentDomain.ErrNotFound
	}
	return g, nil
}

func (r *memConsentRepo) Put(_ context.Context, g *consentDomain.Grant) error {
	r.grants[g.UserID()+"|"+g.ClientID()] = g
	return nil
}

func (r *memConsentRepo) Delete(_ context.Context, userID, clientID string) error {
	delete(r.grants, userID+"|"+clientID)
	return nil
}

func (r *memConsentRepo) ListByUser(_ context.Context, userID string) ([]*consentDomain.Grant, error) {
	var result []*consentDomain.Grant
	for _, g := range r.grants {
		if g.UserID() == userID {
			result = append(result, g)
		}
	}
	return result, nil
}

// oauthTestApp wires the authorize + token handlers with in-memory backends.
type oauthTestApp struct {
	app        *fiber.App
	clientRepo *memClientRepo
	sessionSvc *sessionDomain.Service
	consentSvc *consentDomain.Service
	userRepo   *memUserRepo
	apiKeySvc  *apikeyDomain.Service
	cache      *cache.Client
	cfg        *config.Config
}

func newOAuthTestApp(t *testing.T) *oauthTestApp {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa key: %v", err)
	}
	cfg := &config.Config{
		Environment: "test", BaseURL: "http://localhost", Audience: "http://localhost",
		RSAPrivateKey: privateKey, PublicKeyKID: "test-kid",
	}
	jwtSvc, err := crypto.NewJWTService(cfg)
	if err != nil {
		t.Fatalf("jwt svc: %v", err)
	}

	inMem := cache.NewInMemory()
	clientRepo := newMemClientRepo()
	codeRepo := authcode.NewRepository(inMem)
	sessionSvc := sessionDomain.NewService(newMemSessionRepo())
	userRepo := newMemUserRepo()
	userSvc := userDomain.NewService(userRepo)

	app := fiber.New(fiber.Config{
		ErrorHandler: func(c fiber.Ctx, err error) error {
			if problem, ok := errors.AsType[*apierror.Problem](err); ok {
				return problem.Send(c)
			}
			return apierror.ServerError(c.Path()).Send(c)
		},
	})
	consentSvc := consentDomain.NewService(newMemConsentRepo())

	v1 := app.Group("/v1.0")
	handler.NewAuthorizeHandler(clientRepo, codeRepo, sessionSvc, consentSvc, userSvc, inMem, "http://app.localhost", cfg.BaseURL, "", nil).Register(v1)
	apiKeySvc := apikeyDomain.NewService(newMemAPIKeyRepo())
	handler.NewTokenHandler(clientRepo, codeRepo, sessionSvc, userSvc, apiKeySvc, newTestCatalogService(), jwtSvc, cfg.BaseURL, cfg, nil).Register(v1)
	v1.Get("/userinfo", middleware.RequireAuth(jwtSvc), handler.NewUserInfoHandler(userSvc).UserInfo)

	return &oauthTestApp{app: app, clientRepo: clientRepo, sessionSvc: sessionSvc, consentSvc: consentSvc, userRepo: userRepo, apiKeySvc: apiKeySvc, cache: inMem, cfg: cfg}
}

func (ta *oauthTestApp) getRedirectless(path string) *http.Response {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	// Test client must NOT follow the 302 so we can inspect the Location header.
	resp, _ := ta.app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
	return resp
}

func (ta *oauthTestApp) postForm(path string, form url.Values) *http.Response {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, _ := ta.app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
	return resp
}

// TestAuthorize_OpenRedirect_Blocked verifies that an unregistered redirect_uri
// combined with a bad response_type does NOT produce a 302 to the attacker URI.
func TestAuthorize_OpenRedirect_Blocked(t *testing.T) {
	ta := newOAuthTestApp(t)
	_ = ta.clientRepo.Create(context.Background(), &oauthclient.OAuthClient{
		PK: oauthclient.BuildPK("app"), ClientType: "public",
		RedirectURIs:  []string{"https://good.example/cb"},
		AllowedScopes: []string{"openid"},
	})

	resp := ta.getRedirectless(
		"/v1.0/authorize?client_id=app&redirect_uri=https://evil.example/steal&response_type=BAD&state=xyz")

	if resp.StatusCode == http.StatusFound {
		t.Fatalf("open redirect: got 302 to %q, expected a 4xx problem", resp.Header.Get("Location"))
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for unregistered redirect_uri, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); strings.Contains(loc, "evil.example") {
		t.Fatalf("must never redirect to unregistered URI, Location=%q", loc)
	}
}

// TestAuthorize_TrustedRedirect_StillRedirectsError verifies that once the
// redirect_uri is registered, OAuth errors are delivered to it as a redirect.
func TestAuthorize_TrustedRedirect_StillRedirectsError(t *testing.T) {
	ta := newOAuthTestApp(t)
	_ = ta.clientRepo.Create(context.Background(), &oauthclient.OAuthClient{
		PK: oauthclient.BuildPK("app"), ClientType: "public",
		RedirectURIs:  []string{"https://good.example/cb"},
		AllowedScopes: []string{"openid"},
	})

	resp := ta.getRedirectless(
		"/v1.0/authorize?client_id=app&redirect_uri=https://good.example/cb&response_type=BAD&state=xyz")

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 to trusted redirect_uri, got %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "https://good.example/cb") || !strings.Contains(loc, "error=unsupported_response_type") {
		t.Fatalf("expected error redirect to trusted URI, Location=%q", loc)
	}
}

// TestSSO_ServerSideExchange_DoesNotInvalidateSession is the regression test for
// the "log in again after dfe login" bug: a confidential client's server-side
// code exchange must not rotate the browser's SSO session token, and must not
// break another client's refresh chain.
func TestSSO_ServerSideExchange_DoesNotInvalidateSession(t *testing.T) {
	ta := newOAuthTestApp(t)
	secretHash, _ := crypto.HashPassword("dfe-secret")
	_ = ta.clientRepo.Create(context.Background(), &oauthclient.OAuthClient{
		PK: oauthclient.BuildPK("dfe"), ClientType: "confidential",
		ClientSecretHash: secretHash,
		RedirectURIs:     []string{"https://dfe.example/cb"},
		AllowedScopes:    []string{"openid", "profile", "email"},
		FirstParty:       true,
	})

	// A logged-in user with a browser SSO session.
	_ = ta.userRepo.Create(context.Background(), &userDomain.User{
		PK: "USER_user-sso", Email: "sso@example.com", EmailVerified: true,
		TOSVersion: legal.CurrentToSVersion, PrivacyVersion: legal.CurrentPrivacyVersion,
	})
	_, ssoToken, err := ta.sessionSvc.Create(context.Background(), "user-sso", "Chrome", "1.2.3.4", "UA", []string{sessionDomain.AMRPassword})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	authorizeWithCookie := func() *http.Response {
		req := httptest.NewRequest(http.MethodGet,
			"/v1.0/authorize?client_id=dfe&redirect_uri=https://dfe.example/cb&response_type=code&scope=openid", nil)
		req.AddCookie(&http.Cookie{Name: "ctech_session", Value: ssoToken})
		resp, _ := ta.app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
		return resp
	}

	// First authorize issues a code.
	resp := authorizeWithCookie()
	loc := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusFound || !strings.HasPrefix(loc, "https://dfe.example/cb?code=") {
		t.Fatalf("expected code redirect, got %d %q", resp.StatusCode, loc)
	}
	code := strings.TrimPrefix(loc, "https://dfe.example/cb?code=")

	// dfe's backend exchanges the code server-side (no browser cookies involved).
	exchResp := ta.postForm("/v1.0/token", url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {"dfe"},
		"client_secret": {"dfe-secret"},
		"redirect_uri":  {"https://dfe.example/cb"},
	})
	if exchResp.StatusCode != http.StatusOK {
		t.Fatalf("code exchange: expected 200, got %d: %s", exchResp.StatusCode, bodyString(exchResp))
	}

	// The browser's SSO cookie must STILL be valid: a second authorize (e.g. the
	// user visiting accounts.aoctech.app) must issue a code, not bounce to login.
	resp = authorizeWithCookie()
	loc = resp.Header.Get("Location")
	if resp.StatusCode != http.StatusFound || !strings.HasPrefix(loc, "https://dfe.example/cb?code=") {
		t.Fatalf("SSO session invalidated by server-side exchange: got %d %q", resp.StatusCode, loc)
	}
}

// TestAuthorize_MaxAgeZero_ForcesReloginOnStaleSession is the regression test
// for the wallet step-up bug: a downstream app's withdrawal flow redirects
// here with max_age=0 to force a fresh MFA proof, but a valid SSO cookie alone
// used to always win and silently re-issue a code — never showing the login
// page at all, so last_mfa_at never refreshed and the wallet 403'd forever.
func TestAuthorize_MaxAgeZero_ForcesReloginOnStaleSession(t *testing.T) {
	ta := newOAuthTestApp(t)
	_ = ta.clientRepo.Create(context.Background(), &oauthclient.OAuthClient{
		PK: oauthclient.BuildPK("wallet"), ClientType: "public",
		RedirectURIs:  []string{"https://wallet.example/cb"},
		AllowedScopes: []string{"openid"},
		FirstParty:    true,
	})
	_ = ta.userRepo.Create(context.Background(), &userDomain.User{
		PK: "USER_user-stale", Email: "stale@example.com", EmailVerified: true,
		TOSVersion: legal.CurrentToSVersion, PrivacyVersion: legal.CurrentPrivacyVersion,
	})
	sess, ssoToken, err := ta.sessionSvc.Create(context.Background(), "user-stale", "Chrome", "1.2.3.4", "UA", []string{sessionDomain.AMRPassword})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	// An old login — old enough that max_age=0 must reject it outright.
	sess.AuthTime = time.Now().UTC().Add(-1 * time.Hour).Unix()

	req := httptest.NewRequest(http.MethodGet,
		"/v1.0/authorize?client_id=wallet&redirect_uri=https://wallet.example/cb&response_type=code&scope=openid&max_age=0&code_challenge=abc&code_challenge_method=S256", nil)
	req.AddCookie(&http.Cookie{Name: "ctech_session", Value: ssoToken})
	resp, _ := ta.app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})

	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		t.Fatalf("expected a redirect, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "http://app.localhost/login") {
		t.Fatalf("expected redirect to login (max_age exceeded), got %q", loc)
	}
}

// TestAuthorize_MaxAgeZero_DoesNotLoopAfterFreshLogin proves max_age=0 is
// self-resolving: right after a (re)login, AuthTime is "now", so the very next
// authorize call with the same max_age=0 must proceed normally — otherwise the
// step-up redirect would bounce forever between /authorize and /login.
func TestAuthorize_MaxAgeZero_DoesNotLoopAfterFreshLogin(t *testing.T) {
	ta := newOAuthTestApp(t)
	_ = ta.clientRepo.Create(context.Background(), &oauthclient.OAuthClient{
		PK: oauthclient.BuildPK("wallet"), ClientType: "public",
		RedirectURIs:  []string{"https://wallet.example/cb"},
		AllowedScopes: []string{"openid"},
		FirstParty:    true,
	})
	_ = ta.userRepo.Create(context.Background(), &userDomain.User{
		PK: "USER_user-fresh", Email: "fresh@example.com", EmailVerified: true,
		TOSVersion: legal.CurrentToSVersion, PrivacyVersion: legal.CurrentPrivacyVersion,
	})
	_, ssoToken, err := ta.sessionSvc.Create(context.Background(), "user-fresh", "Chrome", "1.2.3.4", "UA", []string{sessionDomain.AMRPassword})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet,
		"/v1.0/authorize?client_id=wallet&redirect_uri=https://wallet.example/cb&response_type=code&scope=openid&max_age=0&code_challenge=abc&code_challenge_method=S256", nil)
	req.AddCookie(&http.Cookie{Name: "ctech_session", Value: ssoToken})
	resp, _ := ta.app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})

	loc := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusFound || !strings.HasPrefix(loc, "https://wallet.example/cb?code=") {
		t.Fatalf("expected code redirect right after a fresh login, got %d %q", resp.StatusCode, loc)
	}
}

// TestUserInfo_AccessibleWithDownstreamClientToken is the regression test for
// the "dfe /me returns empty email/name" bug: an access token minted for a
// downstream resource server (aud = the client's own audience, e.g. dfe-api)
// must still be accepted by this IdP's own GET /v1.0/userinfo — otherwise no
// third-party client using audience-scoped tokens can ever fetch the profile.
func TestUserInfo_AccessibleWithDownstreamClientToken(t *testing.T) {
	ta := newOAuthTestApp(t)
	secretHash, _ := crypto.HashPassword("dfe-secret")
	_ = ta.clientRepo.Create(context.Background(), &oauthclient.OAuthClient{
		PK: oauthclient.BuildPK("dfe"), ClientType: "confidential",
		ClientSecretHash: secretHash,
		RedirectURIs:     []string{"https://dfe.example/cb"},
		AllowedScopes:    []string{"openid", "profile", "email"},
		Audience:         []string{"https://dfe-api.example"}, // NOT ta.cfg.Audience
		FirstParty:       true,
	})
	_ = ta.userRepo.Create(context.Background(), &userDomain.User{
		PK: "USER_user-ui", Email: "ui@example.com", FirstName: "Ui", EmailVerified: true,
		TOSVersion: legal.CurrentToSVersion, PrivacyVersion: legal.CurrentPrivacyVersion,
	})
	_, ssoToken, err := ta.sessionSvc.Create(context.Background(), "user-ui", "Chrome", "1.2.3.4", "UA", []string{sessionDomain.AMRPassword})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet,
		"/v1.0/authorize?client_id=dfe&redirect_uri=https://dfe.example/cb&response_type=code&scope=openid+profile+email", nil)
	req.AddCookie(&http.Cookie{Name: "ctech_session", Value: ssoToken})
	resp, _ := ta.app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
	loc := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusFound || !strings.HasPrefix(loc, "https://dfe.example/cb?code=") {
		t.Fatalf("expected code redirect, got %d %q", resp.StatusCode, loc)
	}
	code := strings.TrimPrefix(loc, "https://dfe.example/cb?code=")

	exchResp := ta.postForm("/v1.0/token", url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {"dfe"},
		"client_secret": {"dfe-secret"},
		"redirect_uri":  {"https://dfe.example/cb"},
	})
	if exchResp.StatusCode != http.StatusOK {
		t.Fatalf("code exchange: expected 200, got %d: %s", exchResp.StatusCode, bodyString(exchResp))
	}
	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}
	readJSON(t, exchResp, &tokenResp)

	uiReq := httptest.NewRequest(http.MethodGet, "/v1.0/userinfo", nil)
	uiReq.Header.Set("Authorization", "Bearer "+tokenResp.AccessToken)
	uiResp, _ := ta.app.Test(uiReq, fiber.TestConfig{Timeout: 5 * time.Second})
	if uiResp.StatusCode != http.StatusOK {
		t.Fatalf("userinfo: expected 200, got %d: %s", uiResp.StatusCode, bodyString(uiResp))
	}
	var profile struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	readJSON(t, uiResp, &profile)
	if profile.Email != "ui@example.com" {
		t.Fatalf("expected email in userinfo response, got %+v", profile)
	}
}

// TestConsent_ThirdPartyClient_FullFlow verifies the consent gate: a client
// without first_party is redirected to the consent screen, an approval stores
// the grant and re-runs authorize to a code, and a denial redirects back to the
// client with error=access_denied.
func TestConsent_ThirdPartyClient_FullFlow(t *testing.T) {
	ta := newOAuthTestApp(t)
	_ = ta.clientRepo.Create(context.Background(), &oauthclient.OAuthClient{
		PK: oauthclient.BuildPK("third"), Name: "Third App", ClientType: "public",
		RedirectURIs:  []string{"https://third.example/cb"},
		AllowedScopes: []string{"openid", "dfe:nfes:read"},
	})
	_ = ta.userRepo.Create(context.Background(), &userDomain.User{
		PK: "USER_user-c", Email: "c@example.com", EmailVerified: true,
		TOSVersion: legal.CurrentToSVersion, PrivacyVersion: legal.CurrentPrivacyVersion,
	})
	_, ssoToken, err := ta.sessionSvc.Create(context.Background(), "user-c", "Chrome", "1.2.3.4", "UA", []string{sessionDomain.AMRPassword})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	authorizePath := "/v1.0/authorize?client_id=third&redirect_uri=https://third.example/cb" +
		"&response_type=code&scope=openid+dfe:nfes:read&state=st1&code_challenge=" +
		strings.Repeat("a", 43) + "&code_challenge_method=S256"

	get := func() *http.Response {
		req := httptest.NewRequest(http.MethodGet, authorizePath, nil)
		req.AddCookie(&http.Cookie{Name: "ctech_session", Value: ssoToken})
		resp, _ := ta.app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
		return resp
	}

	// 1. No grant yet → redirect to the frontend consent page, not to the client.
	resp := get()
	loc := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusFound || !strings.HasPrefix(loc, "http://app.localhost/consent?") {
		t.Fatalf("expected consent redirect, got %d %q", resp.StatusCode, loc)
	}
	consentURL, _ := url.Parse(loc)
	reqParam := consentURL.Query().Get("req")
	if reqParam == "" {
		t.Fatal("consent redirect missing req parameter")
	}

	postDecision := func(approved bool) map[string]any {
		body := fmt.Sprintf(`{"req":%q,"approved":%v}`, reqParam, approved)
		req := httptest.NewRequest(http.MethodPost, "/v1.0/authorize/consent", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "ctech_session", Value: ssoToken})
		resp, _ := ta.app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("consent decision: expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
		}
		var out map[string]any
		readJSON(t, resp, &out)
		return out
	}

	// 2. Denial → redirect back to the client with access_denied and state.
	denied := postDecision(false)
	redirectTo, _ := denied["redirect_to"].(string)
	if !strings.HasPrefix(redirectTo, "https://third.example/cb?error=access_denied") ||
		!strings.Contains(redirectTo, "state=st1") {
		t.Fatalf("expected access_denied redirect, got %q", redirectTo)
	}

	// 3. Approval → grant stored, redirect_to points back at /authorize.
	approved := postDecision(true)
	redirectTo, _ = approved["redirect_to"].(string)
	if !strings.Contains(redirectTo, "/v1.0/authorize?") {
		t.Fatalf("expected authorize redirect, got %q", redirectTo)
	}

	// 4. Authorize now issues a code straight to the client.
	resp = get()
	loc = resp.Header.Get("Location")
	if resp.StatusCode != http.StatusFound || !strings.HasPrefix(loc, "https://third.example/cb?code=") {
		t.Fatalf("expected code redirect after consent, got %d %q", resp.StatusCode, loc)
	}
}

// TestConsent_DecisionRequiresSession verifies an unauthenticated decision is rejected.
func TestConsent_DecisionRequiresSession(t *testing.T) {
	ta := newOAuthTestApp(t)
	body := fmt.Sprintf(`{"req":%q,"approved":true}`,
		base64.RawURLEncoding.EncodeToString([]byte("/v1.0/authorize?client_id=x")))
	req := httptest.NewRequest(http.MethodPost, "/v1.0/authorize/consent", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := ta.app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without session, got %d", resp.StatusCode)
	}
}

// TestAPIKeyExchange verifies grant_type=api_key: a valid key yields a scoped
// short-lived access token whose aud includes the services named by the scopes;
// invalid keys are rejected as invalid_grant.
func TestAPIKeyExchange(t *testing.T) {
	ta := newOAuthTestApp(t)
	_, rawKey, err := ta.apiKeySvc.Create(context.Background(), "user-k", "ci key",
		[]string{"dfe:nfes:read", "account:profile:read"}, 0)
	if err != nil {
		t.Fatalf("create key: %v", err)
	}

	resp := ta.postForm("/v1.0/token", url.Values{
		"grant_type": {"api_key"},
		"api_key":    {rawKey},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("exchange: expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Scope       string `json:"scope"`
	}
	readJSON(t, resp, &body)
	if body.AccessToken == "" || body.ExpiresIn != 900 {
		t.Fatalf("unexpected token response: %+v", body)
	}
	if !strings.Contains(body.Scope, "dfe:nfes:read") {
		t.Fatalf("expected key scopes in response, got %q", body.Scope)
	}

	// Decode the JWT payload (no signature check needed here) and assert claims.
	parts := strings.Split(body.AccessToken, ".")
	if len(parts) != 3 {
		t.Fatalf("expected JWT, got %q", body.AccessToken)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decoding payload: %v", err)
	}
	claims := string(payload)
	if !strings.Contains(claims, "https://dfe-api.aoctech.app") {
		t.Fatalf("aud must include dfe audience, claims=%s", claims)
	}
	if !strings.Contains(claims, `"sub":"user-k"`) {
		t.Fatalf("sub must be the key owner, claims=%s", claims)
	}

	// Wrong key → invalid_grant, not a server error.
	resp = ta.postForm("/v1.0/token", url.Values{
		"grant_type": {"api_key"},
		"api_key":    {"not-a-key"},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid key: expected 400 invalid_grant, got %d", resp.StatusCode)
	}
}

// TestRefresh_ConfidentialClient_RequiresSecret verifies the refresh grant
// authenticates confidential clients (a stolen refresh token is not enough).
func TestRefresh_ConfidentialClient_RequiresSecret(t *testing.T) {
	ta := newOAuthTestApp(t)
	secretHash, err := crypto.HashPassword("s3cr3t")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	_ = ta.clientRepo.Create(context.Background(), &oauthclient.OAuthClient{
		PK: oauthclient.BuildPK("svc"), ClientType: "confidential",
		ClientSecretHash: secretHash, AllowedScopes: []string{"openid", "profile", "email"},
	})

	// A valid per-client refresh token exists for a real session.
	sess, _, err := ta.sessionSvc.Create(context.Background(), "user-1", "Chrome", "1.2.3.4", "UA", []string{sessionDomain.AMRPassword})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	rawToken, err := ta.sessionSvc.IssueClientToken(context.Background(), "user-1", sess.ID(), "svc", []string{"openid", "profile", "email"})
	if err != nil {
		t.Fatalf("issue client token: %v", err)
	}

	// Without client_secret → rejected.
	resp := ta.postForm("/v1.0/token", url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {"svc"},
		"refresh_token": {rawToken},
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing client_secret: expected 401, got %d", resp.StatusCode)
	}

	// With correct client_secret → accepted.
	resp = ta.postForm("/v1.0/token", url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {"svc"},
		"refresh_token": {rawToken},
		"client_secret": {"s3cr3t"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("valid client_secret: expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
	}
}

// TestRefresh_SessionExpiredRace_DoesNotBurnRateLimit is the regression test
// for the post-logout /token throttling bug: a per-client refresh token that
// outlives its SSO session (e.g. an in-flight refresh racing a concurrent
// logout) gets "Session no longer exists" (invalid_grant) — but that failure
// is only reachable by possessing a real refresh token hash, never by
// guessing, so it must not consume the same brute-force budget as an actual
// credential-guessing attack. Otherwise a handful of ordinary logout races
// lock the IP out of legitimate /token calls (including a fresh login).
func TestRefresh_SessionExpiredRace_DoesNotBurnRateLimit(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa key: %v", err)
	}
	cfg := &config.Config{
		Environment: "test", BaseURL: "http://localhost", Audience: "http://localhost",
		RSAPrivateKey: privateKey, PublicKeyKID: "test-kid",
	}
	jwtSvc, err := crypto.NewJWTService(cfg)
	if err != nil {
		t.Fatalf("jwt svc: %v", err)
	}

	inMem := cache.NewInMemory()
	clientRepo := newMemClientRepo()
	sessionRepo := newMemSessionRepo()
	sessionSvc := sessionDomain.NewService(sessionRepo)
	userSvc := userDomain.NewService(newMemUserRepo())
	apiKeySvc := apikeyDomain.NewService(newMemAPIKeyRepo())

	app := fiber.New(fiber.Config{
		ErrorHandler: func(c fiber.Ctx, err error) error {
			if problem, ok := errors.AsType[*apierror.Problem](err); ok {
				return problem.Send(c)
			}
			return apierror.ServerError(c.Path()).Send(c)
		},
	})
	v1 := app.Group("/v1.0")
	tokenLimiter := middleware.RateLimit(middleware.RateLimitConfig{
		Cache: inMem, Prefix: "token", Max: middleware.FailedLoginMax,
		Window: middleware.FailedLoginWindow, KeyFunc: func(fiber.Ctx) string { return "1.2.3.4" },
		CountOnlyFailures: true,
	})
	v1.Use("/token", tokenLimiter)
	handler.NewTokenHandler(clientRepo, authcode.NewRepository(inMem), sessionSvc, userSvc, apiKeySvc, newTestCatalogService(), jwtSvc, cfg.BaseURL, cfg, nil).Register(v1)

	_ = clientRepo.Create(context.Background(), &oauthclient.OAuthClient{
		PK: oauthclient.BuildPK("dfe"), ClientType: "public", AllowedScopes: []string{"openid"},
	})

	postRefresh := func(rawToken string) *http.Response {
		req := httptest.NewRequest(http.MethodPost, "/v1.0/token",
			strings.NewReader(url.Values{"grant_type": {"refresh_token"}, "client_id": {"dfe"}, "refresh_token": {rawToken}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, _ := app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
		return resp
	}

	// Well beyond FailedLoginMax (5) — each attempt races a session that's
	// already gone but presents a still-valid refresh token, so none should count.
	for i := 0; i < 10; i++ {
		sess, _, err := sessionSvc.Create(context.Background(), "user-1", "Chrome", "1.2.3.4", "UA", []string{sessionDomain.AMRPassword})
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		rawToken, err := sessionSvc.IssueClientToken(context.Background(), "user-1", sess.ID(), "dfe", []string{"openid"})
		if err != nil {
			t.Fatalf("issue client token: %v", err)
		}
		// Delete only the session (bypassing Revoke, which would also delete the
		// refresh token) to reproduce the exact race: token found, session gone.
		if err := sessionRepo.Delete(context.Background(), "user-1", sess.ID()); err != nil {
			t.Fatalf("delete session: %v", err)
		}

		resp := postRefresh(rawToken)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401 session-expired, got %d: %s", i+1, resp.StatusCode, bodyString(resp))
		}
		if !strings.Contains(bodyString(resp), "session-expired") {
			t.Fatalf("attempt %d: expected session-expired problem type, got %s", i+1, bodyString(resp))
		}
	}

	// A legitimate refresh right after must still succeed — not 429.
	sess, _, err := sessionSvc.Create(context.Background(), "user-1", "Chrome", "1.2.3.4", "UA", []string{sessionDomain.AMRPassword})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	rawToken, err := sessionSvc.IssueClientToken(context.Background(), "user-1", sess.ID(), "dfe", []string{"openid"})
	if err != nil {
		t.Fatalf("issue client token: %v", err)
	}
	resp := postRefresh(rawToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("legitimate refresh after the races: expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
	}
}

// TestRefresh_MissingToken_DoesNotBurnRateLimit is the regression test for the
// silent-refresh-with-no-session case: an SPA on first mount, before any
// ctech_rt cookie exists, sends grant_type=refresh_token with no token. That's
// a client-side race, not a guessed credential, so it must not count against
// the same /token rate-limit bucket that guards client_secret/code guessing.
func TestRefresh_MissingToken_DoesNotBurnRateLimit(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa key: %v", err)
	}
	cfg := &config.Config{
		Environment: "test", BaseURL: "http://localhost", Audience: "http://localhost",
		RSAPrivateKey: privateKey, PublicKeyKID: "test-kid",
	}
	jwtSvc, err := crypto.NewJWTService(cfg)
	if err != nil {
		t.Fatalf("jwt svc: %v", err)
	}

	inMem := cache.NewInMemory()
	clientRepo := newMemClientRepo()
	sessionRepo := newMemSessionRepo()
	sessionSvc := sessionDomain.NewService(sessionRepo)
	userSvc := userDomain.NewService(newMemUserRepo())
	apiKeySvc := apikeyDomain.NewService(newMemAPIKeyRepo())

	app := fiber.New(fiber.Config{
		ErrorHandler: func(c fiber.Ctx, err error) error {
			if problem, ok := errors.AsType[*apierror.Problem](err); ok {
				return problem.Send(c)
			}
			return apierror.ServerError(c.Path()).Send(c)
		},
	})
	v1 := app.Group("/v1.0")
	tokenLimiter := middleware.RateLimit(middleware.RateLimitConfig{
		Cache: inMem, Prefix: "token", Max: middleware.FailedLoginMax,
		Window: middleware.FailedLoginWindow, KeyFunc: func(fiber.Ctx) string { return "1.2.3.4" },
		CountOnlyFailures: true,
	})
	v1.Use("/token", tokenLimiter)
	handler.NewTokenHandler(clientRepo, authcode.NewRepository(inMem), sessionSvc, userSvc, apiKeySvc, newTestCatalogService(), jwtSvc, cfg.BaseURL, cfg, nil).Register(v1)

	_ = clientRepo.Create(context.Background(), &oauthclient.OAuthClient{
		PK: oauthclient.BuildPK("dfe"), ClientType: "public", AllowedScopes: []string{"openid"},
	})

	postRefreshNoToken := func() *http.Response {
		req := httptest.NewRequest(http.MethodPost, "/v1.0/token",
			strings.NewReader(url.Values{"grant_type": {"refresh_token"}, "client_id": {"dfe"}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, _ := app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
		return resp
	}

	// Well beyond FailedLoginMax (5) — none of these should count, since no
	// credential was ever presented to guess.
	for i := 0; i < 10; i++ {
		resp := postRefreshNoToken()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("attempt %d: expected 400 invalid-request, got %d: %s", i+1, resp.StatusCode, bodyString(resp))
		}
		if !strings.Contains(bodyString(resp), "invalid-request") {
			t.Fatalf("attempt %d: expected invalid-request problem type, got %s", i+1, bodyString(resp))
		}
	}

	// A legitimate refresh right after must still succeed — not 429.
	sess, _, err := sessionSvc.Create(context.Background(), "user-1", "Chrome", "1.2.3.4", "UA", []string{sessionDomain.AMRPassword})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	rawToken, err := sessionSvc.IssueClientToken(context.Background(), "user-1", sess.ID(), "dfe", []string{"openid"})
	if err != nil {
		t.Fatalf("issue client token: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1.0/token",
		strings.NewReader(url.Values{"grant_type": {"refresh_token"}, "client_id": {"dfe"}, "refresh_token": {rawToken}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, _ := app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("legitimate refresh after the races: expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
	}
}

// TestRefresh_DoesNotEscalateScopes is the regression test for scope escalation
// on the refresh_token grant: the refresh must be clamped to the scopes granted
// at authorization time, never widened to the client's full allowed-scope set.
// A token granted only "openid" must not gain "kyc" (and the kyc_level claim) on
// refresh just because the client is allowed to request kyc.
func TestRefresh_DoesNotEscalateScopes(t *testing.T) {
	ta := newOAuthTestApp(t)

	if err := ta.clientRepo.Create(context.Background(), &oauthclient.OAuthClient{
		PK:            oauthclient.BuildPK("web"),
		ClientType:    "public",
		RedirectURIs:  []string{"https://app.example/callback"},
		AllowedScopes: []string{"openid", "profile", "email", "kyc"},
	}); err != nil {
		t.Fatalf("seeding client: %v", err)
	}

	sess, _, err := ta.sessionSvc.Create(context.Background(), "user-esc", "Chrome", "1.2.3.4", "UA", []string{sessionDomain.AMRPassword})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	// Granted only "openid", even though the client is allowed far more.
	rawToken, err := ta.sessionSvc.IssueClientToken(context.Background(), "user-esc", sess.ID(), "web", []string{"openid"})
	if err != nil {
		t.Fatalf("issue client token: %v", err)
	}

	resp := ta.postForm("/v1.0/token", url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {"web"},
		"refresh_token": {rawToken},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("refresh: expected 200, got %d: %s", resp.StatusCode, bodyString(resp))
	}
	var body struct {
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
	}
	readJSON(t, resp, &body)

	if body.Scope != "openid" {
		t.Fatalf("refresh widened the grant: expected scope \"openid\", got %q", body.Scope)
	}
	if strings.Contains(body.Scope, "kyc") {
		t.Fatalf("refresh escalated to kyc scope: %q", body.Scope)
	}
	// The access token must not carry a kyc_level claim it was never granted.
	parts := strings.Split(body.AccessToken, ".")
	if len(parts) == 3 {
		if payload, decErr := base64.RawURLEncoding.DecodeString(parts[1]); decErr == nil {
			if strings.Contains(string(payload), "kyc_level") {
				t.Fatalf("refreshed access token leaked kyc_level claim: %s", payload)
			}
		}
	}
}
