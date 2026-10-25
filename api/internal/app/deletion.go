package app

import (
	"context"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"go.uber.org/fx"

	"gopkg.aoctech.app/account/api/internal/config"
	"gopkg.aoctech.app/account/api/internal/crypto"
	apikeyDomain "gopkg.aoctech.app/account/api/internal/domain/apikey"
	auditDomain "gopkg.aoctech.app/account/api/internal/domain/audit"
	companyDomain "gopkg.aoctech.app/account/api/internal/domain/company"
	"gopkg.aoctech.app/account/api/internal/domain/dataexport"
	"gopkg.aoctech.app/account/api/internal/domain/deletion"
	kycDomain "gopkg.aoctech.app/account/api/internal/domain/kyc"
	passKeyDomain "gopkg.aoctech.app/account/api/internal/domain/mfa/passkey"
	totpDomain "gopkg.aoctech.app/account/api/internal/domain/mfa/totp"
	oauthclientDomain "gopkg.aoctech.app/account/api/internal/domain/oauth/client"
	consentDomain "gopkg.aoctech.app/account/api/internal/domain/oauth/consent"
	orgDomain "gopkg.aoctech.app/account/api/internal/domain/organization"
	"gopkg.aoctech.app/account/api/internal/domain/planlimit"
	sessionDomain "gopkg.aoctech.app/account/api/internal/domain/session"
	supportDomain "gopkg.aoctech.app/account/api/internal/domain/support"
	userDomain "gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/account/api/internal/email"
	"gopkg.aoctech.app/account/api/internal/erasurepub"
	"gopkg.aoctech.app/account/api/internal/handler"
	"gopkg.aoctech.app/account/api/internal/storage"
	"gopkg.aoctech.app/api-commons/awsconfig"
	commoncache "gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/supervisor"
)

// erasureSubjectReader is the participant allowed to read the CPF of a purging
// user (dfe e-CPF certificates are keyed by CPF).
const erasureSubjectReader = "dfe"

// DeletionModule: account deletion and service unlink. Handler is nil while
// ACCOUNT_DELETION_ENABLED is off.
var DeletionModule = fx.Module("deletion",
	fx.Provide(newDeletionHandler),
)

// DeletionHandler wraps the optional handler so fx can hand it around as a
// non-nil value.
type DeletionHandler struct{ *handler.DeletionHandler }

type deletionDeps struct {
	fx.In
	DB        *dynamodb.Client
	Cfg       *config.Config
	W         *Workers
	JWT       *crypto.JWTService
	Sealer    *crypto.Sealer
	Email     *email.Client
	KYCDocs   kycDomain.Presigner
	Export    *dataexport.Service
	Users     *userDomain.Service
	Sessions  *sessionDomain.Service
	APIKeys   *apikeyDomain.Service
	Consents  *consentDomain.Service
	Audit     *auditDomain.Service
	TOTP      *totpDomain.Service
	Passkeys  *passKeyDomain.Service
	Orgs      *orgDomain.Service
	Companies companyDomain.Repository
	Clients   oauthclientDomain.Repository
	Token     *handler.TokenHandler
}

func newDeletionHandler(d deletionDeps) *DeletionHandler {
	cfg := d.Cfg
	if !cfg.AccountDeletionEnabled {
		return &DeletionHandler{}
	}
	ctx := context.Background()
	if d.Email == nil {
		fatal("ACCOUNT_DELETION_ENABLED requires the e-mail client (FROM_EMAIL / SES)")
	}
	var revocation commoncache.Backend = commoncache.NewMemoryBackend(10000)
	if cfg.ValkeyURL != "" {
		// DB 0 = the base URL, where every service reads revocations.
		var err error
		if revocation, err = commoncache.NewRedisBackend(cfg.ValkeyURL); err != nil {
			fatal("connecting revocation backend", "error", err)
		}
	}
	d.JWT.SetRevocation(revocation)
	var publisher deletion.Publisher
	if len(cfg.ErasureServiceNames()) > 0 {
		if cfg.ErasureTopicARN == "" {
			fatal("ERASURE_PARTICIPANTS is set but ACCOUNT_ERASURE_TOPIC_ARN is empty")
		}
		awsCfg, awsErr := awsconfig.Load(ctx, cfg.AWSRegion)
		if awsErr != nil {
			fatal("loading AWS config for SNS", "error", awsErr)
		}
		publisher = erasurepub.New(sns.NewFromConfig(awsCfg), cfg.ErasureTopicARN)
	}
	// Leaving one product (scope service): the OAuth clients people sign in to
	// it with come from ERASURE_PARTICIPANTS; a product without any cannot be left.
	loginClients := func(service string) []string {
		p, _ := cfg.LeavableService(service)
		return p.LoginClients
	}
	deletionSvc := deletion.NewService(
		deletion.NewRepository(d.DB, cfg.TablePrefix), d.Users,
		deletion.NewAccountLocker(d.Users, d.Sessions, d.APIKeys, deletion.NewJWTRevoker(revocation), publisher, cfg.ErasureServiceNames()).
			WithServiceClients(loginClients, d.Sessions),
		d.Email,
	)
	var kycDocs deletion.KYCStore
	if s3Cli, ok := d.KYCDocs.(*storage.S3); ok {
		kycDocs = s3Cli
	}
	purgeUser := func(ctx context.Context, userID, requestID string, at time.Time) error {
		return d.Users.Purge(ctx, userID, requestID, func(cpf string) string { return d.Sealer.MAC("cpf-hmac", cpf) }, at)
	}
	eligibility := erasurepub.NewEligibility(&http.Client{}, cfg.ErasureParticipants, func(aud, scope string) (string, error) {
		now := time.Now().Unix()
		return d.JWT.SignAccessToken("ctech-account", "", cfg.SelfClientID, []string{scope}, cfg.AppURL, []string{aud}, now, now, nil, "")
	})
	deletionSvc = deletionSvc.
		WithOwnership(deletion.NewCompositeOwnership(
			deletion.NewAccountOwnership(d.Orgs, d.Clients), eligibility)).
		WithServices(func(name string) (deletion.ServiceTarget, bool) {
			p, ok := cfg.LeavableService(name)
			return deletion.ServiceTarget{Service: p.Service, LoginClients: p.LoginClients}, ok
		}, eligibility).
		WithServiceList(func() []string {
			var names []string
			for _, p := range cfg.ErasureParticipants {
				if len(p.LoginClients) > 0 {
					names = append(names, p.Service)
				}
			}
			return names
		}).
		WithServiceUnlinker(deletion.NewServiceUnlinker(d.Consents, d.Sessions, d.APIKeys, d.Users, d.Users, d.Audit, d.Email, loginClients)).
		WithPurger(deletion.NewAccountPurger(d.Orgs, d.Companies, d.Consents, d.Sessions, d.APIKeys, d.TOTP, d.Passkeys, purgeUser).
			WithDataErasers(orgDomain.NewEraser(d.DB, cfg.TablePrefix), companyDomain.NewEraser(d.DB, cfg.TablePrefix),
				supportDomain.NewRedactor(d.DB, cfg.TablePrefix), auditDomain.NewAnonymizer(d.DB, cfg.TablePrefix),
				kycDocs, d.Users).
			WithOwnerData(orgDomain.NewEraser(d.DB, cfg.TablePrefix), planlimit.NewEraser(d.DB, cfg.TablePrefix)).
			WithOwnerData(exportEraser(d.Export)...)).
		WithParticipants(cfg.ErasureServiceNames())
	// A person who left a product tells it they are back on their next sign-in
	// through one of its login clients (service unlink, spec § 7).
	d.Token.WithRelinker(deletion.NewRelinker(publisher, d.Users, loginClients))
	d.W.Start("deletion", supervisor.Every(deletion.WorkerInterval, func(ctx context.Context) error {
		deletionSvc.ProcessDue(ctx)
		return nil
	}))
	return &DeletionHandler{handler.NewDeletionHandler(deletionSvc, d.Users, d.Audit)}
}

// exportEraser is the export's part of a purge, or nothing when the export is off.
func exportEraser(svc *dataexport.Service) []deletion.OwnerEraser {
	if svc == nil {
		return nil
	}
	return []deletion.OwnerEraser{svc}
}
