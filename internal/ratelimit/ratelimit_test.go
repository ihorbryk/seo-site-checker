package ratelimit_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/ratelimit"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newClock() *clock { return &clock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)} }

func TestReserveRefusesOnceMaxHitsAreHeld(t *testing.T) {
	c := newClock()
	l := ratelimit.New("login-ip", 3, time.Hour, c.now)
	for i := 0; i < 3; i++ {
		if v := l.Reserve("a"); !v.Allowed {
			t.Fatalf("refused after only %d hits", i)
		}
	}
	v := l.Reserve("a")
	if v.Allowed || v.RetryAfter != time.Hour {
		t.Fatalf("verdict %+v, want refused for the full hour", v)
	}
	if !l.Reserve("b").Allowed {
		t.Error("another key must not be refused")
	}
}

func TestReserveIsAtomicUnderConcurrency(t *testing.T) {
	l := ratelimit.New("x", 5, time.Hour, newClock().now)
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if l.Reserve("a").Allowed {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := allowed.Load(); got != 5 {
		t.Errorf("%d reservations allowed, want exactly 5", got)
	}
}

func TestRefundGivesBackTheLatestHit(t *testing.T) {
	l := ratelimit.New("x", 1, time.Hour, newClock().now)
	l.Reserve("a")
	l.Refund("a")
	if !l.Reserve("a").Allowed {
		t.Error("refunded hit still counted")
	}
	l.Refund("nobody") // harmless
}

func TestOnlyTheFirstRefusalOfATripIsMarkedTripped(t *testing.T) {
	c := newClock()
	l := ratelimit.New("x", 1, time.Hour, c.now)
	l.Reserve("a")
	if v := l.Reserve("a"); v.Allowed || !v.Tripped {
		t.Fatalf("first refusal %+v, want tripped", v)
	}
	if v := l.Reserve("a"); v.Allowed || v.Tripped {
		t.Fatalf("second refusal %+v, want refused but not tripped again", v)
	}
	c.advance(time.Hour)
	l.Reserve("a")
	if v := l.Reserve("a"); !v.Tripped {
		t.Errorf("a new trip after recovery %+v, want tripped", v)
	}
}

func TestRecoversAsTheWindowSlides(t *testing.T) {
	c := newClock()
	l := ratelimit.New("x", 2, time.Hour, c.now)
	l.Reserve("a") // t+0
	c.advance(30 * time.Minute)
	l.Reserve("a") // t+30m
	v := l.Reserve("a")
	if v.Allowed || v.RetryAfter != 30*time.Minute {
		t.Fatalf("verdict %+v, want refused for 30m until the oldest hit leaves", v)
	}
	c.advance(30 * time.Minute) // the first hit is exactly a window old
	if !l.Reserve("a").Allowed {
		t.Error("must recover once the oldest hit leaves the window")
	}
}

func TestClearForgetsAKey(t *testing.T) {
	l := ratelimit.New("x", 1, time.Hour, newClock().now)
	l.Reserve("a")
	l.Reserve("b")
	l.Clear("a")
	if !l.Reserve("a").Allowed {
		t.Error("cleared key still refused")
	}
	if l.Reserve("b").Allowed {
		t.Error("clearing one key must not clear another")
	}
}

func TestSweepDropsStaleKeys(t *testing.T) {
	c := newClock()
	l := ratelimit.New("x", 5, time.Hour, c.now)
	l.Reserve("old")
	c.advance(2 * time.Hour)
	l.Reserve("new")
	l.Sweep()
	if got := l.Len(); got != 1 {
		t.Errorf("%d keys after sweep, want 1", got)
	}
}

func TestReserveSweepsOnItsOwn(t *testing.T) {
	c := newClock()
	l := ratelimit.New("x", 5, time.Hour, c.now)
	for _, k := range []string{"a", "b", "c"} {
		l.Reserve(k)
	}
	c.advance(3 * time.Hour)
	l.Reserve("d")
	if got := l.Len(); got != 1 {
		t.Errorf("%d keys, want stale ones swept on Reserve", got)
	}
}

func TestClientKey(t *testing.T) {
	tests := []struct {
		name, remote, header, value, want string
	}{
		{"remote addr", "203.0.113.7:5555", "", "", "203.0.113.7"},
		{"header ignored when not configured", "203.0.113.7:5555", "", "1.1.1.1", "203.0.113.7"},
		{"header absent falls back", "203.0.113.7:5555", "X-Forwarded-For", "", "203.0.113.7"},
		{"rightmost entry", "10.0.0.1:1", "X-Forwarded-For", "9.9.9.9, 1.1.1.1 , 198.51.100.4", "198.51.100.4"},
		{"single entry", "10.0.0.1:1", "X-Real-IP", "198.51.100.4", "198.51.100.4"},
		{"garbage entry falls back", "203.0.113.7:5555", "X-Forwarded-For", "1.1.1.1, nonsense", "203.0.113.7"},
		{"ipv6 keyed by /64", "[2001:db8:1:2:aaaa:bbbb:cccc:dddd]:80", "", "", "2001:db8:1:2::/64"},
		{"ipv6 siblings share a key", "[2001:db8:1:2:1::1]:80", "", "", "2001:db8:1:2::/64"},
		{"ipv6 from header", "10.0.0.1:1", "X-Forwarded-For", "2001:db8:1:2:aaaa::1", "2001:db8:1:2::/64"},
		{"ipv4-mapped ipv6 is ipv4", "[::ffff:203.0.113.7]:80", "", "", "203.0.113.7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ratelimit.ClientKey(tt.remote, tt.header, tt.value); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
