// Command seed creates the demo User and the manual-only demo Project, named
// by DEMO_EMAIL, DEMO_PASSWORD and DEMO_DOMAIN. It requires all three
// together (so seeding a real database can never create a known-password
// account), never duplicates anything and never changes an existing User's
// password.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/ihorbryk/seo-site-checker/internal/app"
	"github.com/ihorbryk/seo-site-checker/internal/config"
	"github.com/ihorbryk/seo-site-checker/internal/project"
	"github.com/ihorbryk/seo-site-checker/internal/user"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	domain, email, password := os.Getenv("DEMO_DOMAIN"), os.Getenv("DEMO_EMAIL"), os.Getenv("DEMO_PASSWORD")
	if domain == "" || email == "" || password == "" {
		fmt.Println("seed: DEMO_DOMAIN, DEMO_EMAIL and DEMO_PASSWORD must all be set, nothing to do. Set them (for example in mise.local.toml: [env] DEMO_DOMAIN = \"example.com\", DEMO_EMAIL = \"demo@example.com\", DEMO_PASSWORD = \"a password of 15+ characters\") to get a demo User and Project.")
		return
	}
	app.Run("seed", func(ctx context.Context, cfg config.Config, _ *slog.Logger, pool *pgxpool.Pool) error { // pageliterals:ok command name, not a Page origin
		hasher := user.NewHasher(user.Params{MemoryKiB: cfg.Argon2MemoryKiB, Time: cfg.Argon2Time, Threads: cfg.Argon2Threads})
		u, userCreated, err := user.Ensure(ctx, pool, hasher, email, password)
		if err != nil {
			return err
		}
		if userCreated {
			fmt.Printf("seed: created demo User %s\n", u.Email)
		} else {
			fmt.Printf("seed: demo User %s already exists\n", u.Email)
		}
		created, err := project.EnsureDemo(ctx, pool, u.ID, domain)
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
