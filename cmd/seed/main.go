// Command seed creates the manual-only demo Project named by DEMO_DOMAIN.
// It never duplicates it and does nothing, with a hint, when the variable is unset.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/ihorbryk/seo-site-checker/internal/app"
	"github.com/ihorbryk/seo-site-checker/internal/config"
	"github.com/ihorbryk/seo-site-checker/internal/project"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	domain := os.Getenv("DEMO_DOMAIN")
	if domain == "" {
		fmt.Println("seed: DEMO_DOMAIN is not set, nothing to do. Set it (for example in mise.local.toml: [env] DEMO_DOMAIN = \"example.com\") to get a demo Project.")
		return
	}
	app.Run("seed", func(ctx context.Context, _ config.Config, _ *slog.Logger, pool *pgxpool.Pool) error { // pageliterals:ok command name, not a Page origin
		created, err := project.EnsureDemo(ctx, pool, domain)
		if err != nil {
			return err
		}
		if created {
			fmt.Printf("seed: created demo Project %s (manual only)\n", domain)
		} else {
			fmt.Printf("seed: demo Project %s already exists\n", domain)
		}
		return nil
	})
}
