package testfixtures_test

import (
	"context"
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/testdb"
	"github.com/ihorbryk/seo-site-checker/internal/testfixtures"
)

func TestProjectAndPageBuilders(t *testing.T) {
	pool := testdb.New(t)

	a := testfixtures.Project(t, pool)
	b := testfixtures.Project(t, pool, testfixtures.WithDomain("shop.test"), testfixtures.WithSchedule("manual"))
	if a.Domain == b.Domain {
		t.Fatalf("default domains must be unique, both %q", a.Domain)
	}
	if b.Domain != "shop.test" || b.Schedule != "manual" {
		t.Fatalf("options ignored: %+v", b)
	}

	testfixtures.Page(t, pool, b, "/", testfixtures.WithOrigin("seed"))
	testfixtures.Page(t, pool, b, "/about")

	pages, err := dbgen.New(pool).ListPagesByProject(context.Background(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 || pages[0].Url != "https://shop.test/" || pages[0].Origin != "seed" ||
		pages[1].Url != "https://shop.test/about" || pages[1].Origin != "discovered" {
		t.Fatalf("unexpected pages: %+v", pages)
	}
}
