package web

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/ratelimit"
)

// limits are the rate limiters on the credential forms. All check before
// argon2 runs.
type limits struct {
	loginIP      *ratelimit.Limiter // failed sign-ins per client IP
	loginEmail   *ratelimit.Limiter // failed sign-ins per normalised email, known or not
	registerIP   *ratelimit.Limiter // registration attempts per client IP, successes included
	passwordUser *ratelimit.Limiter // failed current-password checks per User
}

func newLimits(now func() time.Time) limits {
	return limits{
		loginIP:      ratelimit.New("login-ip", 20, 15*time.Minute, now),
		loginEmail:   ratelimit.New("login-email", 10, time.Hour, now),
		registerIP:   ratelimit.New("register-ip", 5, time.Hour, now),
		passwordUser: ratelimit.New("password-user", 10, time.Hour, now),
	}
}

// limitCheck pairs a limiter with the key to check. secret keys (emails) are
// hashed in logs.
type limitCheck struct {
	l      *ratelimit.Limiter
	key    string
	secret bool
}

// clientIP is the key for the client behind r.
func (s *server) clientIP(r *http.Request) string {
	h := s.opts.TrustedProxyHeader
	return ratelimit.ClientKey(r.RemoteAddr, h, r.Header.Get(h))
}

// tripped reports whether any check is blocked, logging one warn line per
// tripped limiter. retry is the longest wait among them.
func (s *server) tripped(checks ...limitCheck) (retry time.Duration, blocked bool) {
	for _, c := range checks {
		d, b := c.l.Blocked(c.key)
		if !b {
			continue
		}
		blocked = true
		retry = max(retry, d)
		key := c.key
		if c.secret {
			sum := sha256.Sum256([]byte(key))
			key = hex.EncodeToString(sum[:8])
		}
		s.log.Warn("rate limit tripped", "limiter", c.l.Name(), "key", key)
	}
	return retry, blocked
}

// tooMany sets Retry-After and returns the message the form shows.
func tooMany(w http.ResponseWriter, retry time.Duration) string {
	secs := int(retry.Round(0)/time.Second) + 1 // round up; never advertise 0
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	mins := (secs + 59) / 60
	unit := "minutes"
	if mins == 1 {
		unit = "minute"
	}
	return fmt.Sprintf("Too many attempts. Try again in %d %s.", mins, unit)
}
