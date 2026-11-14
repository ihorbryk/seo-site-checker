package user

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	saltLen       = 16
	keyLen        = 32
	maxConcurrent = 4
)

// Params are the argon2id cost parameters.
type Params struct {
	MemoryKiB uint32
	Time      uint32
	Threads   uint8
}

// Hasher hashes and verifies passwords with argon2id, running at most
// maxConcurrent hashes at once (each allocates MemoryKiB).
type Hasher struct {
	params Params
	sem    chan struct{}
}

// NewHasher returns a Hasher that creates hashes with p.
func NewHasher(p Params) *Hasher {
	return &Hasher{params: p, sem: make(chan struct{}, maxConcurrent)}
}

// MaxConcurrent is the cap on simultaneous hash computations.
func (h *Hasher) MaxConcurrent() int { return cap(h.sem) }

func (h *Hasher) derive(pw string, salt []byte, p Params) []byte {
	h.sem <- struct{}{}
	defer func() { <-h.sem }()
	return argon2.IDKey([]byte(pw), salt, p.Time, p.MemoryKiB, p.Threads, keyLen)
}

// Hash returns the PHC-encoded argon2id hash of pw.
func (h *Hasher) Hash(pw string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := h.derive(pw, salt, h.params)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version,
		h.params.MemoryKiB, h.params.Time, h.params.Threads,
		b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// Verify reports whether pw matches hash, and whether the hash was made with
// parameters other than the current ones and should be replaced. The locked
// hash "!" and anything malformed never verify, but still cost one derivation
// so timing does not tell them apart from a wrong password.
func (h *Hasher) Verify(pw, hash string) (ok, needsRehash bool) {
	p, salt, want, valid := parse(hash)
	if !valid {
		h.derive(pw, make([]byte, saltLen), h.params)
		return false, false
	}
	got := h.derive(pw, salt, p)
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return false, false
	}
	return true, p != h.params
}

func parse(hash string) (p Params, salt, key []byte, ok bool) {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return
	}
	var version int
	if n, err := fmt.Sscanf(parts[2], "v=%d", &version); n != 1 || err != nil || version != argon2.Version {
		return
	}
	var m, t uint32
	var th uint8
	if n, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &th); n != 3 || err != nil {
		return
	}
	if m == 0 || t == 0 || th == 0 {
		return
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return
	}
	key, err = b64.DecodeString(parts[5])
	if err != nil || len(key) == 0 {
		return
	}
	return Params{MemoryKiB: m, Time: t, Threads: th}, salt, key, true
}
