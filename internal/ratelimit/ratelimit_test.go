package ratelimit_test

import (
	"testing"
	"time"

	"github.com/ihorbryk/seo-site-checker/internal/ratelimit"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newClock() *clock { return &clock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)} }

func TestBlocksOnceThresholdHitsAreRecorded(t *testing.T) {
	c := newClock()
	l := ratelimit.New("login-ip", 3, time.Hour, c.now)
	for i := 0; i < 3; i++ {
		if _, blocked := l.Blocked("a"); blocked {
			t.Fatalf("blocked after only %d hits", i)
		}
		l.Record("a")
	}
	retry, blocked := l.Blocked("a")
	if !blocked || retry != time.Hour {
		t.Fatalf("blocked=%v retry=%v, want blocked for the full hour", blocked, retry)
	}
	if _, blocked := l.Blocked("b"); blocked {
		t.Error("another key must not be blocked")
	}
}

func TestRecoversAsTheWindowSlides(t *testing.T) {
	c := newClock()
	l := ratelimit.New("x", 2, time.Hour, c.now)
	l.Record("a") // t+0
	c.advance(30 * time.Minute)
	l.Record("a") // t+30m
	retry, blocked := l.Blocked("a")
	if !blocked || retry != 30*time.Minute {
		t.Fatalf("blocked=%v retry=%v, want 30m until the oldest hit leaves", blocked, retry)
	}
	c.advance(30 * time.Minute) // the first hit is exactly a window old
	if _, blocked := l.Blocked("a"); blocked {
		t.Error("must recover once the oldest hit leaves the window")
	}
}

func TestClearForgetsAKey(t *testing.T) {
	l := ratelimit.New("x", 1, time.Hour, newClock().now)
	l.Record("a")
	l.Record("b")
	l.Clear("a")
	if _, blocked := l.Blocked("a"); blocked {
		t.Error("cleared key still blocked")
	}
	if _, blocked := l.Blocked("b"); !blocked {
		t.Error("clearing one key must not clear another")
	}
}

func TestSweepDropsStaleKeys(t *testing.T) {
	c := newClock()
	l := ratelimit.New("x", 5, time.Hour, c.now)
	l.Record("old")
	c.advance(2 * time.Hour)
	l.Record("new")
	l.Sweep()
	if got := l.Len(); got != 1 {
		t.Errorf("%d keys after sweep, want 1", got)
	}
}

func TestRecordSweepsOnItsOwn(t *testing.T) {
	c := newClock()
	l := ratelimit.New("x", 5, time.Hour, c.now)
	for _, k := range []string{"a", "b", "c"} {
		l.Record(k)
	}
	c.advance(3 * time.Hour)
	l.Record("d")
	if got := l.Len(); got != 1 {
		t.Errorf("%d keys, want stale ones swept on Record", got)
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
