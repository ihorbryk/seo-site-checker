// Package session creates the server-side half of a sign-in: a random token
// whose SHA-256 is stored, so a database leak exposes no live sessions.
package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/jackc/pgx/v5"
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
		Now:       now,
		ExpiresAt: now.Add(MaxAge),
	})
	if err != nil {
		return "", err
	}
	return raw, nil
}

// BumpEvery is how stale last_seen_at must be before Lookup refreshes it, so
// a busy session does not write on every request.
const BumpEvery = time.Hour

// Lookup returns the User behind the raw cookie token, or ok=false when there
// is no such session or it is past IdleTimeout since last use or past MaxAge
// since creation. It refreshes last_seen_at at most every BumpEvery.
func Lookup(ctx context.Context, db dbgen.DBTX, raw string, now time.Time) (u dbgen.User, ok bool, err error) {
	q := dbgen.New(db)
	hash := HashToken(raw)
	row, err := q.GetSessionUser(ctx, dbgen.GetSessionUserParams{
		TokenHash: hash, Now: now, IdleCutoff: now.Add(-IdleTimeout),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.User{}, false, nil
	}
	if err != nil {
		return dbgen.User{}, false, err
	}
	if now.Sub(row.LastSeenAt) > BumpEvery {
		if err := q.TouchSession(ctx, dbgen.TouchSessionParams{
			TokenHash: hash, Now: now, StaleBefore: now.Add(-BumpEvery),
		}); err != nil {
			return dbgen.User{}, false, err
		}
	}
	return dbgen.User{ID: row.ID, Email: row.Email, PasswordHash: row.PasswordHash, CreatedAt: row.CreatedAt}, true, nil
}

// Delete removes the session for the raw cookie token (logout, login rotation).
func Delete(ctx context.Context, db dbgen.DBTX, raw string) error {
	return dbgen.New(db).DeleteSession(ctx, HashToken(raw))
}

// Prune deletes sessions past IdleTimeout or MaxAge and returns how many. Lookup
// already refuses them; this only keeps the table small.
func Prune(ctx context.Context, db dbgen.DBTX, now time.Time) (int64, error) {
	return dbgen.New(db).DeleteExpiredSessions(ctx, dbgen.DeleteExpiredSessionsParams{Now: now, IdleCutoff: now.Add(-IdleTimeout)})
}
