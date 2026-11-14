package session_test

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/session"
	"github.com/ihorbryk/seo-site-checker/internal/testdb"
)

func TestCreateStoresOnlyTheTokenHash(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	var uid int64
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ('a@b.co', '!') RETURNING id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	raw, err := session.Create(ctx, pool, uid, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 43 { // 32 bytes, unpadded base64url
		t.Fatalf("token %q has length %d, want 43", raw, len(raw))
	}
	want := sha256.Sum256([]byte(raw))
	var hash []byte
	var expires time.Time
	if err := pool.QueryRow(ctx, `SELECT token_hash, expires_at FROM sessions WHERE user_id = $1`, uid).Scan(&hash, &expires); err != nil {
		t.Fatal(err)
	}
	if string(hash) != string(want[:]) {
		t.Fatal("stored token_hash is not the SHA-256 of the raw token")
	}
	if d := expires.Sub(now.Add(30 * 24 * time.Hour)); d < -time.Second || d > time.Second {
		t.Fatalf("expires_at off by %v from 30 days", d)
	}
	other, err := session.Create(ctx, pool, uid, now)
	if err != nil {
		t.Fatal(err)
	}
	if other == raw {
		t.Fatal("tokens must be unique")
	}
}
