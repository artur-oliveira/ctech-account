package handler_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/cache"
	"gopkg.aoctech.app/account/api/internal/config"
	"gopkg.aoctech.app/account/api/internal/crypto"
	apikeyDomain "gopkg.aoctech.app/account/api/internal/domain/apikey"
	"gopkg.aoctech.app/account/api/internal/domain/audit"
	kycDomain "gopkg.aoctech.app/account/api/internal/domain/kyc"
	passKeyDomain "gopkg.aoctech.app/account/api/internal/domain/mfa/passkey"
	"gopkg.aoctech.app/account/api/internal/domain/mfa/totp"
	oauthclientDomain "gopkg.aoctech.app/account/api/internal/domain/oauth/client"
	consentDomain "gopkg.aoctech.app/account/api/internal/domain/oauth/consent"
	riskDomain "gopkg.aoctech.app/account/api/internal/domain/risk"
	sessionDomain "gopkg.aoctech.app/account/api/internal/domain/session"
	supportDomain "gopkg.aoctech.app/account/api/internal/domain/support"
	userDomain "gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/account/api/internal/handler"
	"gopkg.aoctech.app/account/api/internal/keystore"
	"gopkg.aoctech.app/account/api/internal/middleware"
	scopesPkg "gopkg.aoctech.app/account/api/internal/scopes"
	"gopkg.aoctech.app/account/api/internal/storage"
	"gopkg.aoctech.app/account/api/internal/turnstile"
)

// noopTOTPService implements both TOTPService and TOTPManagementService.
// All methods return errors — TOTP is not configured.
type noopTOTPService struct{}

func (n *noopTOTPService) Get(_ context.Context, _ string) (*totp.TOTPSecret, error) {
	return nil, totp.ErrNotFound
}
func (n *noopTOTPService) Validate(_ context.Context, _, _ string) (bool, error) {
	return false, errors.New("totp not configured")
}
func (n *noopTOTPService) Generate(_ context.Context, _, _, _ string) (*totp.TOTPSecret, string, error) {
	return nil, "", errors.New("totp not configured")
}
func (n *noopTOTPService) Verify(_ context.Context, _, _ string) ([]string, error) {
	return nil, errors.New("totp not configured")
}
func (n *noopTOTPService) Remove(_ context.Context, _ string) error {
	return errors.New("totp not configured")
}
func (n *noopTOTPService) RegenerateBackupCodes(_ context.Context, _ string) ([]string, error) {
	return nil, errors.New("totp not configured")
}

// memAuditRepo is an in-memory audit.Repository with real limit/cursor
// semantics (cursor = sk of the last returned event, base64url like prod).
type memAuditRepo struct {
	events []*audit.Event
}

func (m *memAuditRepo) Put(_ context.Context, e *audit.Event) error {
	m.events = append(m.events, e)
	return nil
}

func (m *memAuditRepo) QueryByUser(_ context.Context, userID, cursor string, limit int32) ([]*audit.Event, string, error) {
	var mine []*audit.Event
	for _, e := range m.events {
		if e.PK == audit.BuildPK(userID) {
			mine = append(mine, e)
		}
	}
	// newest first
	sort.Slice(mine, func(i, j int) bool { return mine[i].SK > mine[j].SK })

	start := 0
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return nil, "", err
		}
		for i, e := range mine {
			if e.SK == string(raw) {
				start = i + 1
				break
			}
		}
	}
	end := start + int(limit)
	if end > len(mine) {
		end = len(mine)
	}
	page := mine[start:end]
	next := ""
	if end < len(mine) && len(page) > 0 {
		next = base64.RawURLEncoding.EncodeToString([]byte(page[len(page)-1].SK))
	}
	return page, next, nil
}

// testApp builds a Fiber app wired with in-memory repositories — no real AWS required.
type testApp struct {
	app          *fiber.App
	userSvc      *userDomain.Service
	userRepo     *memUserRepo
	sessionSvc   *sessionDomain.Service
	apiKeySvc    *apikeyDomain.Service
	auditSvc     *audit.Service
	auditRepo    *memAuditRepo
	jwtSvc       *crypto.JWTService
	cfg          *config.Config
	socialCache  *cache.Client
	clientRepo   *memClientRepo
	kycPresigner *memPresigner
	kycSvc       *kycDomain.Service
	passkeyRepo  *memPasskeyRepo
	supportSvc   *supportDomain.Service
	supportRepo  *mockSupportRepo
}

func newTestApp(t *testing.T) *testApp {
	t.Helper()
	return newTestAppWithTOTP(t, &noopTOTPService{})
}

// totpFullService is the union of TOTPService and TOTPManagementService used
// by the test app so a single stub can drive both the auth and MFA handlers.
type totpFullService interface {
	handler.TOTPService
	handler.TOTPManagementService
}

func newTestAppWithTOTP(t *testing.T, noop totpFullService) *testApp {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}

	cfg := &config.Config{
		Environment:   "test",
		BaseURL:       "http://localhost",
		Audience:      "http://localhost",
		TOTPIssuer:    "http://localhost",
		Port:          "8000",
		SigningKey:    privateKey,
		SigningKeyAlg: keystore.AlgRS256,
		PublicKeyKID:  "test-kid",
		RPID:          "localhost",
		RPOrigins:     []string{"http://localhost"},
		SelfClientID:  "test-client",
	}

	jwtSvc, err := crypto.NewJWTService(cfg)
	if err != nil {
		t.Fatalf("creating JWT service: %v", err)
	}

	// Disabled cache — no Valkey connection needed in tests.
	disabledCache, _ := cache.New("")

	userRepo := newMemUserRepo()
	sessionRepo := newMemSessionRepo()
	apikeyRepo := newMemAPIKeyRepo()
	passkeyRepo := newMemPasskeyRepo()

	userSvc := userDomain.NewService(userRepo)
	sessionSvc := sessionDomain.NewService(sessionRepo)
	apiKeySvc := apikeyDomain.NewService(apikeyRepo)
	auditRepo := &memAuditRepo{}
	auditSvc := audit.NewService(auditRepo)
	kycPresigner := newMemPresigner()
	kycSvc := kycDomain.NewService(newMemKYCRepo(userRepo), kycPresigner, riskDomain.NoopEvaluator{})
	supportRepo := newMockSupportRepo()
	supportSvc := supportDomain.NewService(supportRepo)

	// WebAuthn instance for tests — uses localhost as RPID/origin.
	wa, err := webauthn.New(&webauthn.Config{
		RPID:          cfg.RPID,
		RPDisplayName: "ctech-account test",
		RPOrigins:     cfg.RPOrigins,
	})
	if err != nil {
		t.Fatalf("creating webauthn: %v", err)
	}
	passkeyCache := cache.NewInMemory()
	passkeySvc := passKeyDomain.NewService(wa, passkeyRepo, passkeyCache)

	app := fiber.New(fiber.Config{
		ErrorHandler: func(c fiber.Ctx, err error) error {
			if problem, ok := errors.AsType[*apierror.Problem](err); ok {
				return problem.Send(c)
			}
			if fiberErr, ok := errors.AsType[*fiber.Error](err); ok {
				return apierror.NewFromFiber(fiberErr, c.Path()).Send(c)
			}
			return apierror.ServerError(c.Path()).Send(c)
		},
	})
	app.Use(recover.New())

	sharedClientRepo := newMemClientRepo()

	v1 := app.Group("/v1.0")
	handler.NewAuthHandler(userSvc, sessionSvc, noop, sharedClientRepo, disabledCache, cfg, nil, auditSvc).Register(v1)
	handler.NewPasskeyHandler(passkeySvc, userSvc, sessionSvc, noop, disabledCache, cfg, auditSvc, nil).RegisterAuth(v1.Group("/auth"))
	v1.Get("/userinfo", middleware.RequireAuth(jwtSvc), handler.NewUserInfoHandler(userSvc).UserInfo)
	handler.NewStepUpHandler(sessionSvc, noop, passkeySvc, disabledCache, auditSvc).Register(v1, middleware.RequireAuth(jwtSvc), middleware.RequireClientID(cfg.SelfClientID))

	account := v1.Group("/account", middleware.RequireAuth(jwtSvc))
	stepUp := middleware.RequireRecentMFA(middleware.StepUpMaxAge)
	handler.NewProfileHandler(userSvc, sessionSvc, auditSvc).Register(account, stepUp)
	handler.NewSessionsHandler(sessionSvc, auditSvc).Register(account)
	handler.NewAPIKeysHandler(apiKeySvc, newTestCatalogService(), auditSvc).Register(account, stepUp)
	handler.NewOAuthClientsHandler(oauthclientDomain.NewService(sharedClientRepo, newTestCatalogService()), auditSvc).Register(account, stepUp)
	handler.NewConsentsHandler(consentDomain.NewService(newMemConsentRepo()), sharedClientRepo, auditSvc).Register(account)
	handler.NewMFAHandler(noop, userSvc, cfg, auditSvc).Register(account, stepUp)
	handler.NewActivityHandler(auditSvc).Register(account)
	handler.NewPasskeyHandler(passkeySvc, userSvc, sessionSvc, noop, disabledCache, cfg, auditSvc, nil).RegisterManagement(account, stepUp)
	handler.NewTermsHandler(userSvc, auditSvc).Register(account)
	supportH := handler.NewSupportHandler(supportSvc, userSvc, turnstile.New("", cfg.AppURL), nil, cfg.AppURL)
	supportH.Register(v1.Group("", middleware.OptionalAuth(jwtSvc)))
	supportH.RegisterAccount(account)
	handler.NewSupportAdminHandler(supportSvc, userSvc, nil, cfg.AppURL).Register(v1.Group("/admin", middleware.RequireAuth(jwtSvc), middleware.RequireClientID(cfg.SelfClientID), middleware.RequireSupportRole(userSvc, userDomain.SupportRoleAgent)))
	kycH := handler.NewKYCHandler(kycSvc, auditSvc)
	kycH.Register(account, stepUp)
	handler.NewKYCAdminHandler(kycSvc, auditSvc, userSvc).Register(v1.Group("/admin/kyc", middleware.RequireAuth(jwtSvc), middleware.RequireClientID(cfg.SelfClientID), middleware.RequireSupportRole(userSvc, userDomain.SupportRoleManager)))
	kycH.RegisterInternalGet(v1, middleware.RequireAuth(jwtSvc), middleware.RequireInternalScope(scopesPkg.InternalWalletConfirmDeposit))

	handler.NewWellKnownHandler(jwtSvc, cfg.BaseURL, cfg.AppURL, cfg.Audience).Register(app)

	socialCache := cache.NewInMemory()
	handler.NewSocialHandler(userSvc, sessionSvc, socialCache, cfg, auditSvc, nil).Register(v1)

	return &testApp{
		app:          app,
		userSvc:      userSvc,
		userRepo:     userRepo,
		sessionSvc:   sessionSvc,
		apiKeySvc:    apiKeySvc,
		auditSvc:     auditSvc,
		auditRepo:    auditRepo,
		jwtSvc:       jwtSvc,
		cfg:          cfg,
		socialCache:  socialCache,
		clientRepo:   sharedClientRepo,
		kycPresigner: kycPresigner,
		kycSvc:       kycSvc,
		passkeyRepo:  passkeyRepo,
		supportSvc:   supportSvc,
		supportRepo:  supportRepo,
	}
}

// memKYCRepo implements kyc.Repository over the shared memUserRepo store with
// real CPF-uniqueness semantics (mirrors the CPF_{cpf} conditional item).
type memKYCRepo struct {
	users   *memUserRepo
	cpfs    map[string]string // cpf -> userID
	pending map[string]*kycDomain.PendingDocument
}

func newMemKYCRepo(users *memUserRepo) *memKYCRepo {
	return &memKYCRepo{
		users:   users,
		cpfs:    map[string]string{},
		pending: map[string]*kycDomain.PendingDocument{},
	}
}

func (m *memKYCRepo) GetUser(ctx context.Context, userID string) (*userDomain.User, error) {
	return m.users.GetByID(ctx, userID)
}

func (m *memKYCRepo) SavePendingDocument(_ context.Context, userID, documentID, docType, contentType string) error {
	m.pending[documentID] = &kycDomain.PendingDocument{UserID: userID, Type: docType, ContentType: contentType}
	return nil
}

func (m *memKYCRepo) GetPendingDocument(_ context.Context, documentID string) (*kycDomain.PendingDocument, error) {
	return m.pending[documentID], nil
}

func (m *memKYCRepo) DeletePendingDocument(_ context.Context, documentID string) error {
	delete(m.pending, documentID)
	return nil
}

func (m *memKYCRepo) SaveBasicSubmission(_ context.Context, userID string, rec kycDomain.BasicRecord, oldCPF string) error {
	if owner, taken := m.cpfs[rec.CPF]; taken && owner != userID {
		return kycDomain.ErrCPFConflict
	}
	u, ok := m.users.byID[userID]
	if !ok {
		return userDomain.ErrNotFound
	}
	if oldCPF != "" && oldCPF != rec.CPF {
		delete(m.cpfs, oldCPF)
	}
	m.cpfs[rec.CPF] = userID

	u.CPF, u.LegalName, u.BirthDate, u.PhoneNumber = rec.CPF, rec.LegalName, rec.BirthDate, rec.PhoneNumber
	u.Address = rec.Address
	u.KYCLevel, u.KYCStatus = kycDomain.LevelBasic, kycDomain.StatusVerified
	u.KYCSubmittedAt = rec.SubmittedAt
	u.KYCRejectionReason, u.PhoneVerifiedAt, u.KYCBasicVerifiedAt = "", "", rec.SubmittedAt
	return nil
}

func (m *memKYCRepo) AddDocument(_ context.Context, userID string, doc kycDomain.Document) error {
	u, ok := m.users.byID[userID]
	if !ok {
		return userDomain.ErrNotFound
	}
	u.KYCDocuments = append(u.KYCDocuments, doc)
	return nil
}

func (m *memKYCRepo) SaveEnhancedSubmission(_ context.Context, userID, submittedAt, expiresAt string) error {
	u, ok := m.users.byID[userID]
	if !ok {
		return userDomain.ErrNotFound
	}
	u.KYCLevel, u.KYCStatus = kycDomain.LevelEnhanced, kycDomain.StatusPending
	u.KYCSubmittedAt, u.KYCExpiresAt, u.KYCRejectionReason = submittedAt, expiresAt, ""
	return nil
}

func (m *memKYCRepo) MarkVerified(_ context.Context, userID, verifiedAt string, actor kycDomain.ReviewActor) error {
	u, ok := m.users.byID[userID]
	if !ok {
		return userDomain.ErrNotFound
	}
	u.KYCStatus, u.KYCVerifiedAt, u.KYCRejectionReason = kycDomain.StatusVerified, verifiedAt, ""
	u.KYCReviewedAt, u.KYCReviewedBy, u.KYCReviewedByName, u.KYCReviewDecision = verifiedAt, actor.ID, actor.Name, kycDomain.DecisionApprove
	return nil
}

func (m *memKYCRepo) MarkRejected(_ context.Context, userID, reasonCode, details, reviewedAt string, actor kycDomain.ReviewActor) error {
	u, ok := m.users.byID[userID]
	if !ok {
		return userDomain.ErrNotFound
	}
	u.KYCStatus, u.KYCRejectionCode, u.KYCRejectionReason = kycDomain.StatusRejected, reasonCode, details
	u.KYCReviewedAt, u.KYCReviewedBy, u.KYCReviewedByName, u.KYCReviewDecision = reviewedAt, actor.ID, actor.Name, kycDomain.DecisionReject
	u.KYCDocuments = nil
	return nil
}

func (m *memKYCRepo) SaveRiskAssessment(_ context.Context, userID string, a riskDomain.Assessment) error {
	u, ok := m.users.byID[userID]
	if !ok {
		return userDomain.ErrNotFound
	}
	u.KYCRiskScore, u.KYCRiskEvaluatedAt = a.Score, a.EvaluatedAt
	return nil
}

func (m *memKYCRepo) ListPendingKYC(_ context.Context) ([]*userDomain.User, error) {
	var out []*userDomain.User
	for _, u := range m.users.byID {
		if u.KYCLevel == kycDomain.LevelEnhanced && u.KYCStatus == kycDomain.StatusPending {
			cp := *u
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (m *memKYCRepo) ListKYCReviews(_ context.Context, queue string) ([]*userDomain.User, error) {
	var out []*userDomain.User
	for _, u := range m.users.byID {
		pending := u.KYCLevel == kycDomain.LevelEnhanced && u.KYCStatus == kycDomain.StatusPending
		completed := u.KYCLevel == kycDomain.LevelEnhanced && (u.KYCStatus == kycDomain.StatusVerified || u.KYCStatus == kycDomain.StatusRejected)
		if (queue == kycDomain.ReviewQueuePending && pending) || (queue == kycDomain.ReviewQueueCompleted && completed) {
			cp := *u
			out = append(out, &cp)
		}
	}
	return out, nil
}

// memPresigner stands in for S3 in handler tests. putObject simulates the
// browser having uploaded to the presigned URL.
type memPresigner struct {
	objects map[string]int64
}

func newMemPresigner() *memPresigner {
	return &memPresigner{objects: map[string]int64{}}
}

func (p *memPresigner) PresignPut(_ context.Context, key, _ string, _ time.Duration) (string, error) {
	return "https://s3.test/" + key + "?sig=put", nil
}

func (p *memPresigner) PresignGet(_ context.Context, key string, _ time.Duration) (string, error) {
	return "https://s3.test/" + key + "?sig=get", nil
}

func (p *memPresigner) Size(_ context.Context, key string) (int64, error) {
	size, ok := p.objects[key]
	if !ok {
		return 0, storage.ErrNotFound
	}
	return size, nil
}

func (p *memPresigner) putObject(key string, size int64) { p.objects[key] = size }

// issueMachineToken mints a client_credentials-style token: sub = client_id,
// empty sid, given scopes, no step-up claims.
func (ta *testApp) issueMachineToken(t *testing.T, clientID string, scopes []string) string {
	t.Helper()
	token, err := ta.jwtSvc.SignAccessToken(clientID, "", clientID, scopes, "http://localhost", []string{"http://localhost"}, 0, 0, nil, "")
	if err != nil {
		t.Fatalf("issuing machine token: %v", err)
	}
	return token
}

// issueStaleToken mints a user token without fresh MFA proof (fails step-up).
func (ta *testApp) issueStaleToken(t *testing.T, userID string) string {
	t.Helper()
	token, err := ta.jwtSvc.SignAccessToken(userID, "sess-test", "test-client", testAccountScopes(), "http://localhost", []string{"http://localhost"}, time.Now().Unix(), 0, nil, "")
	if err != nil {
		t.Fatalf("issuing stale token: %v", err)
	}
	return token
}

func (ta *testApp) do(method, path string, body any, headers ...map[string]string) *http.Response {
	var bodyReader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		bodyReader = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, bodyReader)
	req.Header.Set("Content-Type", "application/json")
	for _, h := range headers {
		for k, v := range h {
			req.Header.Set(k, v)
		}
	}
	resp, _ := ta.app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
	return resp
}

func (ta *testApp) doWithToken(method, path string, body any, token string) *http.Response {
	return ta.do(method, path, body, map[string]string{"Authorization": "Bearer " + token})
}

func (ta *testApp) issueToken(t *testing.T, userID string) string {
	t.Helper()
	token, err := ta.jwtSvc.SignAccessToken(userID, "sess-test", "test-client", testAccountScopes(), "http://localhost", []string{"http://localhost"}, time.Now().Unix(), time.Now().Unix(), []string{sessionDomain.AMRPassword, sessionDomain.AMRTOTP}, "")
	if err != nil {
		t.Fatalf("issuing token: %v", err)
	}
	return token
}

func testAccountScopes() []string {
	return append([]string{scopesPkg.OpenID, scopesPkg.Profile, scopesPkg.Email}, scopesPkg.AccountUserScopes()...)
}

// issueTokenWithScopes mirrors issueToken but accepts an explicit scope
// slice, for tests exercising scope-gated claims (e.g. the kyc scope).
func (ta *testApp) issueTokenWithScopes(t *testing.T, userID string, scopes []string) string {
	t.Helper()
	token, err := ta.jwtSvc.SignAccessToken(userID, "sess-test", "test-client", scopes, "http://localhost", []string{"http://localhost"}, time.Now().Unix(), time.Now().Unix(), []string{sessionDomain.AMRPassword, sessionDomain.AMRTOTP}, "")
	if err != nil {
		t.Fatalf("issuing token: %v", err)
	}
	return token
}

// issueServiceToken mints a client-credentials token: scopes, and no session.
//
// The empty session id is the point. RequireInternalScope refuses any token
// that carries one, so a service route cannot be reached by a signed-in
// person's token even if that token somehow carried the scope — and a test that
// used issueTokenWithScopes would be testing a path production never takes.
func (ta *testApp) issueServiceToken(t *testing.T, scopes []string) string {
	t.Helper()
	token, err := ta.jwtSvc.SignAccessToken("svc-dfe", "", "dfe", scopes, "http://localhost", []string{"http://localhost"}, time.Now().Unix(), time.Now().Unix(), nil, "")
	if err != nil {
		t.Fatalf("issuing service token: %v", err)
	}
	return token
}

// registerUser creates an account with its email already verified — the normal
// state for a usable account. Use registerUnverifiedUser to exercise the gate.
func (ta *testApp) registerUser(t *testing.T, email, password, firstName string) *userDomain.User {
	t.Helper()
	u := ta.registerUnverifiedUser(t, email, password, firstName)
	if err := ta.userSvc.MarkEmailVerified(context.Background(), u.ID()); err != nil {
		t.Fatalf("marking email verified: %v", err)
	}
	u.EmailVerified = true
	return u
}

// addPasskey inserts a minimal credential directly into the in-memory
// repository, bypassing the full WebAuthn registration ceremony — enough for
// tests that only need HasPasskeys/ListByUserID to report a passkey exists.
func (ta *testApp) addPasskey(t *testing.T, userID, name string) {
	t.Helper()
	credID := []byte(name + userID)
	cred := &passKeyDomain.Credential{
		PK:        passKeyDomain.BuildPK(userID),
		SK:        passKeyDomain.BuildSK(credID),
		Name:      name,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := ta.passkeyRepo.Create(context.Background(), cred); err != nil {
		t.Fatalf("adding passkey: %v", err)
	}
}

func (ta *testApp) registerUnverifiedUser(t *testing.T, email, password, firstName string) *userDomain.User {
	t.Helper()
	u, err := ta.userSvc.Register(context.Background(), email, password, firstName, "")
	if err != nil {
		t.Fatalf("registering user: %v", err)
	}
	return u
}

func readJSON(t *testing.T, resp *http.Response, dest any) {
	t.Helper()
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)
	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		t.Fatalf("decoding response body: %v", err)
	}
}

func bodyString(resp *http.Response) string {
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// memScopesRepo serves the default scope catalog seed in-memory.
type memScopesRepo struct{}

func (memScopesRepo) LoadCatalog(_ context.Context) ([]scopesPkg.ServiceScopes, error) {
	return scopesPkg.DefaultCatalog(), nil
}

func (memScopesRepo) PutService(_ context.Context, _ scopesPkg.ServiceScopes) error { return nil }

func (memScopesRepo) LoadResources(_ context.Context) ([]scopesPkg.ResourceServer, error) {
	manifest, err := scopesPkg.AccountManifest()
	if err != nil {
		return nil, err
	}
	for i := range manifest.Scopes {
		manifest.Scopes[i].Description = manifest.Scopes[i].Descriptions["en"]
		manifest.Scopes[i].DescriptionPT = manifest.Scopes[i].Descriptions["pt-BR"]
	}
	return []scopesPkg.ResourceServer{{
		SK: scopesPkg.AccountResourceID, DisplayName: manifest.DisplayName,
		Audience: "http://localhost", Scopes: manifest.Scopes,
	}}, nil
}

// newTestCatalogService builds a CatalogService over the seed with cache disabled.
func newTestCatalogService() *scopesPkg.CatalogService {
	disabledCache, _ := cache.New("")
	return scopesPkg.NewCatalogService(memScopesRepo{}, disabledCache)
}

// ── In-memory repositories ──────────────────────────────────────────────────

type memUserRepo struct {
	byID    map[string]*userDomain.User
	byEmail map[string]*userDomain.User
	nextID  int
}

func newMemUserRepo() *memUserRepo {
	return &memUserRepo{
		byID:    make(map[string]*userDomain.User),
		byEmail: make(map[string]*userDomain.User),
	}
}

func (m *memUserRepo) GetByID(_ context.Context, id string) (*userDomain.User, error) {
	u, ok := m.byID[id]
	if !ok {
		return nil, userDomain.ErrNotFound
	}
	return u, nil
}

func (m *memUserRepo) GetByEmail(_ context.Context, email string) (*userDomain.User, error) {
	u, ok := m.byEmail[strings.ToLower(email)]
	if !ok {
		return nil, userDomain.ErrNotFound
	}
	return u, nil
}

func (m *memUserRepo) Create(_ context.Context, u *userDomain.User) error {
	if u.PK == "" {
		m.nextID++
		u.PK = fmt.Sprintf("USER_test-%d", m.nextID)
	}
	m.byID[u.ID()] = u
	m.byEmail[u.Email] = u
	return nil
}

func (m *memUserRepo) Update(_ context.Context, userID string, updates map[string]any) error {
	u, ok := m.byID[userID]
	if !ok {
		return userDomain.ErrNotFound
	}
	for k, v := range updates {
		switch k {
		case "first_name":
			u.FirstName, _ = v.(string)
		case "last_name":
			u.LastName, _ = v.(string)
		case "display_name":
			u.DisplayName, _ = v.(string)
		case "password_hash":
			u.PasswordHash, _ = v.(string)
		case "tos_version":
			u.TOSVersion, _ = v.(string)
		case "tos_accepted_at":
			u.TOSAcceptedAt, _ = v.(string)
		case "privacy_version":
			u.PrivacyVersion, _ = v.(string)
		case "privacy_accepted_at":
			u.PrivacyAcceptedAt, _ = v.(string)
		case "support_role":
			u.SupportRole, _ = v.(string)
		}
	}
	return nil
}

type memSessionRepo struct {
	sessions map[string]*sessionDomain.Session
	tokens   map[string]*sessionDomain.RefreshToken
	consumed map[string]*sessionDomain.ConsumedToken
}

func newMemSessionRepo() *memSessionRepo {
	return &memSessionRepo{
		sessions: make(map[string]*sessionDomain.Session),
		tokens:   make(map[string]*sessionDomain.RefreshToken),
		consumed: make(map[string]*sessionDomain.ConsumedToken),
	}
}

func (m *memSessionRepo) Create(_ context.Context, s *sessionDomain.Session) error {
	m.sessions[s.PK+"|"+s.SK] = s
	return nil
}

func (m *memSessionRepo) GetByID(_ context.Context, userID, sessionID string) (*sessionDomain.Session, error) {
	k := sessionDomain.BuildPK(userID) + "|" + sessionDomain.BuildSK(sessionID)
	s, ok := m.sessions[k]
	if !ok {
		return nil, sessionDomain.ErrNotFound
	}
	return s, nil
}

func (m *memSessionRepo) GetByTokenHash(_ context.Context, tokenHash string) (*sessionDomain.Session, error) {
	for _, s := range m.sessions {
		if s.RefreshTokenHash == tokenHash {
			return s, nil
		}
	}
	return nil, sessionDomain.ErrNotFound
}

func (m *memSessionRepo) PutRefreshToken(_ context.Context, t *sessionDomain.RefreshToken) error {
	m.tokens[t.PK+"|"+t.SK] = t
	return nil
}

func (m *memSessionRepo) GetRefreshTokenByHash(_ context.Context, tokenHash string) (*sessionDomain.RefreshToken, error) {
	for _, t := range m.tokens {
		if t.RefreshTokenHash == tokenHash {
			return t, nil
		}
	}
	return nil, sessionDomain.ErrRefreshTokenNotFound
}

func (m *memSessionRepo) PutConsumedToken(_ context.Context, userID, sessionID, clientID, supersededHash string, _ int64) error {
	m.consumed[supersededHash] = &sessionDomain.ConsumedToken{
		PK:               sessionDomain.BuildPK(userID),
		SK:               "CONSUMED_" + supersededHash,
		RefreshTokenHash: supersededHash,
		UserID:           userID,
		SessionID:        sessionID,
		ClientID:         clientID,
	}
	return nil
}

func (m *memSessionRepo) GetConsumedByHash(_ context.Context, tokenHash string) (*sessionDomain.ConsumedToken, error) {
	if c, ok := m.consumed[tokenHash]; ok {
		return c, nil
	}
	return nil, sessionDomain.ErrRefreshTokenNotFound
}

func (m *memSessionRepo) UpdateRefreshTokenHash(_ context.Context, userID, sessionID, clientID, newHash, oldHash string) error {
	k := sessionDomain.BuildPK(userID) + "|" + sessionDomain.BuildRefreshSK(sessionID, clientID)
	t, ok := m.tokens[k]
	if !ok {
		return sessionDomain.ErrRefreshTokenNotFound
	}
	if t.RefreshTokenHash != oldHash {
		return sessionDomain.ErrTokenReuse
	}
	t.RefreshTokenHash = newHash
	return nil
}

func (m *memSessionRepo) ListRefreshTokensBySession(_ context.Context, userID, sessionID string) ([]*sessionDomain.RefreshToken, error) {
	prefix := sessionDomain.BuildPK(userID) + "|" + sessionDomain.BuildRefreshSK(sessionID, "")
	var result []*sessionDomain.RefreshToken
	for k, t := range m.tokens {
		if strings.HasPrefix(k, prefix) {
			result = append(result, t)
		}
	}
	return result, nil
}

func (m *memSessionRepo) DeleteRefreshToken(_ context.Context, userID, sessionID, clientID string) error {
	delete(m.tokens, sessionDomain.BuildPK(userID)+"|"+sessionDomain.BuildRefreshSK(sessionID, clientID))
	return nil
}

func (m *memSessionRepo) UpdateMFA(_ context.Context, userID, sessionID string, amr []string, lastMFAAt int64) error {
	k := sessionDomain.BuildPK(userID) + "|" + sessionDomain.BuildSK(sessionID)
	sess, ok := m.sessions[k]
	if !ok {
		return sessionDomain.ErrNotFound
	}
	sess.AMR = amr
	sess.LastMFAAt = lastMFAAt
	return nil
}

func (m *memSessionRepo) Delete(_ context.Context, userID, sessionID string) error {
	delete(m.sessions, sessionDomain.BuildPK(userID)+"|"+sessionDomain.BuildSK(sessionID))
	return nil
}

func (m *memSessionRepo) ListByUserID(_ context.Context, userID string) ([]*sessionDomain.Session, error) {
	pk := sessionDomain.BuildPK(userID)
	var result []*sessionDomain.Session
	for k, s := range m.sessions {
		if strings.HasPrefix(k, pk+"|") {
			result = append(result, s)
		}
	}
	return result, nil
}

type memAPIKeyRepo struct {
	keys   map[string]*apikeyDomain.APIKey
	byHash map[string]*apikeyDomain.APIKey
}

func newMemAPIKeyRepo() *memAPIKeyRepo {
	return &memAPIKeyRepo{
		keys:   make(map[string]*apikeyDomain.APIKey),
		byHash: make(map[string]*apikeyDomain.APIKey),
	}
}

func (m *memAPIKeyRepo) Create(_ context.Context, k *apikeyDomain.APIKey) error {
	m.keys[k.PK+"|"+k.SK] = k
	m.byHash[k.KeyHash] = k
	return nil
}

func (m *memAPIKeyRepo) GetByID(_ context.Context, userID, keyID string) (*apikeyDomain.APIKey, error) {
	k, ok := m.keys[apikeyDomain.BuildPK(userID)+"|"+apikeyDomain.BuildSK(keyID)]
	if !ok {
		return nil, apikeyDomain.ErrNotFound
	}
	return k, nil
}

func (m *memAPIKeyRepo) GetByHash(_ context.Context, hash string) (*apikeyDomain.APIKey, error) {
	k, ok := m.byHash[hash]
	if !ok {
		return nil, apikeyDomain.ErrNotFound
	}
	return k, nil
}

func (m *memAPIKeyRepo) ListByUserID(_ context.Context, userID string) ([]*apikeyDomain.APIKey, error) {
	pk := apikeyDomain.BuildPK(userID)
	var result []*apikeyDomain.APIKey
	for key, k := range m.keys {
		if strings.HasPrefix(key, pk+"|") {
			result = append(result, k)
		}
	}
	return result, nil
}

func (m *memAPIKeyRepo) UpdateLastUsed(_ context.Context, _, _ string) error { return nil }

func (m *memAPIKeyRepo) Delete(_ context.Context, userID, keyID string) error {
	key := apikeyDomain.BuildPK(userID) + "|" + apikeyDomain.BuildSK(keyID)
	k, ok := m.keys[key]
	if !ok {
		return apikeyDomain.ErrNotFound
	}
	delete(m.byHash, k.KeyHash)
	delete(m.keys, key)
	return nil
}

type memPasskeyRepo struct {
	creds map[string]*passKeyDomain.Credential // pk|sk → credential
}

func newMemPasskeyRepo() *memPasskeyRepo {
	return &memPasskeyRepo{creds: make(map[string]*passKeyDomain.Credential)}
}

func (m *memPasskeyRepo) Create(_ context.Context, c *passKeyDomain.Credential) error {
	m.creds[c.PK+"|"+c.SK] = c
	return nil
}

func (m *memPasskeyRepo) GetByCredentialID(_ context.Context, userID string, credentialID []byte) (*passKeyDomain.Credential, error) {
	sk := passKeyDomain.BuildSK(credentialID)
	k := passKeyDomain.BuildPK(userID) + "|" + sk
	c, ok := m.creds[k]
	if !ok {
		return nil, passKeyDomain.ErrNotFound
	}
	return c, nil
}

func (m *memPasskeyRepo) ListByUserID(_ context.Context, userID string) ([]*passKeyDomain.Credential, error) {
	pk := passKeyDomain.BuildPK(userID)
	var result []*passKeyDomain.Credential
	for k, c := range m.creds {
		if strings.HasPrefix(k, pk+"|") {
			result = append(result, c)
		}
	}
	return result, nil
}

func (m *memPasskeyRepo) UpdateLastUsed(_ context.Context, userID, credentialSK, lastUsedAt string) error {
	k := passKeyDomain.BuildPK(userID) + "|" + credentialSK
	c, ok := m.creds[k]
	if !ok {
		return passKeyDomain.ErrNotFound
	}
	c.LastUsedAt = lastUsedAt
	return nil
}

func (m *memPasskeyRepo) Delete(_ context.Context, userID, credentialSK string) error {
	k := passKeyDomain.BuildPK(userID) + "|" + credentialSK
	if _, ok := m.creds[k]; !ok {
		return passKeyDomain.ErrNotFound
	}
	delete(m.creds, k)
	return nil
}

// mockSupportRepo is an in-memory support.Repository — direct map operations,
// no pagination logic beyond returning everything with next="" (tests don't
// exercise multi-page cursors; extend when a test actually needs it).
type mockSupportRepo struct {
	tickets  map[string]*supportDomain.Ticket
	messages map[string][]*supportDomain.Message
	notes    map[string][]*supportDomain.InternalNote
	counter  int64
}

func newMockSupportRepo() *mockSupportRepo {
	return &mockSupportRepo{
		tickets:  make(map[string]*supportDomain.Ticket),
		messages: make(map[string][]*supportDomain.Message),
		notes:    make(map[string][]*supportDomain.InternalNote),
	}
}

func (m *mockSupportRepo) NextTicketNumber(_ context.Context) (int64, error) {
	m.counter++
	return m.counter, nil
}

func (m *mockSupportRepo) CreateTicket(_ context.Context, t *supportDomain.Ticket) error {
	m.tickets[t.ID()] = t
	return nil
}

func (m *mockSupportRepo) GetTicket(_ context.Context, id string) (*supportDomain.Ticket, error) {
	t, ok := m.tickets[id]
	if !ok {
		return nil, supportDomain.ErrNotFound
	}
	return t, nil
}

func (m *mockSupportRepo) GetTicketByAnonToken(_ context.Context, token string) (*supportDomain.Ticket, error) {
	for _, t := range m.tickets {
		if token != "" && t.AnonymousToken == token {
			return t, nil
		}
	}
	return nil, supportDomain.ErrNotFound
}

func (m *mockSupportRepo) GetTicketByNumber(_ context.Context, number int64) (*supportDomain.Ticket, error) {
	for _, t := range m.tickets {
		if t.TicketNumber == number {
			return t, nil
		}
	}
	return nil, supportDomain.ErrNotFound
}

func (m *mockSupportRepo) UpdateTicket(_ context.Context, id string, updates map[string]any) error {
	t, ok := m.tickets[id]
	if !ok {
		return supportDomain.ErrNotFound
	}
	for k, v := range updates {
		switch k {
		case "status":
			t.Status = v.(string)
		case "updated_at":
			t.UpdatedAt = v.(string)
		case "closed_at":
			t.ClosedAt = v.(string)
		case "last_message_at":
			t.LastMessageAt = v.(string)
		case "last_ses_message_id":
			t.LastSESMessageID = v.(string)
		case "root_ses_message_id":
			t.RootSESMessageID = v.(string)
		case "nps_score":
			t.NPSScore = v.(int)
		case "nps_message":
			t.NPSMessage = v.(string)
		case "nps_requested_at":
			t.NPSRequestedAt = v.(string)
		case "escalation_level":
			t.EscalationLevel = v.(string)
		case "escalated_at":
			t.EscalatedAt = v.(string)
		case "escalated_by":
			t.EscalatedBy = v.(string)
		}
	}
	return nil
}

func (m *mockSupportRepo) UpdateActiveStatus(_ context.Context, id, status, updatedAt string) error {
	if m.tickets[id].Status == supportDomain.StatusClosed {
		return supportDomain.ErrTicketClosed
	}
	m.tickets[id].Status = status
	m.tickets[id].UpdatedAt = updatedAt
	return nil
}

func (m *mockSupportRepo) MarkAnswered(_ context.Context, id, updatedAt string) error {
	if m.tickets[id].Status == supportDomain.StatusClosed {
		return supportDomain.ErrTicketClosed
	}
	m.tickets[id].Status = supportDomain.StatusAnswered
	m.tickets[id].UpdatedAt = updatedAt
	return nil
}
func (m *mockSupportRepo) UpdateEscalation(_ context.Context, id, level, agentUserID, updatedAt string) error {
	if m.tickets[id].Status == supportDomain.StatusClosed {
		return supportDomain.ErrTicketClosed
	}
	m.tickets[id].EscalationLevel = level
	m.tickets[id].EscalatedBy = agentUserID
	if level == supportDomain.EscalationNone {
		m.tickets[id].EscalatedAt = ""
	} else {
		m.tickets[id].EscalatedAt = updatedAt
	}
	return nil
}

func (m *mockSupportRepo) PutInternalNote(_ context.Context, note *supportDomain.InternalNote) error {
	id := note.PK
	if m.tickets[id].Status == supportDomain.StatusClosed {
		return supportDomain.ErrTicketClosed
	}
	note.PK = supportDomain.BuildPK(id)
	note.SK = supportDomain.BuildNoteSK(note.CreatedAt)
	m.notes[id] = append(m.notes[id], note)
	return nil
}
func (m *mockSupportRepo) ListInternalNotes(_ context.Context, id string) ([]*supportDomain.InternalNote, error) {
	return m.notes[id], nil
}
func (m *mockSupportRepo) CloseTicket(_ context.Context, id string, closedAt time.Time, _ int64) error {
	m.tickets[id].Status = supportDomain.StatusClosed
	m.tickets[id].ClosedAt = closedAt.Format(time.RFC3339)
	return nil
}
func (m *mockSupportRepo) GetMetrics(_ context.Context, _ time.Time) ([]supportDomain.MetricBucket, error) {
	return []supportDomain.MetricBucket{}, nil
}

func (m *mockSupportRepo) PutMessage(_ context.Context, msg *supportDomain.Message, requireOpen bool) error {
	ticketID := msg.PK
	if requireOpen && m.tickets[ticketID].Status == supportDomain.StatusClosed {
		return supportDomain.ErrTicketClosed
	}
	msg.PK = supportDomain.BuildPK(ticketID)
	msg.SK = supportDomain.BuildMessageSK(msg.CreatedAt)
	m.messages[ticketID] = append(m.messages[ticketID], msg)
	return nil
}

func (m *mockSupportRepo) ListMessages(_ context.Context, ticketID string) ([]*supportDomain.Message, error) {
	return m.messages[ticketID], nil
}

func (m *mockSupportRepo) ListByUser(_ context.Context, userID, _ string, _ int32) ([]*supportDomain.Ticket, string, error) {
	var out []*supportDomain.Ticket
	for _, t := range m.tickets {
		if t.UserID == userID {
			out = append(out, t)
		}
	}
	return out, "", nil
}

func (m *mockSupportRepo) ListByStatus(_ context.Context, status, _ string, _ int32) ([]*supportDomain.Ticket, string, error) {
	var out []*supportDomain.Ticket
	for _, t := range m.tickets {
		if t.Status == status {
			out = append(out, t)
		}
	}
	return out, "", nil
}
