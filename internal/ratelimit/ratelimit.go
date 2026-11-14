// Package ratelimit holds the in-memory sliding-window limiter that slows
// repeated sign-in, registration and change-password attempts. State is lost
// on restart and there is no stored lockout (ADR-0001: one web instance).
package ratelimit

import (
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// Limiter allows at most max recorded hits per key within a sliding window.
// Callers Reserve a hit before the expensive work.
type Limiter struct {
	name   string
	max    int
	window time.Duration
	now    func() time.Time

	mu        sync.Mutex
	hits      map[string][]time.Time
	warned    map[string]bool // refused keys already reported as tripped
	lastSweep time.Time
}

// New returns a Limiter; now is the clock (nil means time.Now).
func New(name string, max int, window time.Duration, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{name: name, max: max, window: window, now: now, hits: map[string][]time.Time{}, warned: map[string]bool{}, lastSweep: now()}
}

// Name identifies the limiter in logs.
func (l *Limiter) Name() string { return l.name }

// Verdict is the outcome of Reserve.
type Verdict struct {
	// Allowed is true when the hit was counted; the caller may go ahead.
	Allowed bool
	// RetryAfter is how long until the oldest counted hit leaves the window
	// (refused hits only).
	RetryAfter time.Duration
	// Tripped is true on the first refusal of a key since it last had room,
	// so callers log one line per trip rather than one per refused request.
	Tripped bool
}

// Reserve counts one hit for key if it has room, checking and counting under
// one lock so concurrent requests cannot all pass the check before any is
// recorded. Callers that should not charge a hit (a success, or a request
// refused by another limiter) give it back with Refund.
func (l *Limiter) Reserve(key string) Verdict {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	// Stale keys are swept lazily, here, once per window: memory stays bounded
	// without a background goroutine.
	if now.Sub(l.lastSweep) >= l.window {
		l.sweep(now)
	}
	live := l.live(key, now)
	if len(live) >= l.max {
		v := Verdict{RetryAfter: live[len(live)-l.max].Add(l.window).Sub(now), Tripped: !l.warned[key]}
		l.warned[key] = true
		return v
	}
	delete(l.warned, key)
	l.hits[key] = append(live, now)
	return Verdict{Allowed: true}
}

// Refund gives back the most recent hit of key.
func (l *Limiter) Refund(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	hs := l.hits[key]
	if len(hs) == 0 {
		return
	}
	if len(hs) == 1 {
		delete(l.hits, key)
	} else {
		l.hits[key] = hs[:len(hs)-1]
	}
	delete(l.warned, key)
}

// Clear forgets key's hits.
func (l *Limiter) Clear(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, key)
	delete(l.warned, key)
}

// Sweep drops keys whose hits have all left the window. Reserve calls it once
// per window, so memory stays bounded without a background goroutine.
func (l *Limiter) Sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(l.now())
}

// Len is the number of keys currently held.
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.hits)
}

func (l *Limiter) sweep(now time.Time) {
	for k := range l.hits {
		l.live(k, now) // deletes the key once nothing is left
	}
	for k := range l.warned {
		if _, held := l.hits[k]; !held {
			delete(l.warned, k)
		}
	}
	l.lastSweep = now
}

// live trims key's expired hits and returns the rest. Hits are in time
// order, so the expired ones are a prefix.
func (l *Limiter) live(key string, now time.Time) []time.Time {
	hs := l.hits[key]
	cutoff := now.Add(-l.window)
	i := 0
	for i < len(hs) && !hs[i].After(cutoff) {
		i++
	}
	if i == len(hs) {
		delete(l.hits, key)
		return nil
	}
	hs = hs[i:]
	l.hits[key] = hs
	return hs
}

// ClientKey names the client a request came from: the rightmost entry of
// header's value when header is configured and the entry parses, else the
// host of remoteAddr. IPv6 addresses are keyed by their /64 prefix.
func ClientKey(remoteAddr, header, headerValue string) string {
	if header != "" {
		parts := strings.Split(headerValue, ",")
		if addr, err := netip.ParseAddr(strings.TrimSpace(parts[len(parts)-1])); err == nil {
			return keyOf(addr)
		}
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return remoteAddr
	}
	return keyOf(addr)
}

func keyOf(a netip.Addr) string {
	a = a.Unmap()
	if a.Is6() {
		p, _ := a.Prefix(64)
		return p.String()
	}
	return a.String()
}
