// Package session creates the server-side half of a sign-in: a random token
// whose SHA-256 is stored, so a database leak exposes no live sessions.
package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
)

// Lifetimes: a session ends after IdleTimeout without use, or MaxAge after
// creation, whichever comes first.
const (
	IdleTimeout = 14 * 24 * time.Hour
	MaxAge      = 30 * 24 * time.Hour
)

// HashToken returns the SHA-256 of a raw token, the value stored as token_hash.
func HashToken(raw string) []byte {
	h := sha256.Sum256([]byte(raw))
	return h[:]
}

// Create stores a new session for userID and returns the raw token for the
// cookie (32 random bytes, base64url). Login, registration, change password
// and test fixtures all use it.
func Create(ctx context.Context, db dbgen.DBTX, userID int64, now time.Time) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	raw := base64.RawURLEncoding.EncodeToString(b)
	err := dbgen.New(db).CreateSession(ctx, dbgen.CreateSessionParams{
		TokenHash: HashToken(raw),
		UserID:    userID,
		ExpiresAt: now.Add(MaxAge),
	})
	if err != nil {
		return "", err
	}
	return raw, nil
}
