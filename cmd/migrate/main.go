// Command migrate applies the embedded database migrations to DATABASE_URL
// and exits non-zero on failure. It is the production counterpart of
// `mise run migrate` (the goose CLI) and ships in the operator image.
//
// Migration 00039 reads BOOTSTRAP_EMAIL; goose's Provider substitutes it from
// the environment the same way the CLI does.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ihorbryk/seo-site-checker/db"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/pressly/goose/v3"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil)).With("binary", "migrate")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	url := os.Getenv("DATABASE_URL")
	if url == "" {
		log.Error("DATABASE_URL is required")
		os.Exit(1)
	}
	sqlDB, err := sql.Open("pgx", url)
	if err != nil {
		log.Error("open database", "err", err)
		os.Exit(1)
	}

	n, err := migrate(ctx, sqlDB, log)
	sqlDB.Close()
	if err != nil {
		log.Error("migration failed", "err", err)
		os.Exit(1)
	}
	log.Info("migrations done", "applied", n)
}

// migrate applies every pending migration, logging each, and returns how many
// were applied.
func migrate(ctx context.Context, sqlDB *sql.DB, log *slog.Logger) (int, error) {
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, db.Migrations())
	if err != nil {
		return 0, fmt.Errorf("create provider: %w", err)
	}
	results, err := p.Up(ctx)
	for _, r := range results {
		log.Info("applied migration", "version", r.Source.Version, "path", r.Source.Path, "duration", r.Duration)
	}
	return len(results), err
}
