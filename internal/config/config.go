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

	// InsecureCookies (INSECURE_COOKIES=true) serves the session cookie over
	// plain HTTP, for local development: no Secure flag, no HSTS.
	InsecureCookies bool

	// DenyRegistration (DENY_REGISTRATION=true) closes self-registration:
	// /register does not exist and accounts are created by the operator.
	DenyRegistration bool

	// TrustedProxyHeader (TRUSTED_PROXY_HEADER) names the header holding the
	// client IP behind a reverse proxy; its rightmost entry keys rate limits.
	// Empty means the connection's address.
	TrustedProxyHeader string

	// argon2id cost parameters for password hashing.
	Argon2MemoryKiB uint32
	Argon2Time      uint32
	Argon2Threads   uint8
}

// Load builds a Config from getenv (os.Getenv in the binaries). Unset
// variables take their defaults, except DATABASE_URL which is required.
func Load(getenv func(string) string) (Config, error) {
	c := Config{
		DatabaseURL:        getenv("DATABASE_URL"),
		WebAddr:            orDefault(getenv("WEB_ADDR"), "127.0.0.1:8089"),
		WorkerConcurrency:  20,
		TrustedProxyHeader: getenv("TRUSTED_PROXY_HEADER"),
		Argon2MemoryKiB:    19456,
		Argon2Time:         2,
		Argon2Threads:      1,
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
	var err error
	if v := getenv("INSECURE_COOKIES"); v != "" {
		if c.InsecureCookies, err = strconv.ParseBool(v); err != nil {
			return c, fmt.Errorf("INSECURE_COOKIES must be true or false, got %q", v)
		}
	}
	if v := getenv("DENY_REGISTRATION"); v != "" {
		if c.DenyRegistration, err = strconv.ParseBool(v); err != nil {
			return c, fmt.Errorf("DENY_REGISTRATION must be true or false, got %q", v)
		}
	}
	if c.Argon2MemoryKiB, err = uintVar(getenv, "ARGON2_MEMORY_KIB", c.Argon2MemoryKiB, 8, 1<<22); err != nil {
		return c, err
	}
	if c.Argon2Time, err = uintVar(getenv, "ARGON2_TIME", c.Argon2Time, 1, 100); err != nil {
		return c, err
	}
	threads, err := uintVar(getenv, "ARGON2_THREADS", uint32(c.Argon2Threads), 1, 255)
	if err != nil {
		return c, err
	}
	c.Argon2Threads = uint8(threads)
	if c.Argon2MemoryKiB < 8*uint32(c.Argon2Threads) {
		return c, errors.New("ARGON2_MEMORY_KIB must be at least 8 times ARGON2_THREADS")
	}
	return c, nil
}

// uintVar reads an integer in [lo, hi] from name, or returns def when unset.
func uintVar(getenv func(string) string, name string, def, lo, hi uint32) (uint32, error) {
	v := getenv(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.ParseUint(v, 10, 32)
	if err != nil || n < uint64(lo) || n > uint64(hi) {
		return 0, fmt.Errorf("%s must be an integer between %d and %d, got %q", name, lo, hi, v)
	}
	return uint32(n), nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
