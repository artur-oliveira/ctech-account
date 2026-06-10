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

	"github.com/artur-oliveira/ctech-account/internal/apierror"
	"github.com/artur-oliveira/ctech-account/internal/cache"
	"github.com/artur-oliveira/ctech-account/internal/config"
	"github.com/artur-oliveira/ctech-account/internal/crypto"
	"github.com/artur-oliveira/ctech-account/internal/database"
	apikeyDomain "github.com/artur-oliveira/ctech-account/internal/domain/apikey"
	passKeyDomain "github.com/artur-oliveira/ctech-account/internal/domain/mfa/passkey"
	totpDomain "github.com/artur-oliveira/ctech-account/internal/domain/mfa/totp"
	oauthclientDomain "github.com/artur-oliveira/ctech-account/internal/domain/oauth/client"
	authcodeDomain "github.com/artur-oliveira/ctech-account/internal/domain/oauth/code"
	sessionDomain "github.com/artur-oliveira/ctech-account/internal/domain/session"
	userDomain "github.com/artur-oliveira/ctech-account/internal/domain/user"
	"github.com/artur-oliveira/ctech-account/internal/email"
	"github.com/artur-oliveira/ctech-account/internal/handler"
	"github.com/artur-oliveira/ctech-account/internal/middleware"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/logger"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/gofiber/fiber/v3/middleware/requestid"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("loading config: %v", err)
	}

	ctx := context.Background()

	db, err := database.New(ctx, cfg.AWSRegion, cfg.TablePrefix)
	if err != nil {
		log.Fatalf("connecting to DynamoDB: %v", err)
	}

	valkeyClient, err := cache.New(cfg.ValkeyURL)
	if err != nil {
		log.Fatalf("connecting to Valkey: %v", err)
	}
	defer valkeyClient.Close()

	jwtSvc, err := crypto.NewJWTService(cfg)
	if err != nil {
		log.Fatalf("initializing JWT service: %v", err)
	}

	// Repositories
	userRepo := userDomain.NewRepository(db)
	sessionRepo := sessionDomain.NewRepository(db)
	oauthClientRepo := oauthclientDomain.NewRepository(db)
	authCodeRepo := authcodeDomain.NewRepository(valkeyClient)
	apiKeyRepo := apikeyDomain.NewRepository(db)

	// WebAuthn Relying Party
	wa, err := webauthn.New(&webauthn.Config{
		RPID:          cfg.RPID,
		RPDisplayName: "arturocarvalho.com",
		RPOrigins:     cfg.RPOrigins,
	})
	if err != nil {
		log.Fatalf("initializing WebAuthn: %v", err)
	}

	// Repositories
	passkeyRepo := passKeyDomain.NewRepository(db)

	// Services
	userSvc := userDomain.NewService(userRepo)
	sessionSvc := sessionDomain.NewService(sessionRepo)
	totpSvc := totpDomain.NewService(db)
	apiKeySvc := apikeyDomain.NewService(apiKeyRepo)
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
	authH := handler.NewAuthHandler(userSvc, sessionSvc, totpSvc, passkeySvc, valkeyClient, cfg, emailCli)
	socialH := handler.NewSocialHandler(userSvc, sessionSvc, valkeyClient, cfg)
	authorizeH := handler.NewAuthorizeHandler(oauthClientRepo, authCodeRepo, sessionSvc, cfg.AppURL, cfg.CookieDomain)
	tokenH := handler.NewTokenHandler(oauthClientRepo, authCodeRepo, sessionSvc, userSvc, jwtSvc, cfg.BaseURL, cfg)
	userinfoH := handler.NewUserInfoHandler(userSvc)
	sessionsH := handler.NewSessionsHandler(sessionSvc)
	profileH := handler.NewProfileHandler(userSvc)
	apiKeysH := handler.NewAPIKeysHandler(apiKeySvc)
	mfaH := handler.NewMFAHandler(totpSvc, userSvc, cfg)
	passkeyH := handler.NewPasskeyHandler(passkeySvc, userSvc, sessionSvc, totpSvc, valkeyClient, cfg)
	internalH := handler.NewInternalHandler(userSvc, cfg.InternalToken)

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

	app.Get("/healthz", healthHandler(db, valkeyClient))

	wellknownH.Register(app)
	internalH.Register(app)

	v1 := app.Group("/v1.0")
	authH.Register(v1)
	socialH.Register(v1)
	authorizeH.Register(v1)
	tokenH.Register(v1)
	passkeyH.RegisterAuth(v1.Group("/auth"))
	v1.Get("/userinfo", middleware.RequireAuth(jwtSvc), userinfoH.UserInfo)

	account := v1.Group("/account", middleware.RequireAuth(jwtSvc))
	profileH.Register(account)
	sessionsH.Register(account)
	apiKeysH.Register(account)
	mfaH.Register(account)
	passkeyH.RegisterManagement(account)

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
