// Package app is the account API's fx container: one module per domain, each
// providing its repositories, services and handlers, plus the HTTP module that
// mounts them. cmd/api/main.go only loads the config and runs it.
package app

import (
	"context"
	"log/slog"
	"os"
	"time"

	"go.uber.org/fx"

	"gopkg.aoctech.app/account/api/internal/config"
)

// fatal logs through slog (JSON, like every other line) and exits.
func fatal(msg string, args ...any) {
	slog.Error(msg, args...)
	os.Exit(1)
}

// Modules lists every module; the order only decides Invoke order (worker
// starts, then route registration, then the listener last).
func Modules(cfg *config.Config) fx.Option {
	return fx.Options(
		fx.NopLogger,                   // slog is the only log stream
		fx.StopTimeout(30*time.Second), // server drain 10s + worker drain 10s
		fx.Supply(cfg),
		InfraModule,
		IdentityModule,
		OrgModule,
		KYCModule,
		SupportModule,
		ExportModule,
		DeletionModule,
		HTTPModule,
	)
}

// Run builds the container, blocks until SIGINT/SIGTERM, then drains.
func Run(cfg *config.Config) {
	a := fx.New(Modules(cfg))
	if err := a.Err(); err != nil {
		fatal("building application", "error", err)
	}
	if err := a.Start(context.Background()); err != nil {
		fatal("starting application", "error", err)
	}
	<-a.Done()
	slog.Info("shutting down gracefully...")
	if err := a.Stop(context.Background()); err != nil {
		slog.Error("shutdown error", "error", err)
	}
	slog.Info("server stopped")
}
