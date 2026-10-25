package app

import (
	"context"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"go.uber.org/fx"

	"gopkg.aoctech.app/account/api/internal/config"
	apikeyDomain "gopkg.aoctech.app/account/api/internal/domain/apikey"
	auditDomain "gopkg.aoctech.app/account/api/internal/domain/audit"
	"gopkg.aoctech.app/account/api/internal/domain/dataexport"
	consentDomain "gopkg.aoctech.app/account/api/internal/domain/oauth/consent"
	sessionDomain "gopkg.aoctech.app/account/api/internal/domain/session"
	userDomain "gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/account/api/internal/email"
	"gopkg.aoctech.app/account/api/internal/erasurepub"
	"gopkg.aoctech.app/account/api/internal/handler"
	"gopkg.aoctech.app/account/api/internal/storage"
	"gopkg.aoctech.app/api-commons/awsconfig"
)

// ExportModule: personal data export. Both results are nil when it is off.
var ExportModule = fx.Module("export",
	fx.Provide(
		newExportService,
		func(svc *dataexport.Service, users *userDomain.Service) *handler.DataExportHandler {
			if svc == nil {
				return nil
			}
			return handler.NewDataExportHandler(svc, users)
		},
	),
)

// newExportService is built before the deletion service: the purge of a
// deleted account removes its export files and row too.
func newExportService(db *dynamodb.Client, cfg *config.Config, userSvc *userDomain.Service, sessionSvc *sessionDomain.Service,
	apiKeySvc *apikeyDomain.Service, consentSvc *consentDomain.Service, auditSvc *auditDomain.Service, emailCli *email.Client) *dataexport.Service {
	if cfg.ExportsBucket == "" || emailCli == nil {
		slog.Info("EXPORTS_BUCKET or FROM_EMAIL not set — personal data export disabled")
		return nil
	}
	ctx := context.Background()
	exportStore, err := storage.NewS3(ctx, cfg.AWSRegion, cfg.ExportsBucket)
	if err != nil {
		fatal("initializing export storage", "error", err)
	}
	exportSvc := dataexport.NewService(
		dataexport.NewRepository(db, cfg.TablePrefix), exportStore,
		dataexport.NewZipBuilder(dataexport.AccountSections(dataexport.Sources{
			Users: userSvc, Sessions: sessionSvc, Keys: apiKeySvc, Consents: consentSvc, Audit: auditSvc})...),
		dataexport.UserContacts{Users: userSvc}, emailCli, dataexport.AuditRecorder{Audit: auditSvc})
	// Participants are asked over the erasure topic (routed by its "services"
	// attribute); only those marked "export" in ERASURE_PARTICIPANTS are asked.
	if services := cfg.ExportServices(); len(services) > 0 {
		if cfg.ErasureTopicARN == "" {
			fatal("ERASURE_PARTICIPANTS has export participants but ACCOUNT_ERASURE_TOPIC_ARN is empty")
		}
		awsCfg, awsErr := awsconfig.Load(ctx, cfg.AWSRegion)
		if awsErr != nil {
			fatal("loading AWS config for SNS", "error", awsErr)
		}
		exportSvc = exportSvc.WithParticipants(services,
			erasurepub.ExportPublisher{SNS: erasurepub.New(sns.NewFromConfig(awsCfg), cfg.ErasureTopicARN)})
	}
	return exportSvc
}
