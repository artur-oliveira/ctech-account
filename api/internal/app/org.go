package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"go.uber.org/fx"

	"gopkg.aoctech.app/account/api/internal/billingclient"
	"gopkg.aoctech.app/account/api/internal/config"
	"gopkg.aoctech.app/account/api/internal/crypto"
	companyDomain "gopkg.aoctech.app/account/api/internal/domain/company"
	"gopkg.aoctech.app/account/api/internal/domain/company/registry"
	oauthclientDomain "gopkg.aoctech.app/account/api/internal/domain/oauth/client"
	orgDomain "gopkg.aoctech.app/account/api/internal/domain/organization"
	"gopkg.aoctech.app/account/api/internal/domain/planlimit"
	userDomain "gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/account/api/internal/handler"
	scopesPkg "gopkg.aoctech.app/account/api/internal/scopes"
	"gopkg.aoctech.app/api-commons/supervisor"
)

// OrgModule: organizations, their companies, plan limits and the handoff.
var OrgModule = fx.Module("org",
	fx.Provide(
		func(db *dynamodb.Client, cfg *config.Config) companyDomain.Repository {
			return companyDomain.NewRepository(db, cfg.TablePrefix)
		},
		func(repo companyDomain.Repository) *companyDomain.Service {
			return companyDomain.NewService(repo, time.Now)
		},
		newOrgService,
		handler.NewHandoffHandler,
		handler.NewOrganizationHandler,
		func(c *companyDomain.Service, o *orgDomain.Service, u *userDomain.Service) *handler.CompanyHandler {
			return handler.NewCompanyHandler(c, o, u, registry.NewCNPJA(nil))
		},
	),
)

func newOrgService(db *dynamodb.Client, cfg *config.Config, companySvc *companyDomain.Service, userSvc *userDomain.Service,
	jwtSvc *crypto.JWTService, clients oauthclientDomain.Repository, catalog *scopesPkg.CatalogService, w *Workers) *orgDomain.Service {
	orgSvc := orgDomain.NewService(orgDomain.NewRepository(db, cfg.TablePrefix), time.Now)
	// An accepted invitation grants the companies it named. Wired here rather
	// than injected into the organization package, which stays ignorant that
	// companies exist (ctech-billing ADR 0023).
	orgSvc = orgSvc.WithActorGranter(
		func(ctx context.Context, orgID, companyID, userID, name, grantedBy string) error {
			return companySvc.GrantActor(ctx, orgID, companyID, userID, name, grantedBy)
		})

	// Plan limits on personal spaces. Dark until BILLING_API_URL is set, which
	// is deploy step 2 (docs/specs/2026-10-10-space-plan-limits.md).
	if cfg.BillingAPIURL == "" {
		slog.Info("BILLING_API_URL not set — plan limits on personal spaces disabled")
		return orgSvc
	}
	billingTokens := billingclient.NewSelfSigned(jwtSvc, clients, catalog, cfg.AppURL, cfg.Audience, time.Now)
	planSvc := planlimit.NewService(billingclient.New(cfg.BillingAPIURL, billingTokens),
		planlimit.NewQueue(db, cfg.TablePrefix), time.Now).WithCounter(orgSvc)
	orgSvc = orgSvc.
		WithPlanLimits(planSvc, orgDomain.NewCounterRepository(db, cfg.TablePrefix)).
		WithEmailOwner(func(ctx context.Context, email string) (string, error) {
			u, err := userSvc.GetByEmail(ctx, email)
			if errors.Is(err, userDomain.ErrNotFound) {
				return "", nil
			}
			if err != nil {
				return "", err
			}
			return u.ID(), nil
		})
	// Startup diagnostic: runs on every instance (not a leader-only worker).
	go func() { _ = planSvc.CheckBilling(w.Ctx) }()
	w.Start("plan-levels", supervisor.Every(planlimit.WorkerInterval, func(ctx context.Context) error {
		planSvc.ProcessDue(ctx)
		return nil
	}))
	return orgSvc
}
