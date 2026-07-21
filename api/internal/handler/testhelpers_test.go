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
	sessionDomain "gopkg.aoctech.app/account/api/internal/domain/session"
	userDomain "gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/account/api/internal/handler"
	"gopkg.aoctech.app/account/api/internal/middleware"
	scopesPkg "gopkg.aoctech.app/account/api/internal/scopes"
	"gopkg.aoctech.app/account/api/internal/storage"
)

// noopTOTPService implements both TOTPService and TOTPManagementService.
// All methods return errors — TOTP is not configured.
type noopTOTPService struct{}

func (n *noopTOTPService) Get(_ context.Context, _ string) (*totp.TOTPSecret, error) {
	return nil, errors.New("totp not configured")
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
		RSAPrivateKey: privateKey,
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
	kycSvc := kycDomain.NewService(newMemKYCRepo(userRepo), kycPresigner)

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
			return apierror.ServerError(c.Path()).Send(c)
		},
	})
	app.Use(recover.New())

	sharedClientRepo := newMemClientRepo()

	v1 := app.Group("/v1.0")
	handler.NewAuthHandler(userSvc, sessionSvc, noop, passkeySvc, sharedClientRepo, disabledCache, cfg, nil, auditSvc).Register(v1)
	handler.NewPasskeyHandler(passkeySvc, userSvc, sessionSvc, noop, disabledCache, cfg, auditSvc).RegisterAuth(v1.Group("/auth"))
	v1.Get("/userinfo", middleware.RequireAuth(jwtSvc), handler.NewUserInfoHandler(userSvc).UserInfo)
	handler.NewStepUpHandler(sessionSvc, noop, passkeySvc, disabledCache, auditSvc).Register(v1, middleware.RequireAuth(jwtSvc), middleware.RequireClientID(cfg.SelfClientID))

	account := v1.Group("/account", middleware.RequireAuth(jwtSvc), middleware.RequireClientID(cfg.SelfClientID))
	stepUp := middleware.RequireRecentMFA(middleware.StepUpMaxAge)
	handler.NewProfileHandler(userSvc, sessionSvc, auditSvc).Register(account, stepUp)
	handler.NewSessionsHandler(sessionSvc, auditSvc).Register(account)
	handler.NewAPIKeysHandler(apiKeySvc, newTestCatalogService(), auditSvc).Register(account, stepUp)
	handler.NewOAuthClientsHandler(oauthclientDomain.NewService(sharedClientRepo, newTestCatalogService()), auditSvc).Register(account, stepUp)
	handler.NewConsentsHandler(consentDomain.NewService(newMemConsentRepo()), sharedClientRepo, auditSvc).Register(account)
	handler.NewMFAHandler(noop, userSvc, cfg, auditSvc).Register(account, stepUp)
	handler.NewActivityHandler(auditSvc).Register(account)
	handler.NewPasskeyHandler(passkeySvc, userSvc, sessionSvc, noop, disabledCache, cfg, auditSvc).RegisterManagement(account, stepUp)
	handler.NewTermsHandler(userSvc, auditSvc).Register(account)
	kycH := handler.NewKYCHandler(kycSvc, auditSvc)
	kycH.Register(account, stepUp)
	kycH.RegisterInternalGet(v1, middleware.RequireAuth(jwtSvc), middleware.RequireInternalScope(scopesPkg.InternalWalletConfirmDeposit))

	handler.NewWellKnownHandler(jwtSvc, cfg.BaseURL).Register(app)

	socialCache := cache.NewInMemory()
	handler.NewSocialHandler(userSvc, sessionSvc, socialCache, cfg, auditSvc).Register(v1)

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

func (m *memKYCRepo) SaveSubmission(_ context.Context, userID string, rec kycDomain.Record, oldCPF string) error {
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

	u.CPF, u.LegalName, u.BirthDate = rec.CPF, rec.LegalName, rec.BirthDate
	u.KYCMethod, u.KYCDocStatus = rec.Method, rec.DocStatus
	u.KYCSubmittedAt, u.KYCExpiresAt = rec.SubmittedAt, rec.ExpiresAt
	u.Address = rec.Address
	// Documents were already uploaded and validated before Submit — only the
	// stale rejection reason is cleared here.
	u.KYCRejectionReason = ""
	return nil
}

func (m *memKYCRepo) AddDocument(_ context.Context, userID string, doc kycDomain.Document, docStatus string) error {
	u, ok := m.users.byID[userID]
	if !ok {
		return userDomain.ErrNotFound
	}
	u.KYCDocuments = append(u.KYCDocuments, doc)
	u.KYCDocStatus = docStatus
	return nil
}

func (m *memKYCRepo) MarkVerified(_ context.Context, userID, verifiedAt string) error {
	u, ok := m.users.byID[userID]
	if !ok {
		return userDomain.ErrNotFound
	}
	u.KYCLevel, u.KYCVerifiedAt = kycDomain.LevelVerified, verifiedAt
	u.KYCDocStatus, u.KYCRejectionReason = kycDomain.DocStatusNone, ""
	return nil
}

func (m *memKYCRepo) MarkRejected(_ context.Context, userID, reason string) error {
	u, ok := m.users.byID[userID]
	if !ok {
		return userDomain.ErrNotFound
	}
	u.KYCDocStatus, u.KYCRejectionReason = kycDomain.DocStatusRejected, reason
	u.KYCDocuments = nil
	return nil
}

func (m *memKYCRepo) ListPendingKYC(_ context.Context) ([]*userDomain.User, error) {
	var out []*userDomain.User
	for _, u := range m.users.byID {
		if u.KYCDocStatus == kycDomain.DocStatusPendingReview {
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
	token, err := ta.jwtSvc.SignAccessToken(userID, "sess-test", "test-client", []string{"openid", "profile"}, "http://localhost", []string{"http://localhost"}, time.Now().Unix(), 0, nil, "")
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
	token, err := ta.jwtSvc.SignAccessToken(userID, "sess-test", "test-client", []string{"openid", "profile"}, "http://localhost", []string{"http://localhost"}, time.Now().Unix(), time.Now().Unix(), []string{sessionDomain.AMRPassword, sessionDomain.AMRTOTP}, "")
	if err != nil {
		t.Fatalf("issuing token: %v", err)
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

func (m *memSessionRepo) UpdateGeoData(_ context.Context, userID, sessionID, city, region string, lat, lon float64) error {
	k := sessionDomain.BuildPK(userID) + "|" + sessionDomain.BuildSK(sessionID)
	s, ok := m.sessions[k]
	if !ok {
		return sessionDomain.ErrNotFound
	}
	s.GeoCity = city
	s.GeoRegion = region
	s.GeoLatitude = lat
	s.GeoLongitude = lon
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
