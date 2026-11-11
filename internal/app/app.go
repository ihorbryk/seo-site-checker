// Package app is the shared startup of the binaries: environment config,
// logging, a Postgres pool and signal-driven shutdown. Nothing here migrates;
// run `mise run migrate` first.
package app

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ihorbryk/seo-site-checker/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Run loads the configuration, connects to Postgres and calls fn with a
// context cancelled by SIGINT/SIGTERM. It exits the process with status 1 on
// error.
func Run(name string, fn func(ctx context.Context, cfg config.Config, log *slog.Logger, pool *pgxpool.Pool) error) {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		slog.Error("configuration", "binary", name, "err", err)
		os.Exit(1)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel})).With("binary", name)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err == nil {
		err = pool.Ping(ctx)
	}
	if err != nil {
		log.Error("connect to Postgres", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := fn(ctx, cfg, log, pool); err != nil {
		log.Error("exiting", "err", err)
		pool.Close()
		os.Exit(1)
	}
}
