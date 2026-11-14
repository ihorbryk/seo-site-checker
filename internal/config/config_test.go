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

func TestLoadArgon2Defaults(t *testing.T) {
	c, err := config.Load(env(map[string]string{"DATABASE_URL": "u"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Argon2MemoryKiB != 19456 || c.Argon2Time != 2 || c.Argon2Threads != 1 {
		t.Fatalf("defaults: %+v", c)
	}
}

func TestLoadArgon2Overrides(t *testing.T) {
	c, err := config.Load(env(map[string]string{
		"DATABASE_URL": "u", "ARGON2_MEMORY_KIB": "47104", "ARGON2_TIME": "1", "ARGON2_THREADS": "4",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Argon2MemoryKiB != 47104 || c.Argon2Time != 1 || c.Argon2Threads != 4 {
		t.Fatalf("overrides: %+v", c)
	}
}

func TestLoadRejectsBadArgon2(t *testing.T) {
	for _, m := range []map[string]string{
		{"ARGON2_MEMORY_KIB": "0"}, {"ARGON2_MEMORY_KIB": "lots"}, {"ARGON2_TIME": "0"},
		{"ARGON2_THREADS": "0"}, {"ARGON2_THREADS": "256"}, {"ARGON2_MEMORY_KIB": "4"},
	} {
		m["DATABASE_URL"] = "u"
		if _, err := config.Load(env(m)); err == nil {
			t.Errorf("%v: want an error", m)
		}
	}
}

func TestLoadDenyRegistration(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://x/y"}
	c, err := config.Load(env(base))
	if err != nil || c.DenyRegistration {
		t.Fatalf("registration must be open by default: %+v err=%v", c, err)
	}
	base["DENY_REGISTRATION"] = "true"
	if c, err = config.Load(env(base)); err != nil || !c.DenyRegistration {
		t.Fatalf("DENY_REGISTRATION=true: %+v err=%v", c, err)
	}
	base["DENY_REGISTRATION"] = "maybe"
	if _, err = config.Load(env(base)); err == nil {
		t.Fatal("a non-boolean DENY_REGISTRATION must be rejected")
	}
}

func TestLoadInsecureCookies(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://x/y"}
	c, err := config.Load(env(base))
	if err != nil || c.InsecureCookies {
		t.Fatalf("default must be secure cookies: %+v err=%v", c, err)
	}
	base["INSECURE_COOKIES"] = "true"
	if c, err = config.Load(env(base)); err != nil || !c.InsecureCookies {
		t.Fatalf("INSECURE_COOKIES=true: %+v err=%v", c, err)
	}
	base["INSECURE_COOKIES"] = "sometimes"
	if _, err = config.Load(env(base)); err == nil {
		t.Fatal("a non-boolean INSECURE_COOKIES must be rejected")
	}
}

func TestLoadTrustedProxyHeader(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://x/y"}
	if c, err := config.Load(env(base)); err != nil || c.TrustedProxyHeader != "" {
		t.Fatalf("default must be no header: %+v err=%v", c, err)
	}
	base["TRUSTED_PROXY_HEADER"] = "X-Forwarded-For"
	if c, err := config.Load(env(base)); err != nil || c.TrustedProxyHeader != "X-Forwarded-For" {
		t.Fatalf("TRUSTED_PROXY_HEADER: %+v err=%v", c, err)
	}
}
