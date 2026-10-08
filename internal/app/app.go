package app

import (
	"context"
	"fmt"
	"net/http"
	"time"
	"log/slog"

	"checkin/internal/config"
	"checkin/migrations"
	"checkin/internal/repository"
	"checkin/internal/transport/httpapi"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func Run(ctx context.Context, cfg config.Config) error {
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("pool init: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping db: %w", err)
	}
	db := stdlib.OpenDBFromPool(pool)
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		return fmt.Errorf("goose: %w", err)
	}
	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	slog.Info("migrations applied", "count", len(results))
	repo := repository.New(pool)
	server := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: httpapi.New(repo, cfg.AdminPassword).Routes(),
		ReadTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()

	slog.Info("listening", "addr", cfg.HTTPAddr)
	select {
	case err := <-errCh:
		return fmt.Errorf("server: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
	}
	return nil
}
