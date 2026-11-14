// Package user holds the rules every way of creating or changing a User
// shares: email normalisation and validation, password rules and argon2id
// hashing.
package user

import (
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"unicode/utf8"
)

// Limits on credentials.
const (
	MaxEmailLen      = 254
	MinPasswordRunes = 15
	MaxPasswordBytes = 1024
)

// ErrInvalidEmail is returned by NormalizeEmail for an unusable address.
var ErrInvalidEmail = errors.New("Enter an email address like name@example.com.")

// NormalizeEmail trims and lowercases s and checks it is a bare address with
// a dotted domain of at most 254 characters.
func NormalizeEmail(s string) (string, error) {
	e := strings.ToLower(strings.TrimSpace(s))
	if len(e) > MaxEmailLen {
		return "", ErrInvalidEmail
	}
	a, err := mail.ParseAddress(e)
	if err != nil || a.Address != e || a.Name != "" {
		return "", ErrInvalidEmail
	}
	at := strings.LastIndexByte(e, '@')
	if at < 0 || !strings.Contains(e[at+1:], ".") {
		return "", ErrInvalidEmail
	}
	return e, nil
}

// ValidatePassword enforces length only: no composition rules, no trimming.
// The error text is shown to the User as is.
func ValidatePassword(pw string) error {
	if n := utf8.RuneCountInString(pw); n < MinPasswordRunes {
		return fmt.Errorf("Use at least %d characters (this one has %d).", MinPasswordRunes, n)
	}
	if len(pw) > MaxPasswordBytes {
		return fmt.Errorf("Use at most %d bytes.", MaxPasswordBytes)
	}
	return nil
}
