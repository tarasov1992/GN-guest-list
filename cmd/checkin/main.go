package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	_ "time/tzdata" // embedded time zone data, so TZ=Europe/Moscow works in any base image

	"checkin/internal/app"
	"checkin/internal/config"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "err", err)
		os.Exit(1)
	}

	if err := app.Run(ctx, cfg); err != nil {
		slog.Error("run app", "err", err)
		os.Exit(1)
	}
}
