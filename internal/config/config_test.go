package config_test

import (
	"log/slog"
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/config"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadAppliesDefaults(t *testing.T) {
	c, err := config.Load(env(map[string]string{"DATABASE_URL": "postgres://x/y"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.DatabaseURL != "postgres://x/y" || c.WebAddr != "127.0.0.1:8089" ||
		c.LogLevel != slog.LevelInfo || c.WorkerConcurrency != 20 {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}

func TestLoadReadsOverrides(t *testing.T) {
	c, err := config.Load(env(map[string]string{
		"DATABASE_URL": "postgres://x/y", "WEB_ADDR": ":9000", "LOG_LEVEL": "debug", "WORKER_CONCURRENCY": "3",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.WebAddr != ":9000" || c.LogLevel != slog.LevelDebug || c.WorkerConcurrency != 3 {
		t.Fatalf("overrides ignored: %+v", c)
	}
}

func TestLoadRejectsBadValues(t *testing.T) {
	for name, m := range map[string]map[string]string{
		"missing url":     {},
		"bad level":       {"DATABASE_URL": "u", "LOG_LEVEL": "loud"},
		"bad concurrency": {"DATABASE_URL": "u", "WORKER_CONCURRENCY": "0"},
		"non-numeric":     {"DATABASE_URL": "u", "WORKER_CONCURRENCY": "many"},
	} {
		if _, err := config.Load(env(m)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
