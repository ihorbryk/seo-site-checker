// Command web serves the htmx UI.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/app"
	"github.com/ihorbryk/seo-site-checker/internal/config"
	"github.com/ihorbryk/seo-site-checker/internal/web"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	app.Run("web", func(ctx context.Context, cfg config.Config, log *slog.Logger, pool *pgxpool.Pool) error {
		handler, err := web.New(pool, log)
		if err != nil {
			return err
		}
		srv := &http.Server{Addr: cfg.WebAddr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
		go func() {
			<-ctx.Done()
			shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutdown)
		}()
		log.Info("listening", "addr", cfg.WebAddr)
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})
}
