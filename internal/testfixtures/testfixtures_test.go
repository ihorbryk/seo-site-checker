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

func TestCheckAndSnapshotBuilders(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	p := testfixtures.Project(t, pool)
	pg := testfixtures.Page(t, pool, p, "/")

	c := testfixtures.Check(t, pool, p)
	if c.ProjectID != p.ID {
		t.Fatalf("check project = %d, want %d", c.ProjectID, p.ID)
	}
	snap := testfixtures.Snapshot(t, pool, c, pg)
	if snap.PageID != pg.ID || snap.CheckID != c.ID {
		t.Fatalf("snapshot = %+v", snap)
	}
	if _, err := dbgen.New(pool).GetSnapshotView(ctx, snap.ID); err != nil {
		t.Fatalf("snapshot not readable: %v", err)
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

func TestCheckOptionsSetStateAndCreationTimeAndKeepTheCounterInStep(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	p := testfixtures.Project(t, pool)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	first := testfixtures.Check(t, pool, p, testfixtures.WithCheckState("finished"), testfixtures.WithCheckCreatedAt(at))
	second := testfixtures.Check(t, pool, p)

	got, err := dbgen.New(pool).GetCheck(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "finished" || !got.CreatedAt.Equal(at) || !got.StartedAt.Valid || !got.FinishedAt.Valid {
		t.Fatalf("check = %+v", got)
	}
	if first.Number != 1 || second.Number != 2 || second.State != "queued" {
		t.Fatalf("numbers %d, %d; second state %q", first.Number, second.Number, second.State)
	}
	var last int64
	if err := pool.QueryRow(ctx, `SELECT last_check_number FROM projects WHERE id = $1`, p.ID).Scan(&last); err != nil || last != 2 {
		t.Fatalf("last_check_number = %d (%v), want 2", last, err)
	}
}
