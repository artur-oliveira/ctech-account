package app

import (
	"context"
	"log/slog"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/go-webauthn/webauthn/webauthn"
	"go.uber.org/fx"

	"gopkg.aoctech.app/account/api/internal/cache"
	"gopkg.aoctech.app/account/api/internal/config"
	"gopkg.aoctech.app/account/api/internal/crypto"
	apikeyDomain "gopkg.aoctech.app/account/api/internal/domain/apikey"
	auditDomain "gopkg.aoctech.app/account/api/internal/domain/audit"
	passKeyDomain "gopkg.aoctech.app/account/api/internal/domain/mfa/passkey"
	totpDomain "gopkg.aoctech.app/account/api/internal/domain/mfa/totp"
	oauthclientDomain "gopkg.aoctech.app/account/api/internal/domain/oauth/client"
	authcodeDomain "gopkg.aoctech.app/account/api/internal/domain/oauth/code"
	consentDomain "gopkg.aoctech.app/account/api/internal/domain/oauth/consent"
	orgDomain "gopkg.aoctech.app/account/api/internal/domain/organization"
	sessionDomain "gopkg.aoctech.app/account/api/internal/domain/session"
	userDomain "gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/account/api/internal/email"
	"gopkg.aoctech.app/account/api/internal/handler"
	scopesPkg "gopkg.aoctech.app/account/api/internal/scopes"
)

// IdentityModule: users, sessions, OAuth (clients, codes, consent, scopes),
// API keys, MFA, audit and the e-mail client, with their handlers.
var IdentityModule = fx.Module("identity",
	fx.Provide(
		func(db *dynamodb.Client, cfg *config.Config) userDomain.Repository {
			return userDomain.NewRepository(db, cfg.TablePrefix)
		},
		userDomain.NewService,
		func(db *dynamodb.Client, cfg *config.Config) sessionDomain.Repository {
			return sessionDomain.NewRepository(db, cfg.TablePrefix)
		},
		sessionDomain.NewService,
		func(db *dynamodb.Client, cfg *config.Config) oauthclientDomain.Repository {
			return oauthclientDomain.NewRepository(db, cfg.TablePrefix)
		},
		authcodeDomain.NewRepository,
		func(db *dynamodb.Client, cfg *config.Config) apikeyDomain.Repository {
			return apikeyDomain.NewRepository(db, cfg.TablePrefix)
		},
		apikeyDomain.NewService,
		func(db *dynamodb.Client, cfg *config.Config) consentDomain.Repository {
			return consentDomain.NewRepository(db, cfg.TablePrefix)
		},
		consentDomain.NewService,
		newScopes,
		func(repo oauthclientDomain.Repository, catalog *scopesPkg.CatalogService) *oauthclientDomain.Service {
			return oauthclientDomain.NewService(repo, catalog)
		},
		func(repo oauthclientDomain.Repository, catalog *scopesPkg.CatalogService) *oauthclientDomain.OperatorService {
			return oauthclientDomain.NewOperatorService(repo, catalog)
		},
		func(db *dynamodb.Client, cfg *config.Config) passKeyDomain.Repository {
			return passKeyDomain.NewRepository(db, cfg.TablePrefix)
		},
		newWebAuthn,
		passKeyDomain.NewService,
		func(db *dynamodb.Client, s *crypto.Sealer, cfg *config.Config) *totpDomain.Service {
			return totpDomain.NewService(db, s, cfg.TablePrefix)
		},
		func(db *dynamodb.Client, cfg *config.Config) auditDomain.Repository {
			return auditDomain.NewRepository(db, cfg.TablePrefix)
		},
		auditDomain.NewService,
		newEmail,

		newWellKnownHandler,
		newAuthHandler,
		handler.NewSocialHandler,
		newAuthorizeHandler,
		newTokenHandler,
		handler.NewUserInfoHandler,
		handler.NewSessionsHandler,
		newProfileHandler,
		handler.NewAPIKeysHandler,
		handler.NewOAuthClientsHandler,
		handler.NewConsentsHandler,
		newMFAHandler,
		handler.NewActivityHandler,
		handler.NewTermsHandler,
		newStepUpHandler,
		handler.NewPasskeyHandler,
		newScopeRegistryHandler,
		handler.NewScopesHandler,
	),
	fx.Invoke(bootstrapAccountResource),
)

// scopesOut: the scope repository's type is unexported and satisfies two
// interfaces, so its two services are built together.
type scopesOut struct {
	fx.Out
	Catalog  *scopesPkg.CatalogService
	Registry *scopesPkg.RegistryService
}

func newScopes(db *dynamodb.Client, cfg *config.Config, valkey *cache.Client) scopesOut {
	repo := scopesPkg.NewRepository(db, cfg.TablePrefix)
	return scopesOut{
		Catalog:  scopesPkg.NewCatalogService(repo, valkey),
		Registry: scopesPkg.NewRegistryService(repo, valkey),
	}
}

// newWebAuthn builds the WebAuthn Relying Party.
func newWebAuthn(cfg *config.Config) *webauthn.WebAuthn {
	wa, err := webauthn.New(&webauthn.Config{
		RPID:          cfg.RPID,
		RPDisplayName: "aoctech.app",
		RPOrigins:     cfg.RPOrigins,
	})
	if err != nil {
		fatal("initializing WebAuthn", "error", err)
	}
	return wa
}

// newEmail: optional, only active when FROM_EMAIL is set; nil otherwise.
func newEmail(cfg *config.Config) *email.Client {
	if cfg.FromEmail == "" {
		return nil
	}
	c, err := email.New(context.Background(), cfg.AWSRegion, cfg.FromEmail, cfg.AppURL)
	if err != nil {
		slog.Warn("email client init failed (email sending disabled)", "error", err)
		return nil
	}
	return c
}

func newWellKnownHandler(jwtSvc *crypto.JWTService, cfg *config.Config) *handler.WellKnownHandler {
	return handler.NewWellKnownHandler(jwtSvc, cfg.BaseURL, cfg.AppURL, cfg.Audience)
}

func newAuthHandler(userSvc *userDomain.Service, sessionSvc *sessionDomain.Service, totpSvc *totpDomain.Service,
	clients oauthclientDomain.Repository, valkey *cache.Client, cfg *config.Config, emailCli *email.Client, auditSvc *auditDomain.Service) *handler.AuthHandler {
	return handler.NewAuthHandler(userSvc, sessionSvc, totpSvc, clients, valkey, cfg, emailCli, auditSvc)
}

func newAuthorizeHandler(clients oauthclientDomain.Repository, codes *authcodeDomain.Repository, sessionSvc *sessionDomain.Service,
	consentSvc *consentDomain.Service, userSvc *userDomain.Service, valkey *cache.Client, cfg *config.Config, auditSvc *auditDomain.Service) *handler.AuthorizeHandler {
	return handler.NewAuthorizeHandler(clients, codes, sessionSvc, consentSvc, userSvc, valkey, cfg.AppURL, cfg.BaseURL, cfg.CookieDomain, auditSvc)
}

func newTokenHandler(clients oauthclientDomain.Repository, codes *authcodeDomain.Repository, sessionSvc *sessionDomain.Service,
	userSvc *userDomain.Service, apiKeySvc *apikeyDomain.Service, catalog *scopesPkg.CatalogService, jwtSvc *crypto.JWTService,
	cfg *config.Config, auditSvc *auditDomain.Service) *handler.TokenHandler {
	return handler.NewTokenHandler(clients, codes, sessionSvc, userSvc, apiKeySvc, catalog, jwtSvc, cfg.AppURL, cfg, auditSvc)
}

func newProfileHandler(userSvc *userDomain.Service, sessionSvc *sessionDomain.Service, auditSvc *auditDomain.Service, orgSvc *orgDomain.Service) *handler.ProfileHandler {
	return handler.NewProfileHandler(userSvc, sessionSvc, auditSvc).WithOrganizations(orgSvc)
}

func newMFAHandler(totpSvc *totpDomain.Service, userSvc *userDomain.Service, cfg *config.Config, auditSvc *auditDomain.Service) *handler.MFAHandler {
	return handler.NewMFAHandler(totpSvc, userSvc, cfg, auditSvc)
}

func newStepUpHandler(sessionSvc *sessionDomain.Service, totpSvc *totpDomain.Service, passkeySvc *passKeyDomain.Service,
	valkey *cache.Client, auditSvc *auditDomain.Service) *handler.StepUpHandler {
	return handler.NewStepUpHandler(sessionSvc, totpSvc, passkeySvc, valkey, auditSvc)
}

func newScopeRegistryHandler(registry *scopesPkg.RegistryService, clients oauthclientDomain.Repository,
	operator *oauthclientDomain.OperatorService, auditSvc *auditDomain.Service) *handler.ScopeRegistryHandler {
	return handler.NewScopeRegistryHandler(registry, clients, operator, auditSvc)
}

// bootstrapAccountResource registers the Account Resource Server and the
// first-party frontend OAuth client; the process refuses to boot without them.
func bootstrapAccountResource(cfg *config.Config, registry *scopesPkg.RegistryService, operator *oauthclientDomain.OperatorService) {
	ctx := context.Background()
	accountResource, accountResourceChanged, err := registry.BootstrapAccount(ctx, cfg.Audience, cfg.AppVersion)
	if err != nil {
		fatal("bootstrapping Account Resource Server", "error", err)
	}
	slog.Info("Account Resource Server ready", "revision", accountResource.Revision, "changed", accountResourceChanged, "audience", accountResource.Audience)

	selfScopes := append([]string{scopesPkg.OpenID, scopesPkg.Profile, scopesPkg.Email}, scopesPkg.AccountUserScopes()...)
	selfClient, selfClientChanged, err := operator.EnsureFirstPartyPublicClient(
		ctx,
		cfg.SelfClientID,
		"CTech Account",
		strings.TrimRight(cfg.AppURL, "/")+"/login/callback",
		selfScopes,
		nil, // the Account SPA never calls another service's API; its own audience is always prepended
	)
	if err != nil {
		fatal("bootstrapping Account frontend OAuth client", "error", err)
	}
	slog.Info("Account frontend OAuth client ready", "client_id", selfClient.ID(), "changed", selfClientChanged, "scopes", len(selfClient.AllowedScopes))
}
