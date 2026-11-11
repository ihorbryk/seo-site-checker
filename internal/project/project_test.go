package project_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/project"
	"github.com/ihorbryk/seo-site-checker/internal/testdb"
)

func TestNormalizeDomain(t *testing.T) {
	valid := map[string]string{
		"example.com":           "example.com",
		"  Example.COM ":        "example.com",
		"www.example.com":       "www.example.com",
		"https://example.com":   "example.com",
		"https://Example.com/":  "example.com",
		"example.com.":          "example.com",
		"shop.test":             "shop.test",
		"bücher.example":        "xn--bcher-kva.example",
		"xn--bcher-kva.example": "xn--bcher-kva.example",
		"a-b.example.co.uk":     "a-b.example.co.uk",
	}
	for in, want := range valid {
		got, err := project.NormalizeDomain(in)
		if err != nil || got != want {
			t.Errorf("NormalizeDomain(%q) = %q, %v; want %q", in, got, err, want)
		}
	}

	invalid := []string{
		"",
		"   ",
		"http://example.com",
		"example.com/path",
		"https://example.com/path",
		"example.com?x=1",
		"example.com#frag",
		"example.com:8443",
		"user@example.com",
		"exa mple.com",
		"localhost",
		"192.168.0.1",
		"[::1]",
		"-bad.example.com",
		"bad..example.com",
		"*.example.com",
		"ftp://example.com",
	}
	for _, in := range invalid {
		if got, err := project.NormalizeDomain(in); !errors.Is(err, project.ErrInvalidDomain) {
			t.Errorf("NormalizeDomain(%q) = %q, %v; want ErrInvalidDomain", in, got, err)
		}
	}
}

func TestAddCreatesProjectWithSeedPageAndDefaults(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	p, err := project.Add(ctx, pool, " Example.com ")
	if err != nil {
		t.Fatal(err)
	}
	if p.Domain != "example.com" {
		t.Errorf("domain = %q", p.Domain)
	}
	if p.Schedule != "daily" || p.UserAgent != "SEOSiteChecker/0.1" || p.TimeoutS != 30 ||
		p.MaxParallel != 2 || p.RespectRobots {
		t.Errorf("Request settings defaults wrong: %+v", p)
	}

	pages, err := dbgen.New(pool).ListPagesByProject(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 || pages[0].Url != "https://example.com/" ||
		pages[0].Origin != "seed" || pages[0].State != "active" {
		t.Fatalf("expected the seed Page, got %+v", pages)
	}
}

func TestDomainIsImmutable(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	p, err := project.Add(ctx, pool, "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE projects SET domain = 'other.com' WHERE id = $1`, p.ID); err == nil {
		t.Fatal("changing a Project's domain should be refused")
	}
}

func TestAddRejectsDuplicateDomainAndCreatesNothingExtra(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	if _, err := project.Add(ctx, pool, "example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := project.Add(ctx, pool, "https://EXAMPLE.com/"); !errors.Is(err, project.ErrDuplicate) {
		t.Fatalf("want ErrDuplicate, got %v", err)
	}
	// www is a different exact host, so a different Project.
	if _, err := project.Add(ctx, pool, "www.example.com"); err != nil {
		t.Fatalf("www.example.com is a separate Project: %v", err)
	}
	if _, err := project.Add(ctx, pool, "not a domain"); !errors.Is(err, project.ErrInvalidDomain) {
		t.Fatalf("want ErrInvalidDomain, got %v", err)
	}
	projects, _ := dbgen.New(pool).ListProjects(ctx)
	if len(projects) != 2 {
		t.Fatalf("want 2 Projects, got %d", len(projects))
	}
}
