package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/logger"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/gofiber/fiber/v3/middleware/requestid"
	"gopkg.aoctech.app/account/api/internal/apierror"
	"gopkg.aoctech.app/account/api/internal/cache"
	"gopkg.aoctech.app/account/api/internal/config"
	"gopkg.aoctech.app/account/api/internal/crypto"
	"gopkg.aoctech.app/account/api/internal/database"
	apikeyDomain "gopkg.aoctech.app/account/api/internal/domain/apikey"
	auditDomain "gopkg.aoctech.app/account/api/internal/domain/audit"
	kycDomain "gopkg.aoctech.app/account/api/internal/domain/kyc"
	passKeyDomain "gopkg.aoctech.app/account/api/internal/domain/mfa/passkey"
	totpDomain "gopkg.aoctech.app/account/api/internal/domain/mfa/totp"
	oauthclientDomain "gopkg.aoctech.app/account/api/internal/domain/oauth/client"
	authcodeDomain "gopkg.aoctech.app/account/api/internal/domain/oauth/code"
	consentDomain "gopkg.aoctech.app/account/api/internal/domain/oauth/consent"
	sessionDomain "gopkg.aoctech.app/account/api/internal/domain/session"
	userDomain "gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/account/api/internal/email"
	"gopkg.aoctech.app/account/api/internal/handler"
	"gopkg.aoctech.app/account/api/internal/keystore"
	"gopkg.aoctech.app/account/api/internal/middleware"
	scopesPkg "gopkg.aoctech.app/account/api/internal/scopes"
	"gopkg.aoctech.app/account/api/internal/storage"
	"gopkg.aoctech.app/account/api/internal/utils"
	"gopkg.aoctech.app/api-commons/awsconfig"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("loading config: %v", err)
	}

	ctx := context.Background()

	db, err := database.New(ctx, cfg.AWSRegion)
	if err != nil {
		log.Fatalf("connecting to DynamoDB: %v", err)
	}

	valkeyClient, err := cache.New(cfg.ValkeyURL)
	if err != nil {
		log.Fatalf("connecting to Valkey: %v", err)
	}
	defer valkeyClient.Close()

	// Valkey is a hard production dependency: OAuth authorization codes, MFA /
	// passkey challenges, account-recovery tokens and all rate limiting live
	// there with no DynamoDB fallback. Refuse to boot without it in non-dev
	// environments so a misconfigured instance never enters rotation (§1.1).
	if !valkeyClient.Enabled() && cfg.Environment != "dev" && cfg.Environment != "development" {
		log.Fatalf("VALKEY_URL is required in environment %q: OAuth codes, MFA tokens and rate limiting depend on Valkey", cfg.Environment)
	}
	valkeyRequired := cfg.Environment != "dev" && cfg.Environment != "development"

	// Signing keys: RSA_PRIVATE_KEY env = dev mode (single key, no rotation);
	// otherwise versioned keys come from SSM and rotate automatically.
	var jwtSvc *crypto.JWTService
	var keyStore *keystore.Store
	if cfg.RSAPrivateKey != nil {
		jwtSvc, err = crypto.NewJWTService(cfg)
	} else {
		awsCfg, awsErr := awsconfig.Load(ctx, cfg.AWSRegion)
		if awsErr != nil {
			log.Fatalf("loading AWS config for SSM: %v", awsErr)
		}
		keyStore = keystore.NewStore(ssm.NewFromConfig(awsCfg), cfg.Environment)
		active, previous, loadErr := keyStore.Load(ctx)
		if loadErr != nil {
			log.Fatalf("loading signing keys from SSM: %v", loadErr)
		}
		jwtSvc, err = crypto.NewJWTServiceWithKeys(cfg, active, previous)
	}
	if err != nil {
		log.Fatalf("initializing JWT service: %v", err)
	}

	// Auto-rotation: hourly reload from SSM; rotate at 90 days under a Valkey
	// lock so exactly one ASG instance generates the new key. Dev mode
	// (env key) and Valkey-less deployments skip it — cmd/rotatekeys remains
	// the manual path.
	if keyStore != nil && valkeyClient.Enabled() {
		// CAC-025: namespace the rotation lock by environment so a Valkey
		// shared across envs can't let one env block another.
		lockKey := keystore.LockKey
		if cfg.Environment != "" {
			lockKey = fmt.Sprintf("rotate_jwk_lock:%s", cfg.Environment)
		}
		go keystore.RunRotator(ctx, keystore.RotatorConfig{
			Store:  keyStore,
			Reload: jwtSvc.Reload,
			TryLock: func(ctx context.Context) (bool, error) {
				return valkeyClient.SetNX(ctx, lockKey, "1", keystore.LockTTL)
			},
			Unlock: func(ctx context.Context) error {
				return valkeyClient.Delete(ctx, lockKey)
			},
			Interval: keystore.CheckInterval,
			MaxAge:   keystore.KeyMaxAge,
			Now:      time.Now,
			Env:      cfg.Environment,
		})
	}

	// Repositories
	userRepo := userDomain.NewRepository(db, cfg.TablePrefix)
	sessionRepo := sessionDomain.NewRepository(db, cfg.TablePrefix)
	oauthClientRepo := oauthclientDomain.NewRepository(db, cfg.TablePrefix)
	authCodeRepo := authcodeDomain.NewRepository(valkeyClient)
	apiKeyRepo := apikeyDomain.NewRepository(db, cfg.TablePrefix)
	consentRepo := consentDomain.NewRepository(db, cfg.TablePrefix)
	kycRepo := kycDomain.NewRepository(db, cfg.TablePrefix, userRepo)
	scopesRepo := scopesPkg.NewRepository(db, cfg.TablePrefix)

	// WebAuthn Relying Party
	wa, err := webauthn.New(&webauthn.Config{
		RPID:          cfg.RPID,
		RPDisplayName: "aoctech.app",
		RPOrigins:     cfg.RPOrigins,
	})
	if err != nil {
		log.Fatalf("initializing WebAuthn: %v", err)
	}

	// Repositories
	passkeyRepo := passKeyDomain.NewRepository(db, cfg.TablePrefix)

	// Services
	userSvc := userDomain.NewService(userRepo)
	sessionSvc := sessionDomain.NewService(sessionRepo)
	scopesCatalogSvc := scopesPkg.NewCatalogService(scopesRepo, valkeyClient)
	oauthClientSvc := oauthclientDomain.NewService(oauthClientRepo, scopesCatalogSvc)
	consentSvc := consentDomain.NewService(consentRepo)
	totpSvc := totpDomain.NewService(db, cfg.TablePrefix)
	apiKeySvc := apikeyDomain.NewService(apiKeyRepo)
	// KYC document uploads need a bucket; without one, KYC submission is
	// unavailable entirely (see kyc.Service.DocumentsEnabled) — verification is
	// document-only now.
	var kycPresigner kycDomain.Presigner
	if cfg.KYCDocumentsBucket != "" {
		s3Cli, err := storage.NewS3(context.Background(), cfg.AWSRegion, cfg.KYCDocumentsBucket)
		if err != nil {
			log.Fatalf("initializing KYC document storage: %v", err)
		}
		kycPresigner = s3Cli
	} else {
		log.Println("KYC_DOCUMENTS_BUCKET not set — document verification disabled")
	}

	kycSvc := kycDomain.NewService(kycRepo, kycPresigner)
	passkeySvc := passKeyDomain.NewService(wa, passkeyRepo, valkeyClient)

	// Email client (optional — only active when FROM_EMAIL is set)
	var emailCli *email.Client
	if cfg.FromEmail != "" {
		emailCli, err = email.New(ctx, cfg.AWSRegion, cfg.FromEmail, cfg.AppURL)
		if err != nil {
			log.Printf("warning: email client init failed: %v (email sending disabled)", err)
			emailCli = nil
		}
	}

	// Handlers
	wellknownH := handler.NewWellKnownHandler(jwtSvc, cfg.BaseURL)
	auditRepo := auditDomain.NewRepository(db, cfg.TablePrefix)
	auditSvc := auditDomain.NewService(auditRepo)
	authH := handler.NewAuthHandler(userSvc, sessionSvc, totpSvc, passkeySvc, oauthClientRepo, valkeyClient, cfg, emailCli, auditSvc)
	socialH := handler.NewSocialHandler(userSvc, sessionSvc, valkeyClient, cfg, auditSvc)
	authorizeH := handler.NewAuthorizeHandler(oauthClientRepo, authCodeRepo, sessionSvc, consentSvc, userSvc, valkeyClient, cfg.AppURL, cfg.BaseURL, cfg.CookieDomain, auditSvc)
	tokenH := handler.NewTokenHandler(oauthClientRepo, authCodeRepo, sessionSvc, userSvc, apiKeySvc, scopesCatalogSvc, jwtSvc, cfg.BaseURL, cfg, auditSvc)
	userinfoH := handler.NewUserInfoHandler(userSvc)
	sessionsH := handler.NewSessionsHandler(sessionSvc, auditSvc)
	profileH := handler.NewProfileHandler(userSvc, sessionSvc, auditSvc)
	apiKeysH := handler.NewAPIKeysHandler(apiKeySvc, scopesCatalogSvc, auditSvc)
	oauthClientsH := handler.NewOAuthClientsHandler(oauthClientSvc, auditSvc)
	consentsH := handler.NewConsentsHandler(consentSvc, oauthClientRepo, auditSvc)
	mfaH := handler.NewMFAHandler(totpSvc, userSvc, cfg, auditSvc)
	activityH := handler.NewActivityHandler(auditSvc)
	kycH := handler.NewKYCHandler(kycSvc, auditSvc)
	termsH := handler.NewTermsHandler(userSvc, auditSvc)
	stepUpH := handler.NewStepUpHandler(sessionSvc, totpSvc, passkeySvc, valkeyClient, auditSvc)
	passkeyH := handler.NewPasskeyHandler(passkeySvc, userSvc, sessionSvc, totpSvc, valkeyClient, cfg, auditSvc)

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
			if problem, ok := errors.AsType[*apierror.Problem](err); ok {
				return problem.Send(c)
			}
			if fiberErr, ok := errors.AsType[*fiber.Error](err); ok {
				return apierror.NewFromFiber(fiberErr, c.Path()).Send(c)
			}
			log.Printf("internal server error request_id=%s path=%s: %v", requestid.FromContext(c), c.Path(), err)
			return apierror.ServerError(c.Path()).Send(c)
		},
	})

	app.Use(recover.New())
	app.Use(requestid.New())
	app.Use(logger.New(logger.Config{
		Format: `{"time":"${time}","method":"${method}","path":"${path}","status":${status},"latency":"${latency}","request_id":"${requestid}"}` + "\n",
	}))

	rawOrigins := append([]string{cfg.AppURL}, cfg.AllowedOrigins...)
	allowedOrigins := rawOrigins[:0]
	for _, o := range rawOrigins {
		if strings.HasPrefix(o, "http://") || strings.HasPrefix(o, "https://") {
			if !slices.Contains(allowedOrigins, o) {
				allowedOrigins = append(allowedOrigins, o)
			} else {
				log.Printf("WARN: origins %s already present", o)
			}
		} else {
			log.Printf("WARN: skipping invalid CORS origin %q (missing http/https scheme)", o)
		}
	}
	if len(allowedOrigins) == 0 {
		log.Fatal("FATAL: no valid CORS origins configured — check BASE_URL and ALLOWED_ORIGINS SSM parameters")
	}

	app.Use(cors.New(cors.Config{
		AllowOrigins:     allowedOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "X-Request-ID"},
		AllowCredentials: true,
		MaxAge:           3600,
	}))

	wellknownH.Register(app)

	v1 := app.Group("/v1.0")
	v1.Get("/health-check", healthHandler(db, database.TableName(cfg.TablePrefix, "account_users"), valkeyClient, cfg.AppVersion, valkeyRequired))

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
	tokenLimiter := middleware.RateLimit(middleware.RateLimitConfig{
		Cache: valkeyClient, Prefix: "token", Max: middleware.FailedLoginMax,
		Window: middleware.FailedLoginWindow, KeyFunc: ipKey, CountOnlyFailures: true, FailClosed: true,
	})
	perUserLimiter := middleware.RateLimit(middleware.RateLimitConfig{
		Cache: valkeyClient, Prefix: "user", Max: middleware.PerUserMax,
		Window: middleware.PerUserWindow, KeyFunc: middleware.GetUserID, CountOnlyFailures: false, FailClosed: true,
	})
	v1.Use("/auth/login", authLimiter)
	v1.Use("/auth/mfa/challenge", authLimiter)
	v1.Use("/auth/forgot-password", pwResetLimiter)
	v1.Use("/auth/reset-password", pwResetLimiter)
	v1.Use("/auth/resend-verification", pwResetLimiter)
	v1.Use("/token", tokenLimiter)

	handler.NewScopesHandler(scopesCatalogSvc).Register(v1)
	authH.Register(v1)
	socialH.Register(v1)
	authorizeH.Register(v1)
	tokenH.Register(v1)
	stepUpH.Register(v1, middleware.RequireAuth(jwtSvc), middleware.RequireClientID(cfg.SelfClientID))
	passkeyH.RegisterAuth(v1.Group("/auth"))
	v1.Get("/userinfo", middleware.RequireAuth(jwtSvc), userinfoH.UserInfo)

	account := v1.Group("/account", middleware.RequireAuth(jwtSvc), middleware.RequireClientID(cfg.SelfClientID), perUserLimiter)
	stepUp := middleware.RequireRecentMFA(middleware.StepUpMaxAge)
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
	kycH.RegisterInternalGet(v1, middleware.RequireAuth(jwtSvc), middleware.RequireInternalScope(scopesPkg.InternalAccountKYC))

	port := fmt.Sprintf(":%s", cfg.Port)
	log.Printf("ctech-account starting on %s (env=%s)", port, cfg.Environment)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	go func() {
		if err := app.Listen(port); err != nil {
			log.Printf("server error: %v", err)
		}
	}()

	<-quit
	log.Println("shutting down gracefully...")
	if err := app.ShutdownWithTimeout(10 * time.Second); err != nil {
		log.Printf("shutdown error: %v", err)
	}
	log.Println("server stopped")
}
