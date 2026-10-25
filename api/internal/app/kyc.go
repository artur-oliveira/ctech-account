package app

import (
	"context"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"go.uber.org/fx"

	"gopkg.aoctech.app/account/api/internal/config"
	"gopkg.aoctech.app/account/api/internal/crypto"
	auditDomain "gopkg.aoctech.app/account/api/internal/domain/audit"
	kycDomain "gopkg.aoctech.app/account/api/internal/domain/kyc"
	risk "gopkg.aoctech.app/account/api/internal/domain/risk"
	userDomain "gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/account/api/internal/handler"
	"gopkg.aoctech.app/account/api/internal/storage"
)

// KYCModule: identity verification and its admin review handler.
var KYCModule = fx.Module("kyc",
	fx.Provide(
		newKYCPresigner,
		func(db *dynamodb.Client, cfg *config.Config, users userDomain.Repository) kycDomain.Repository {
			return kycDomain.NewRepository(db, cfg.TablePrefix, users)
		},
		func(repo kycDomain.Repository, p kycDomain.Presigner) *kycDomain.Service {
			return kycDomain.NewService(repo, p, risk.NoopEvaluator{})
		},
		func(svc *kycDomain.Service, auditSvc *auditDomain.Service, sealer *crypto.Sealer) *handler.KYCHandler {
			return handler.NewKYCHandler(svc, auditSvc).WithCPFMAC(func(cpf string) string { return sealer.MAC("cpf-hmac", cpf) })
		},
		handler.NewKYCAdminHandler,
	),
)

// newKYCPresigner: document uploads need a bucket; without one, Enhanced
// document verification is unavailable entirely (kyc.Service.DocumentsEnabled).
// The result is a nil interface then.
func newKYCPresigner(cfg *config.Config) kycDomain.Presigner {
	if cfg.KYCDocumentsBucket == "" {
		slog.Info("KYC_DOCUMENTS_BUCKET not set — Enhanced document verification disabled")
		return nil
	}
	s3Cli, err := storage.NewS3(context.Background(), cfg.AWSRegion, cfg.KYCDocumentsBucket)
	if err != nil {
		fatal("initializing KYC document storage", "error", err)
	}
	return s3Cli
}
