// Package project adds Projects: it validates the domain and creates the
// Project together with its seed Page.
package project

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/net/idna"
)

var (
	// ErrInvalidDomain means the input is not a single exact https host.
	ErrInvalidDomain = errors.New("not a valid domain")
	// ErrDuplicate means a Project for that exact host already exists.
	ErrDuplicate = errors.New("a Project for this domain already exists")
)

// NormalizeDomain turns user input into the stored domain: one exact host,
// lower case, punycode for international names. A leading https:// and a
// trailing slash or dot are tolerated; anything else (other schemes, ports,
// paths, queries, IP addresses, single-label hosts) is ErrInvalidDomain.
func NormalizeDomain(input string) (string, error) {
	s := strings.TrimSpace(input)
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".")
	if s == "" || strings.ContainsAny(s, "/?#:@[]*% \t\r\n\\") {
		return "", fmt.Errorf("%w: %q (use one host such as example.com)", ErrInvalidDomain, input)
	}
	host, err := idna.Lookup.ToASCII(s)
	if err != nil {
		return "", fmt.Errorf("%w: %q", ErrInvalidDomain, input)
	}
	if _, err := netip.ParseAddr(host); err == nil || !strings.Contains(host, ".") || len(host) > 253 {
		return "", fmt.Errorf("%w: %q (use a host name such as example.com)", ErrInvalidDomain, input)
	}
	for _, label := range strings.Split(host, ".") {
		if !validLabel(label) {
			return "", fmt.Errorf("%w: %q", ErrInvalidDomain, input)
		}
	}
	return host, nil
}

func validLabel(l string) bool {
	if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
		return false
	}
	for _, c := range l {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// Beginner starts transactions; *pgxpool.Pool satisfies it.
type Beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Add creates a Project with the default Request settings and its seed Page
// https://<domain>/ in one transaction.
func Add(ctx context.Context, db Beginner, input string) (dbgen.Project, error) {
	return AddWithSchedule(ctx, db, input, "daily")
}

// EnsureDemo creates the manual-only demo Project for domain unless a Project
// for it exists, and reports whether it created one.
func EnsureDemo(ctx context.Context, db Beginner, domain string) (bool, error) {
	_, err := AddWithSchedule(ctx, db, domain, "manual")
	if errors.Is(err, ErrDuplicate) {
		return false, nil
	}
	return err == nil, err
}

// AddWithSchedule is Add with a chosen schedule (the demo Project is "manual").
func AddWithSchedule(ctx context.Context, db Beginner, input, schedule string) (dbgen.Project, error) {
	domain, err := NormalizeDomain(input)
	if err != nil {
		return dbgen.Project{}, err
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return dbgen.Project{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	q := dbgen.New(tx)
	p, err := q.CreateProject(ctx, dbgen.CreateProjectParams{Domain: domain, Schedule: schedule})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return dbgen.Project{}, ErrDuplicate
		}
		return dbgen.Project{}, err
	}
	if _, err := q.CreatePage(ctx, dbgen.CreatePageParams{
		ProjectID: p.ID, Url: "https://" + domain + "/", Origin: "seed",
	}); err != nil {
		return dbgen.Project{}, err
	}
	return p, tx.Commit(ctx)
}
