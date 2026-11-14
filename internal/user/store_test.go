package user_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/session"
	"github.com/ihorbryk/seo-site-checker/internal/testdb"
	"github.com/ihorbryk/seo-site-checker/internal/user"
)

var testHasher = user.NewHasher(user.Params{MemoryKiB: 64, Time: 1, Threads: 1})

const goodPassword = "correct horse battery"

func TestCreateNormalizesAndHashes(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	u, err := user.Create(ctx, pool, testHasher, "  Me@Example.COM ", goodPassword)
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "me@example.com" {
		t.Fatalf("email = %q", u.Email)
	}
	if ok, _ := testHasher.Verify(goodPassword, u.PasswordHash); !ok {
		t.Fatal("stored hash does not verify the password")
	}
}

func TestCreateRefusesDuplicateAndInvalid(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	if _, err := user.Create(ctx, pool, testHasher, "me@example.com", goodPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := user.Create(ctx, pool, testHasher, "ME@example.com", "another long password"); !errors.Is(err, user.ErrEmailTaken) {
		t.Fatalf("duplicate: err = %v, want ErrEmailTaken", err)
	}
	if _, err := user.Create(ctx, pool, testHasher, "nope", goodPassword); !errors.Is(err, user.ErrInvalidEmail) {
		t.Fatalf("bad email: err = %v", err)
	}
	if _, err := user.Create(ctx, pool, testHasher, "you@example.com", "short"); err == nil {
		t.Fatal("short password accepted")
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("users = %d, %v; want 1", n, err)
	}
}

func TestResetPasswordReplacesHashAndDeletesAllSessions(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	u, err := user.Create(ctx, pool, testHasher, "me@example.com", goodPassword)
	if err != nil {
		t.Fatal(err)
	}
	other, err := user.Create(ctx, pool, testHasher, "you@example.com", goodPassword)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := session.Create(ctx, pool, u.ID, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := session.Create(ctx, pool, other.ID, time.Now()); err != nil {
		t.Fatal(err)
	}

	n, err := user.ResetPassword(ctx, pool, testHasher, " ME@example.com", "a brand new password")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("signed out %d sessions, want 2", n)
	}
	var hash string
	var mine, theirs int
	if err := pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1`, u.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if ok, _ := testHasher.Verify("a brand new password", hash); !ok {
		t.Fatal("new password does not verify")
	}
	if ok, _ := testHasher.Verify(goodPassword, hash); ok {
		t.Fatal("old password still verifies")
	}
	pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE user_id = $1`, u.ID).Scan(&mine)
	pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE user_id = $1`, other.ID).Scan(&theirs)
	if mine != 0 || theirs != 1 {
		t.Fatalf("sessions left: mine=%d theirs=%d, want 0 and 1", mine, theirs)
	}
}

func TestResetPasswordRefusesUnknownAndInvalid(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	if _, err := user.ResetPassword(ctx, pool, testHasher, "ghost@example.com", goodPassword); !errors.Is(err, user.ErrNotFound) {
		t.Fatalf("unknown: err = %v, want ErrNotFound", err)
	}
	u, _ := user.Create(ctx, pool, testHasher, "me@example.com", goodPassword)
	if _, err := session.Create(ctx, pool, u.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := user.ResetPassword(ctx, pool, testHasher, "me@example.com", "short"); err == nil {
		t.Fatal("short password accepted")
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM sessions`).Scan(&n)
	if n != 1 {
		t.Fatalf("a refused reset deleted sessions: %d left", n)
	}
}

func TestEnsureCreatesOnceAndKeepsTheExistingPassword(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	u, created, err := user.Ensure(ctx, pool, testHasher, "Demo@Example.com", goodPassword)
	if err != nil || !created || u.Email != "demo@example.com" {
		t.Fatalf("first call: %+v created=%v err=%v", u, created, err)
	}
	again, created, err := user.Ensure(ctx, pool, testHasher, "demo@example.com", "a different long password")
	if err != nil || created || again.ID != u.ID {
		t.Fatalf("second call: %+v created=%v err=%v", again, created, err)
	}
	if ok, _ := testHasher.Verify(goodPassword, again.PasswordHash); !ok {
		t.Fatal("Ensure must leave an existing User's password unchanged")
	}
}
