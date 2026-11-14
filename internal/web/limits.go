package web

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/ratelimit"
	"github.com/ihorbryk/seo-site-checker/internal/user"
)

// limits are the rate limiters on the credential forms. Each form reserves
// its hits before argon2 runs and refunds the ones a success should not cost.
type limits struct {
	loginIP      *ratelimit.Limiter // failed sign-ins per client IP
	loginEmail   *ratelimit.Limiter // failed sign-ins per hashed canonical email, known or not
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

// limitCheck pairs a limiter with the key to charge.
type limitCheck struct {
	l   *ratelimit.Limiter
	key string
}

// clientIP is the key for the client behind r.
func (s *server) clientIP(r *http.Request) string {
	h := s.opts.TrustedProxyHeader
	return ratelimit.ClientKey(r.RemoteAddr, h, r.Header.Get(h))
}

// emailKey is the limiter key for a posted email: a hash of its canonical
// form, so the key is short however long the input and never holds the
// address itself (it is logged when the limit trips).
func emailKey(posted string) string {
	sum := sha256.Sum256([]byte(user.CanonicalEmail(posted)))
	return hex.EncodeToString(sum[:8])
}

// reserve charges one hit to each check in order, atomically per limiter. At
// the first refusal it gives back the hits already taken, so a refused
// request costs nothing, and returns the wait. A key's trip is logged once,
// on its first refusal, not on every refused request.
func (s *server) reserve(checks ...limitCheck) (retry time.Duration, ok bool) {
	for i, c := range checks {
		v := c.l.Reserve(c.key)
		if v.Allowed {
			continue
		}
		for _, taken := range checks[:i] {
			taken.l.Refund(taken.key)
		}
		if v.Tripped {
			s.log.Warn("rate limit tripped", "limiter", c.l.Name(), "key", c.key)
		}
		return v.RetryAfter, false
	}
	return 0, true
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
