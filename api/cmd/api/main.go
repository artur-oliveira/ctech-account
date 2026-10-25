package main

import (
	"log/slog"
	"os"

	"gopkg.aoctech.app/account/api/internal/app"
	"gopkg.aoctech.app/account/api/internal/config"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("loading config", "error", err)
		os.Exit(1)
	}
	app.Run(cfg)
}
