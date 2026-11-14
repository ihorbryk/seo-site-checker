package user_test

import (
	"strings"
	"testing"

	"github.com/ihorbryk/seo-site-checker/internal/user"
)

func TestNormalizeEmail(t *testing.T) {
	long := strings.Repeat("a", 242) + "@example.com" // 254
	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{"me@example.com", "me@example.com", true},
		{"  Me@Example.COM \t", "me@example.com", true},
		{"Me <me@example.com>", "", false},
		{"<me@example.com>", "", false},
		{"me@localhost", "", false},
		{"me@", "", false},
		{"", "", false},
		{"two words@example.com", "", false},
		{long, long, true},
		{"a" + long, "", false},
	} {
		got, err := user.NormalizeEmail(tc.in)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("NormalizeEmail(%q) = %q, %v; want %q ok=%v", tc.in, got, err, tc.want, tc.ok)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	for _, tc := range []struct {
		name, pw, msg string
	}{
		{"14 runes", strings.Repeat("a", 14), "Use at least 15 characters (this one has 14)."},
		{"15 runes", strings.Repeat("a", 15), ""},
		{"14 multibyte runes", strings.Repeat("é", 14), "Use at least 15 characters (this one has 14)."},
		{"15 multibyte runes", strings.Repeat("é", 15), ""},
		{"1024 bytes", strings.Repeat("a", 1024), ""},
		{"1025 bytes", strings.Repeat("a", 1025), "Use at most 1024 bytes."},
		{"512 two-byte runes", strings.Repeat("é", 512), ""},
		{"513 two-byte runes", strings.Repeat("é", 513), "Use at most 1024 bytes."},
		{"spaces count, no trim", "  " + strings.Repeat("a", 13), ""},
		{"empty", "", "Use at least 15 characters (this one has 0)."},
	} {
		err := user.ValidatePassword(tc.pw)
		switch {
		case tc.msg == "" && err != nil:
			t.Errorf("%s: unexpected %v", tc.name, err)
		case tc.msg != "" && (err == nil || err.Error() != tc.msg):
			t.Errorf("%s: got %v, want %q", tc.name, err, tc.msg)
		}
	}
}

var cheap = user.Params{MemoryKiB: 64, Time: 1, Threads: 1}

func TestHashVerify(t *testing.T) {
	h := user.NewHasher(cheap)
	hash, err := h.Hash("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=64,t=1,p=1$") {
		t.Fatalf("not PHC argon2id: %s", hash)
	}
	other, _ := h.Hash("correct horse battery")
	if other == hash {
		t.Fatal("salt must differ")
	}
	ok, rehash := h.Verify("correct horse battery", hash)
	if !ok || rehash {
		t.Fatalf("ok=%v rehash=%v", ok, rehash)
	}
	if ok, _ := h.Verify("wrong", hash); ok {
		t.Fatal("wrong password verified")
	}
}

func TestVerifyFlagsRehashWhenParamsDiffer(t *testing.T) {
	old, _ := user.NewHasher(cheap).Hash("correct horse battery")
	now := user.NewHasher(user.Params{MemoryKiB: 128, Time: 1, Threads: 1})
	ok, rehash := now.Verify("correct horse battery", old)
	if !ok || !rehash {
		t.Fatalf("ok=%v rehash=%v", ok, rehash)
	}
}

func TestVerifyRejectsLockedAndMalformed(t *testing.T) {
	h := user.NewHasher(cheap)
	for _, hash := range []string{"!", "", "garbage", "$argon2id$v=19$m=x,t=1,p=1$AAAA$AAAA", "$bcrypt$x"} {
		if ok, _ := h.Verify("!", hash); ok {
			t.Errorf("%q verified", hash)
		}
		if ok, _ := h.Verify("anything", hash); ok {
			t.Errorf("%q verified", hash)
		}
	}
}

func TestConcurrentHashesAreCapped(t *testing.T) {
	h := user.NewHasher(cheap)
	if h.MaxConcurrent() < 1 {
		t.Fatal("cap must be positive")
	}
	done := make(chan error)
	for range 20 {
		go func() { _, err := h.Hash("correct horse battery"); done <- err }()
	}
	for range 20 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}
