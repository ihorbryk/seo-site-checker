package session_test

import (
	"context"
	"testing"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/session"
	"github.com/ihorbryk/seo-site-checker/internal/testdb"
)

func TestLookupFindsALiveSessionAndRejectsDeadOnes(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	var uid int64
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ('a@b.co', '!') RETURNING id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	raw, err := session.Create(ctx, pool, uid, start)
	if err != nil {
		t.Fatal(err)
	}

	lookup := func(token string, at time.Time) bool {
		t.Helper()
		u, ok, err := session.Lookup(ctx, pool, token, at)
		if err != nil {
			t.Fatal(err)
		}
		if ok && (u.ID != uid || u.Email != "a@b.co") {
			t.Fatalf("looked up %+v", u)
		}
		return ok
	}

	if !lookup(raw, start.Add(time.Minute)) {
		t.Fatal("a fresh session must be found")
	}
	if lookup("not-a-token", start) {
		t.Fatal("an unknown token must not be found")
	}

	// Idle: found just inside 14 days of the last use, gone just past it.
	if !lookup(raw, start.Add(session.IdleTimeout-time.Minute)) {
		t.Fatal("a session idle for just under 14 days is alive")
	}
	// (that lookup bumped raw, so check the dead case on a session nobody used)
	unused, _ := session.Create(ctx, pool, uid, start)
	if lookup(unused, start.Add(session.IdleTimeout+time.Minute)) {
		t.Fatal("a session idle for over 14 days is dead")
	}
}

func TestLookupBumpsLastSeenAtMostHourly(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	var uid int64
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ('a@b.co', '!') RETURNING id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	raw, _ := session.Create(ctx, pool, uid, start)
	seen := func() time.Time {
		var at time.Time
		if err := pool.QueryRow(ctx, `SELECT last_seen_at FROM sessions WHERE user_id = $1`, uid).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at.UTC()
	}
	use := func(at time.Time) {
		if _, ok, err := session.Lookup(ctx, pool, raw, at); err != nil || !ok {
			t.Fatalf("lookup at %v: ok=%v err=%v", at, ok, err)
		}
	}

	use(start.Add(30 * time.Minute))
	if got := seen(); !got.Equal(start) {
		t.Fatalf("last_seen_at bumped within the hour: %v", got)
	}
	later := start.Add(90 * time.Minute)
	use(later)
	if got := seen(); !got.Equal(later) {
		t.Fatalf("last_seen_at = %v, want bumped to %v", got, later)
	}

	// Regular use keeps a session alive past the idle limit, but never past 30 days.
	for at := later; at.Before(start.Add(session.MaxAge - 2*time.Hour)); at = at.Add(10 * 24 * time.Hour) {
		use(at)
	}
	if _, ok, _ := session.Lookup(ctx, pool, raw, start.Add(session.MaxAge+time.Minute)); ok {
		t.Fatal("a session older than 30 days is dead however recently used")
	}
}

func TestDeleteRemovesTheSession(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	var uid int64
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ('a@b.co', '!') RETURNING id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	raw, _ := session.Create(ctx, pool, uid, now)
	if err := session.Delete(ctx, pool, raw); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := session.Lookup(ctx, pool, raw, now); ok {
		t.Fatal("a deleted session must not be found")
	}
}
