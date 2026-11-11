// Package config reads the process configuration. It is environment
// variables only; mise.toml supplies the development defaults.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
)

// Config is shared by the web, scheduler, worker and seed binaries.
type Config struct {
	DatabaseURL       string
	WebAddr           string
	LogLevel          slog.Level
	WorkerConcurrency int
}

// Load builds a Config from getenv (os.Getenv in the binaries). Unset
// variables take their defaults, except DATABASE_URL which is required.
func Load(getenv func(string) string) (Config, error) {
	c := Config{
		DatabaseURL:       getenv("DATABASE_URL"),
		WebAddr:           orDefault(getenv("WEB_ADDR"), "127.0.0.1:8089"),
		WorkerConcurrency: 20,
	}
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required")
	}
	if err := c.LogLevel.UnmarshalText([]byte(orDefault(getenv("LOG_LEVEL"), "info"))); err != nil {
		return c, fmt.Errorf("LOG_LEVEL: %w", err)
	}
	if v := getenv("WORKER_CONCURRENCY"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return c, fmt.Errorf("WORKER_CONCURRENCY must be a positive integer, got %q", v)
		}
		c.WorkerConcurrency = n
	}
	return c, nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
