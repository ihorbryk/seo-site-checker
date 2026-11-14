package user

import (
	"context"
	"errors"

	"github.com/ihorbryk/seo-site-checker/internal/dbgen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	// ErrEmailTaken means a User with that email already exists.
	ErrEmailTaken = errors.New("a User with this email already exists")
	// ErrNotFound means no User has that email.
	ErrNotFound = errors.New("no User with this email")
)

// Beginner starts transactions; *pgxpool.Pool satisfies it.
type Beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Create validates email and password, then inserts a User with the hashed
// password. Validation errors (ErrInvalidEmail, a password message) and
// ErrEmailTaken leave nothing written. It never overwrites an existing User.
func Create(ctx context.Context, db dbgen.DBTX, h *Hasher, email, password string) (dbgen.User, error) {
	email, err := NormalizeEmail(email)
	if err != nil {
		return dbgen.User{}, err
	}
	if err := ValidatePassword(password); err != nil {
		return dbgen.User{}, err
	}
	hash, err := h.Hash(password)
	if err != nil {
		return dbgen.User{}, err
	}
	u, err := dbgen.New(db).CreateUser(ctx, dbgen.CreateUserParams{Email: email, PasswordHash: hash})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return dbgen.User{}, ErrEmailTaken
	}
	return u, err
}

// Ensure returns the User with that email, creating it with the password when
// there is none. An existing User is returned untouched, password included.
func Ensure(ctx context.Context, db dbgen.DBTX, h *Hasher, email, password string) (u dbgen.User, created bool, err error) {
	norm, err := NormalizeEmail(email)
	if err != nil {
		return dbgen.User{}, false, err
	}
	u, err = dbgen.New(db).GetUserByEmail(ctx, norm)
	if err == nil {
		return u, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return dbgen.User{}, false, err
	}
	u, err = Create(ctx, db, h, norm, password)
	return u, err == nil, err
}

// ResetPassword replaces the User's password hash and deletes every one of
// their sessions in one transaction, returning how many were deleted.
// An unknown email is ErrNotFound.
func ResetPassword(ctx context.Context, db Beginner, h *Hasher, email, password string) (int64, error) {
	email, err := NormalizeEmail(email)
	if err != nil {
		return 0, err
	}
	if err := ValidatePassword(password); err != nil {
		return 0, err
	}
	hash, err := h.Hash(password)
	if err != nil {
		return 0, err
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	q := dbgen.New(tx)
	u, err := q.GetUserByEmailForUpdate(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if err := q.SetUserPasswordHash(ctx, dbgen.SetUserPasswordHashParams{ID: u.ID, PasswordHash: hash}); err != nil {
		return 0, err
	}
	n, err := q.DeleteSessionsByUser(ctx, u.ID)
	if err != nil {
		return 0, err
	}
	return n, tx.Commit(ctx)
}
