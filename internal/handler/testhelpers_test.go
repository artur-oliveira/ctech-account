package handler_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/artur-oliveira/ctech-account/internal/apierror"
	"github.com/artur-oliveira/ctech-account/internal/cache"
	"github.com/artur-oliveira/ctech-account/internal/config"
	"github.com/artur-oliveira/ctech-account/internal/crypto"
	apikeyDomain "github.com/artur-oliveira/ctech-account/internal/domain/apikey"
	"github.com/artur-oliveira/ctech-account/internal/domain/mfa/totp"
	sessionDomain "github.com/artur-oliveira/ctech-account/internal/domain/session"
	userDomain "github.com/artur-oliveira/ctech-account/internal/domain/user"
	"github.com/artur-oliveira/ctech-account/internal/handler"
	"github.com/artur-oliveira/ctech-account/internal/middleware"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/recover"
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

// testApp builds a Fiber app wired with in-memory repositories — no real AWS required.
type testApp struct {
	app        *fiber.App
	userSvc    *userDomain.Service
	sessionSvc *sessionDomain.Service
	apiKeySvc  *apikeyDomain.Service
	jwtSvc     *crypto.JWTService
	cfg        *config.Config
}

func newTestApp(t *testing.T) *testApp {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}

	cfg := &config.Config{
		Environment:   "test",
		BaseURL:       "http://localhost",
		Port:          "8000",
		RSAPrivateKey: privateKey,
		PublicKeyKID:  "test-kid",
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

	userSvc := userDomain.NewService(userRepo)
	sessionSvc := sessionDomain.NewService(sessionRepo)
	apiKeySvc := apikeyDomain.NewService(apikeyRepo)

	noop := &noopTOTPService{}

	app := fiber.New(fiber.Config{
		ErrorHandler: func(c fiber.Ctx, err error) error {
			if problem, ok := errors.AsType[*apierror.Problem](err); ok {
				return problem.Send(c)
			}
			return apierror.ServerError(c.Path()).Send(c)
		},
	})
	app.Use(recover.New())

	v1 := app.Group("/v1")
	handler.NewAuthHandler(userSvc, sessionSvc, noop, disabledCache).Register(v1)
	v1.Get("/userinfo", middleware.RequireAuth(jwtSvc), handler.NewUserInfoHandler(userSvc).UserInfo)

	account := v1.Group("/account", middleware.RequireAuth(jwtSvc))
	handler.NewProfileHandler(userSvc).Register(account)
	handler.NewSessionsHandler(sessionSvc).Register(account)
	handler.NewAPIKeysHandler(apiKeySvc).Register(account)
	handler.NewMFAHandler(noop, userSvc, cfg).Register(account)

	handler.NewWellKnownHandler(jwtSvc, cfg.BaseURL).Register(app)

	return &testApp{
		app:        app,
		userSvc:    userSvc,
		sessionSvc: sessionSvc,
		apiKeySvc:  apiKeySvc,
		jwtSvc:     jwtSvc,
		cfg:        cfg,
	}
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
	token, err := ta.jwtSvc.SignAccessToken(userID, "sess-test", []string{"openid", "profile"}, "http://localhost")
	if err != nil {
		t.Fatalf("issuing token: %v", err)
	}
	return token
}

func (ta *testApp) registerUser(t *testing.T, email, password, firstName string) *userDomain.User {
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
		}
	}
	return nil
}

type memSessionRepo struct {
	sessions map[string]*sessionDomain.Session
}

func newMemSessionRepo() *memSessionRepo {
	return &memSessionRepo{sessions: make(map[string]*sessionDomain.Session)}
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

func (m *memSessionRepo) UpdateRefreshToken(_ context.Context, userID, sessionID, newHash string) error {
	k := sessionDomain.BuildPK(userID) + "|" + sessionDomain.BuildSK(sessionID)
	s, ok := m.sessions[k]
	if !ok {
		return sessionDomain.ErrNotFound
	}
	s.RefreshTokenHash = newHash
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
