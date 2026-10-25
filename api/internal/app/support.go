package app

import (
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"go.uber.org/fx"

	"gopkg.aoctech.app/account/api/internal/config"
	supportDomain "gopkg.aoctech.app/account/api/internal/domain/support"
	userDomain "gopkg.aoctech.app/account/api/internal/domain/user"
	"gopkg.aoctech.app/account/api/internal/email"
	"gopkg.aoctech.app/account/api/internal/handler"
	"gopkg.aoctech.app/account/api/internal/turnstile"
	commonws "gopkg.aoctech.app/api-commons/ws"
)

// SupportModule: support tickets, their live socket notifier and handlers.
var SupportModule = fx.Module("support",
	fx.Provide(
		func(db *dynamodb.Client, cfg *config.Config, ws commonws.Registry) *supportDomain.Service {
			svc := supportDomain.NewService(supportDomain.NewRepository(db, cfg.TablePrefix))
			svc.SetNotifier(handler.NewSupportWSNotifier(ws))
			return svc
		},
		func(svc *supportDomain.Service, users *userDomain.Service, cfg *config.Config, mail *email.Client) *handler.SupportHandler {
			return handler.NewSupportHandler(svc, users, turnstile.New(cfg.TurnstileSecretKey, cfg.AppURL), mail, cfg.AppURL)
		},
		func(svc *supportDomain.Service, users *userDomain.Service, mail *email.Client, cfg *config.Config) *handler.SupportAdminHandler {
			return handler.NewSupportAdminHandler(svc, users, mail, cfg.AppURL)
		},
	),
)
