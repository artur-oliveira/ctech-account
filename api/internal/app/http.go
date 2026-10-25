package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/logger"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"go.uber.org/fx"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"gopkg.aoctech.app/account/api/internal/cache"
	"gopkg.aoctech.app/account/api/internal/config"
	"gopkg.aoctech.app/account/api/internal/crypto"
	"gopkg.aoctech.app/account/api/internal/database"
	kycDomain "gopkg.aoctech.app/account/api/internal/domain/kyc"
	oauthclientDomain "gopkg.aoctech.app/account/api/internal/domain/oauth/client"
	orgDomain "gopkg.aoctech.app/account/api/internal/domain/organization"
	supportDomain "gopkg.aoctech.app/account/api/internal/domain/support"
	userDomain "gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/account/api/internal/handler"
	"gopkg.aoctech.app/account/api/internal/middleware"
	"gopkg.aoctech.app/account/api/internal/problem"
	scopesPkg "gopkg.aoctech.app/account/api/internal/scopes"
	"gopkg.aoctech.app/account/api/internal/utils"
	"gopkg.aoctech.app/api-commons/authguard"
	"gopkg.aoctech.app/api-commons/health"
	"gopkg.aoctech.app/api-commons/idempotency"
	fiberobs "gopkg.aoctech.app/api-commons/observability/fiber"
	commonws "gopkg.aoctech.app/api-commons/ws"
)

// HTTPModule: the Fiber app, its middleware, and the route table. Routes are
// registered in one place, in order, because Fiber matches in registration
// order (a literal segment mounted after a parameter is never reached).
var HTTPModule = fx.Module("http",
	fx.Provide(newAllowedOrigins, newFiberApp),
	fx.Invoke(registerRoutes, startServer),
)

// allowedOrigins is the validated CORS origin list.
type allowedOrigins []string

func newAllowedOrigins(cfg *config.Config) allowedOrigins {
	rawOrigins := append([]string{cfg.AppURL}, cfg.AllowedOrigins...)
	origins := rawOrigins[:0]
	for _, o := range rawOrigins {
		if strings.HasPrefix(o, "http://") || strings.HasPrefix(o, "https://") {
			if !slices.Contains(origins, o) {
				origins = append(origins, o)
			} else {
				slog.Warn("origin already present", "origin", o)
			}
		} else {
			slog.Warn("skipping invalid CORS origin (missing http/https scheme)", "origin", o)
		}
	}
	if len(origins) == 0 {
		fatal("no valid CORS origins configured — check BASE_URL and ALLOWED_ORIGINS SSM parameters")
	}
	return origins
}

func newFiberApp(cfg *config.Config, origins allowedOrigins) *fiber.App {
	app := fiber.New(fiber.Config{
		AppName:      "ctech-account",
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
		ProxyHeader:  fiber.HeaderXForwardedFor,
		TrustProxy:   len(cfg.TrustedProxies) > 0,
		TrustProxyConfig: fiber.TrustProxyConfig{
			Proxies: cfg.TrustedProxies,
		},
		ErrorHandler: func(c fiber.Ctx, err error) error {
			// RFC 7807 Problem Details as the single error format.
			if problem, ok := errors.AsType[*problem.Problem](err); ok {
				return problem.Send(c)
			}
			if fiberErr, ok := errors.AsType[*fiber.Error](err); ok {
				return problem.NewFromFiber(fiberErr, c.Path()).WithCause(fiberErr).Send(c)
			}
			return problem.ServerError(c.Path()).WithCause(err).Send(c)
		},
	})

	app.Use(fiberobs.RequestID())
	app.Use(recover.New())
	app.Use(logger.New(logger.Config{
		Format: `{"time":"${time}","status":${status},"latency":"${latency}","method":"${method}",` +
			`"path":"${path}","request_id":"${respHeader:X-Request-Id}"}` + "\n",
		DisableColors: true,
		Skip: func(c fiber.Ctx) bool {
			return c.Path() == "/v1.0/health" || c.Path() == "/v1.0/health-check"
		},
	}))

	app.Use(cors.New(cors.Config{
		AllowOrigins:     []string(origins),
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "X-Request-ID", "Idempotency-Key"},
		ExposeHeaders:    []string{"X-Request-ID", "Idempotent-Replay"},
		AllowCredentials: true,
		MaxAge:           3600,
	}))

	return app
}

// routesIn is every handler and service the route table mounts. Optional
// pieces (data export, account deletion) are nil / empty when switched off.
type routesIn struct {
	fx.In
	Cfg      *config.Config
	DB       *dynamodb.Client
	App      *fiber.App
	Origins  allowedOrigins
	Valkey   *cache.Client
	WS       commonws.Registry
	JWT      *crypto.JWTService
	Users    *userDomain.Service
	Support  *supportDomain.Service
	KYC      *kycDomain.Service
	Orgs     *orgDomain.Service
	Clients  oauthclientDomain.Repository
	Catalog  *scopesPkg.CatalogService
	Operator *oauthclientDomain.OperatorService

	WellKnown     *handler.WellKnownHandler
	Auth          *handler.AuthHandler
	Social        *handler.SocialHandler
	Authorize     *handler.AuthorizeHandler
	Token         *handler.TokenHandler
	UserInfo      *handler.UserInfoHandler
	StepUp        *handler.StepUpHandler
	Passkey       *handler.PasskeyHandler
	SupportH      *handler.SupportHandler
	SupportAdmin  *handler.SupportAdminHandler
	Profile       *handler.ProfileHandler
	Sessions      *handler.SessionsHandler
	APIKeys       *handler.APIKeysHandler
	OAuthClients  *handler.OAuthClientsHandler
	Consents      *handler.ConsentsHandler
	MFA           *handler.MFAHandler
	Activity      *handler.ActivityHandler
	KYCH          *handler.KYCHandler
	KYCAdmin      *handler.KYCAdminHandler
	Terms         *handler.TermsHandler
	Scopes        *handler.ScopesHandler
	ScopeRegistry *handler.ScopeRegistryHandler
	Export        *handler.DataExportHandler
	Deletion      *DeletionHandler
	Handoff       *handler.HandoffHandler
	OrgH          *handler.OrganizationHandler
	CompanyH      *handler.CompanyHandler
}

func registerRoutes(in routesIn) {
	cfg, app, db, valkeyClient, jwtSvc, userSvc := in.Cfg, in.App, in.DB, in.Valkey, in.JWT, in.Users
	valkeyRequired := valkeyRequired(cfg)

	in.WellKnown.Register(app)

	v1 := app.Group("/v1.0")
	handler.RegisterSupportWS(v1, in.Support, userSvc, jwtSvc, in.WS, []string(in.Origins))
	authH, socialH, authorizeH, tokenH, userinfoH := in.Auth, in.Social, in.Authorize, in.Token, in.UserInfo
	stepUpH, passkeyH, supportH, supportAdminH := in.StepUp, in.Passkey, in.SupportH, in.SupportAdmin
	profileH, sessionsH, apiKeysH, oauthClientsH, consentsH := in.Profile, in.Sessions, in.APIKeys, in.OAuthClients, in.Consents
	mfaH, activityH, kycH, termsH, scopeRegistryH := in.MFA, in.Activity, in.KYCH, in.Terms, in.ScopeRegistry
	orgH, companyH := in.OrgH, in.CompanyH

	healthH := health.New(health.Config{ServiceID: "ctech-account", Description: "ctech-account Identity Provider", Version: "1", ReleaseID: cfg.AppVersion}).Add(
		health.DynamoDB(db, database.TableName(cfg.TablePrefix, "account_users"), true),
		// Valkey is a hard dependency in production (OAuth codes, MFA, rate limits): primary there.
		health.Cache(func(ctx context.Context) error {
			if !valkeyClient.Enabled() {
				return health.ErrNotConfigured
			}
			return valkeyClient.Ping(ctx)
		}, valkeyRequired),
		health.CPU(), health.Memory(),
	)
	v1.Get("/health", healthH.Liveness())
	v1.Get("/health-check", healthH.Report())

	// Rate limiting (Valkey-backed; no-op when Valkey is disabled).
	// Brute-force guard: count only failed responses per client IP.
	ipKey := utils.IP
	authLimiter := middleware.RateLimit(middleware.RateLimitConfig{
		Cache: valkeyClient, Prefix: "login", Max: middleware.FailedLoginMax,
		Window: middleware.FailedLoginWindow, KeyFunc: ipKey, CountOnlyFailures: true, FailClosed: true,
	})
	pwResetLimiter := middleware.RateLimit(middleware.RateLimitConfig{
		Cache: valkeyClient, Prefix: "pwreset", Max: middleware.FailedLoginMax,
		Window: middleware.FailedLoginWindow, KeyFunc: ipKey, CountOnlyFailures: true, FailClosed: true,
	})
	tokenLimiterBruteForce := middleware.RateLimit(middleware.RateLimitConfig{
		Cache: valkeyClient, Prefix: "token_brute", Max: middleware.FailedLoginMax,
		Window: middleware.FailedLoginWindow, KeyFunc: ipKey, CountOnlyFailures: true, FailClosed: true,
	})
	tokenLimiterThroughput := middleware.RateLimit(middleware.RateLimitConfig{
		Cache: valkeyClient, Prefix: "token_throughput", Max: 50,
		Window: time.Minute, KeyFunc: ipKey, CountOnlyFailures: false, FailClosed: true,
	})
	perUserLimiter := middleware.RateLimit(middleware.RateLimitConfig{
		Cache: valkeyClient, Prefix: "user", Max: middleware.PerUserMax,
		Window: middleware.PerUserWindow, KeyFunc: middleware.GetUserID, CountOnlyFailures: false, FailClosed: true,
	})
	// Conditional WebAuthn starts a discoverable challenge when the login page
	// loads. Bound the public Valkey-writing endpoint without probing an email.
	passkeyBeginLimiter := middleware.RateLimit(middleware.RateLimitConfig{
		Cache: valkeyClient, Prefix: "passkeybegin", Max: middleware.PasskeyBeginMax,
		Window: middleware.PasskeyBeginWindow, KeyFunc: ipKey, CountOnlyFailures: false, FailClosed: true,
	})
	// Same tier as passkeyBeginLimiter, own key prefix so completing a
	// challenge doesn't share budget with starting one.
	passkeyCompleteLimiter := middleware.RateLimit(middleware.RateLimitConfig{
		Cache: valkeyClient, Prefix: "passkeycomplete", Max: middleware.PasskeyBeginMax,
		Window: middleware.PasskeyBeginWindow, KeyFunc: ipKey, CountOnlyFailures: false, FailClosed: true,
	})
	// Same tier as authLimiter, own key prefix — social login abuse
	// shouldn't burn (or be burned by) the password-login budget.
	googleLimiter := middleware.RateLimit(middleware.RateLimitConfig{
		Cache: valkeyClient, Prefix: "google", Max: middleware.FailedLoginMax,
		Window: middleware.FailedLoginWindow, KeyFunc: ipKey, CountOnlyFailures: true, FailClosed: true,
	})
	v1.Use("/auth/login", authLimiter)
	v1.Use("/auth/mfa/challenge", authLimiter)
	v1.Use("/authorize", authLimiter)
	v1.Use("/authorize/consent", authLimiter)
	v1.Use("/auth/forgot-password", pwResetLimiter)
	v1.Use("/auth/reset-password", pwResetLimiter)
	v1.Use("/auth/resend-verification", pwResetLimiter)
	v1.Use("/auth/register", pwResetLimiter)
	v1.Use("/auth/passkeys/authenticate/begin", passkeyBeginLimiter)
	v1.Use("/auth/passkeys/authenticate/complete", passkeyCompleteLimiter)
	v1.Use("/auth/google", googleLimiter)
	v1.Use("/auth/google/callback", googleLimiter)
	v1.Use("/token", tokenLimiterBruteForce, tokenLimiterThroughput)
	v1.Use("/revoke", tokenLimiterBruteForce, tokenLimiterThroughput)
	v1.Use("/auth/deletion", pwResetLimiter)

	in.Scopes.Register(v1)
	authH.Register(v1)
	socialH.Register(v1)
	authorizeH.Register(v1)
	tokenH.Register(v1)
	stepUpH.Register(v1, middleware.RequireAuth(jwtSvc), authguard.RequireClientID(cfg.SelfClientID))
	passkeyH.RegisterAuth(v1.Group("/auth"))
	v1.Get("/userinfo", middleware.RequireAuth(jwtSvc), userinfoH.UserInfo)
	supportH.Register(v1.Group("", middleware.OptionalAuth(jwtSvc)))

	account := v1.Group("/account", middleware.RequireAuth(jwtSvc), perUserLimiter)
	stepUp := authguard.RequireRecentMFA(authguard.DefaultStepUpMaxAge)
	profileH.Register(account, stepUp)
	sessionsH.Register(account)
	apiKeysH.Register(account, stepUp)
	oauthClientsH.Register(account, stepUp)
	consentsH.Register(account)
	mfaH.Register(account, stepUp)
	activityH.Register(account)
	kycH.Register(account, stepUp)
	termsH.Register(account)
	passkeyH.RegisterManagement(account, stepUp)
	supportH.RegisterAccount(account)
	if in.Export != nil {
		in.Export.Register(account, authguard.RequireClientID(cfg.SelfClientID), middleware.DataExportLimiters(valkeyClient)...)
		in.Export.RegisterInternal(v1, middleware.RequireAuth(jwtSvc), cfg.ErasureServiceForClient)
	}
	if in.Deletion.DeletionHandler != nil {
		deletionH := in.Deletion.DeletionHandler
		deletionH.Register(account, v1.Group("/auth"),
			authguard.RequireClientID(cfg.SelfClientID), middleware.DeletionRequestLimiters(valkeyClient)...)
		deletionH.RegisterInternal(v1, middleware.RequireAuth(jwtSvc), cfg.ErasureServiceForClient, []string{erasureSubjectReader})
		deletionH.RegisterAdmin(v1.Group("/admin/deletion", middleware.RequireAuth(jwtSvc), authguard.RequireClientID(cfg.SelfClientID),
			middleware.RequireSupportRole(userSvc, userDomain.SupportRoleAdmin)))
	}
	adminAuth := []fiber.Handler{
		middleware.RequireAuth(jwtSvc),
		authguard.RequireClientID(cfg.SelfClientID),
	}
	// Hoisted: the company routes mount on this same group, so both handlers
	// sit behind one RequireAuth + RequireClientID rather than two groups that
	// could drift apart.
	orgsGroup := v1.Group("/organizations", adminAuth[0], adminAuth[1])
	// Optional Idempotency-Key on the create routes whose answer holds no secret
	// (the invitation answer carries a token and is deliberately not stored). Run
	// before the real handlers by registration order; Next() falls through to them.
	idemStore := idempotency.NewDynamoStore(database.NewBase(db, cfg.TablePrefix, middleware.IdempotencyTable))
	orgsGroup.Post("/", middleware.Idempotency(idemStore, nil))
	orgsGroup.Post("/:id/companies", middleware.Idempotency(idemStore, func(c fiber.Ctx) string { return c.Params("id") }))
	// Registered FIRST, before the organization handler's /:id: Fiber matches in
	// registration order, so a literal segment mounted after a parameter is
	// captured by it and never reached.
	in.Handoff.Register(orgsGroup)
	orgH.Register(
		orgsGroup,
		v1.Group("/invitations", adminAuth[0], adminAuth[1]),
	)
	companyH.Register(orgsGroup)
	companyH.RegisterLookup(v1.Group("/companies", adminAuth[0], adminAuth[1]))
	supportAdminH.Register(v1.Group("/admin", adminAuth[0], adminAuth[1], middleware.RequireSupportRole(userSvc, userDomain.SupportRoleAgent)))
	in.KYCAdmin.Register(v1.Group("/admin/kyc", adminAuth[0], adminAuth[1], middleware.RequireSupportRole(userSvc, userDomain.SupportRoleManager)))
	kycH.RegisterInternalGet(v1, middleware.RequireAuth(jwtSvc), authguard.RequireInternalScope(scopesPkg.InternalAccountKYC))
	companyH.RegisterInternal(v1, middleware.RequireAuth(jwtSvc), authguard.RequireInternalScope(scopesPkg.InternalAccountCompanyActor))
	orgH.RegisterInternal(v1, middleware.RequireAuth(jwtSvc),
		authguard.RequireInternalScope(scopesPkg.InternalAccountOrgMember),
		authguard.RequireInternalScope(scopesPkg.InternalAccountUserOrganizations))
	handler.NewOAuthClientInternalHandler(oauthclientDomain.NewInternalService(in.Clients, userSvc)).
		RegisterInternal(v1, middleware.RequireAuth(jwtSvc), authguard.RequireInternalScope(scopesPkg.InternalAccountOAuthClientRead))
	handler.NewUserContactInternalHandler(userSvc).
		RegisterInternal(v1, middleware.RequireAuth(jwtSvc), authguard.RequireInternalScope(scopesPkg.InternalAccountUserContactRead))
	scopeRegistryH.Register(v1,
		middleware.RequireAuth(jwtSvc),
		authguard.RequireInternalScope(scopesPkg.InternalAccountScopeRegistryWrite),
	)
}

// startServer listens last, after every route is mounted, and drains on stop.
func startServer(lc fx.Lifecycle, app *fiber.App, cfg *config.Config) {
	port := fmt.Sprintf(":%s", cfg.Port)
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			slog.Info("ctech-account starting", "port", port, "env", cfg.Environment)
			go func() {
				if err := app.Listen(port); err != nil {
					slog.Error("server error", "error", err)
				}
			}()
			return nil
		},
		OnStop: func(context.Context) error {
			if err := app.ShutdownWithTimeout(10 * time.Second); err != nil {
				slog.Error("shutdown error", "error", err)
			}
			return nil
		},
	})
}
