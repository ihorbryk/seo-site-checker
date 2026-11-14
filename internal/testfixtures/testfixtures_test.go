package testfixtures_test

import (
	"context"
	"testing"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/ihorbryk/seo-site-checker/internal/session"
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

func TestUserAndSessionBuilders(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	a := testfixtures.User(t, pool)
	b := testfixtures.User(t, pool, testfixtures.WithEmail("me@example.test"))
	if a.Email == b.Email {
		t.Fatalf("default emails must be unique, both %q", a.Email)
	}
	if b.Email != "me@example.test" {
		t.Fatalf("email = %q", b.Email)
	}
	if ok, _ := testfixtures.Hasher().Verify(testfixtures.Password, a.PasswordHash); !ok {
		t.Fatal("the fixture User's hash must verify testfixtures.Password")
	}

	raw := testfixtures.Session(t, pool, a)
	got, ok, err := session.Lookup(ctx, pool, raw, time.Now())
	if err != nil || !ok || got.ID != a.ID {
		t.Fatalf("session lookup = %+v ok=%v err=%v", got, ok, err)
	}
}

func TestProjectOwnerDefaultsToAFreshUserAndWithOwnerOverrides(t *testing.T) {
	pool := testdb.New(t)
	owner := testfixtures.User(t, pool)

	mine := testfixtures.Project(t, pool, testfixtures.WithOwner(owner))
	if mine.OwnerID != owner.ID {
		t.Fatalf("owner = %d, want %d", mine.OwnerID, owner.ID)
	}
	a, b := testfixtures.Project(t, pool), testfixtures.Project(t, pool)
	if a.OwnerID == b.OwnerID || a.OwnerID == owner.ID {
		t.Fatalf("default owners must be fresh Users: %d %d (explicit %d)", a.OwnerID, b.OwnerID, owner.ID)
	}
}
