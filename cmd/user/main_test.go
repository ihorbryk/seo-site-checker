package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/session"
	"github.com/ihorbryk/seo-site-checker/internal/testdb"
	"github.com/ihorbryk/seo-site-checker/internal/user"
	"github.com/jackc/pgx/v5/pgxpool"
)

const pw = "correct horse battery"

type result struct {
	code        int
	stdout, err string
}

func runCLI(t *testing.T, pool *pgxpool.Pool, stdin string, args ...string) result {
	t.Helper()
	c := &cli{pool: pool, hasher: user.NewHasher(user.Params{MemoryKiB: 64, Time: 1, Threads: 1})}
	var out, errb bytes.Buffer
	code := c.run(context.Background(), args, strings.NewReader(stdin), &out, &errb)
	return result{code, out.String(), errb.String()}
}

func count(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCreate(t *testing.T) {
	pool := testdb.New(t)
	r := runCLI(t, pool, pw+"\n", "create", "Me@Example.com")
	if r.code != 0 || r.stdout != "user: created me@example.com\n" || r.err != "" {
		t.Fatalf("got %+v", r)
	}
	var hash string
	if err := pool.QueryRow(context.Background(), `SELECT password_hash FROM users WHERE email = 'me@example.com'`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if ok, _ := user.NewHasher(user.Params{MemoryKiB: 64, Time: 1, Threads: 1}).Verify(pw, hash); !ok {
		t.Fatal("stored hash does not verify")
	}
}

func TestCreateKeepsAllButTheTrailingNewline(t *testing.T) {
	pool := testdb.New(t)
	spaced := "  " + pw + "  "
	if r := runCLI(t, pool, spaced+"\nignored second line\n", "create", "me@example.com"); r.code != 0 {
		t.Fatalf("got %+v", r)
	}
	var hash string
	pool.QueryRow(context.Background(), `SELECT password_hash FROM users`).Scan(&hash)
	if ok, _ := user.NewHasher(user.Params{MemoryKiB: 64, Time: 1, Threads: 1}).Verify(spaced, hash); !ok {
		t.Fatal("password was trimmed or truncated")
	}
}

func TestCreateRefusesExistingEmailWithoutUpserting(t *testing.T) {
	pool := testdb.New(t)
	runCLI(t, pool, pw+"\n", "create", "me@example.com")
	var before string
	pool.QueryRow(context.Background(), `SELECT password_hash FROM users`).Scan(&before)

	r := runCLI(t, pool, "another long password\n", "create", "ME@example.com")
	if r.code != 1 || r.stdout != "" || r.err != "user: a User with email me@example.com already exists\n" {
		t.Fatalf("got %+v", r)
	}
	var after string
	pool.QueryRow(context.Background(), `SELECT password_hash FROM users`).Scan(&after)
	if before != after || count(t, pool, "users") != 1 {
		t.Fatal("existing User was changed")
	}
}

func TestValidationFailuresWriteNothing(t *testing.T) {
	pool := testdb.New(t)
	for _, tc := range []struct{ stdin, email, msg string }{
		{pw + "\n", "not-an-email", "user: Enter an email address like name@example.com.\n"},
		{"short\n", "me@example.com", "user: Use at least 15 characters (this one has 5).\n"},
		{"", "me@example.com", "user: Use at least 15 characters (this one has 0).\n"},
	} {
		r := runCLI(t, pool, tc.stdin, "create", tc.email)
		if r.code != 1 || r.stdout != "" || r.err != tc.msg {
			t.Errorf("%q: got %+v", tc.email, r)
		}
	}
	if n := count(t, pool, "users"); n != 0 {
		t.Fatalf("users = %d", n)
	}
}

func TestResetPassword(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	runCLI(t, pool, pw+"\n", "create", "me@example.com")
	var uid int64
	pool.QueryRow(ctx, `SELECT id FROM users`).Scan(&uid)
	for range 3 {
		if _, err := session.Create(ctx, pool, uid, time.Now()); err != nil {
			t.Fatal(err)
		}
	}

	r := runCLI(t, pool, "a brand new password\n", "reset-password", "me@example.com")
	if r.code != 0 || r.stdout != "user: reset password for me@example.com, signed out 3 sessions\n" || r.err != "" {
		t.Fatalf("got %+v", r)
	}
	if n := count(t, pool, "sessions"); n != 0 {
		t.Fatalf("sessions left: %d", n)
	}
	var hash string
	pool.QueryRow(ctx, `SELECT password_hash FROM users`).Scan(&hash)
	if ok, _ := user.NewHasher(user.Params{MemoryKiB: 64, Time: 1, Threads: 1}).Verify("a brand new password", hash); !ok {
		t.Fatal("new password does not verify")
	}
}

func TestResetPasswordRefusesUnknownEmail(t *testing.T) {
	pool := testdb.New(t)
	r := runCLI(t, pool, pw+"\n", "reset-password", "ghost@example.com")
	if r.code != 1 || r.stdout != "" || r.err != "user: no User with email ghost@example.com\n" {
		t.Fatalf("got %+v", r)
	}
	if n := count(t, pool, "users"); n != 0 {
		t.Fatal("reset-password created a User")
	}
}

func TestResetPasswordValidationFailureKeepsSessions(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	runCLI(t, pool, pw+"\n", "create", "me@example.com")
	var uid int64
	pool.QueryRow(ctx, `SELECT id FROM users`).Scan(&uid)
	session.Create(ctx, pool, uid, time.Now())
	var before string
	pool.QueryRow(ctx, `SELECT password_hash FROM users`).Scan(&before)

	r := runCLI(t, pool, "short\n", "reset-password", "me@example.com")
	if r.code != 1 || r.stdout != "" || r.err == "" {
		t.Fatalf("got %+v", r)
	}
	var after string
	pool.QueryRow(ctx, `SELECT password_hash FROM users`).Scan(&after)
	if before != after || count(t, pool, "sessions") != 1 {
		t.Fatal("a failed reset wrote something")
	}
}

func TestTerminalPromptsTwiceAndRefusesMismatch(t *testing.T) {
	pool := testdb.New(t)
	answers := []string{pw, pw + "x"}
	var prompts []string
	c := &cli{pool: pool, hasher: user.NewHasher(user.Params{MemoryKiB: 64, Time: 1, Threads: 1}),
		secret: func(prompt string) (string, error) {
			prompts = append(prompts, prompt)
			a := answers[0]
			answers = answers[1:]
			return a, nil
		}}
	var out, errb bytes.Buffer
	code := c.run(context.Background(), []string{"create", "me@example.com"}, strings.NewReader(""), &out, &errb)
	if code != 1 || out.String() != "" || errb.String() != "user: passwords do not match\n" || len(prompts) != 2 {
		t.Fatalf("code=%d out=%q err=%q prompts=%v", code, out.String(), errb.String(), prompts)
	}
	if n := count(t, pool, "users"); n != 0 {
		t.Fatal("a mismatch wrote a User")
	}

	answers = []string{pw, pw}
	code = c.run(context.Background(), []string{"create", "me@example.com"}, strings.NewReader(""), &out, &errb)
	if code != 0 || count(t, pool, "users") != 1 {
		t.Fatalf("matching confirmation: code=%d err=%q", code, errb.String())
	}
}

func TestUsageErrorsExit2(t *testing.T) {
	pool := testdb.New(t)
	for _, args := range [][]string{
		nil,
		{"create"},
		{"create", "a@b.co", "extra"},
		{"reset-password"},
		{"delete", "a@b.co"},
		{"create", "--password=x", "a@b.co"},
	} {
		r := runCLI(t, pool, pw+"\n", args...)
		if r.code != 2 || r.stdout != "" || !strings.Contains(r.err, "usage: user create <email>") {
			t.Errorf("%v: got %+v", args, r)
		}
	}
	if n := count(t, pool, "users"); n != 0 {
		t.Fatal("a usage error wrote a User")
	}
}
