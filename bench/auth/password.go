package auth

import (
	"bufio"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Password length limits. The upper bound keeps a single login from being
// used to make the server hash megabytes.
const (
	MinPasswordLength = 12
	MaxPasswordBytes  = 1024
)

// Argon2Params are the argon2id cost parameters, recorded inside every
// encoded hash so they can change without invalidating stored passwords.
type Argon2Params struct {
	Memory  uint32 // KiB
	Time    uint32 // passes
	Threads uint8
	SaltLen uint32
	KeyLen  uint32
}

// DefaultArgon2Params follow RFC 9106's second recommended option scaled
// for a shared server: 64 MiB, 3 passes, 2 lanes, 16-byte salt, 32-byte key.
var DefaultArgon2Params = Argon2Params{Memory: 64 * 1024, Time: 3, Threads: 2, SaltLen: 16, KeyLen: 32}

// Limits on parameters read back from a stored hash, so a corrupted or
// hostile row can not make one verification exhaust the server.
const (
	maxArgonMemory  = 1 << 20 // 1 GiB in KiB
	maxArgonTime    = 16
	maxArgonThreads = 16
	maxArgonKeyLen  = 128
	maxArgonSaltLen = 64
)

// ErrPasswordMismatch is returned by VerifyPassword for a wrong password.
var ErrPasswordMismatch = errors.New("password does not match")

// ErrWeakPassword wraps every password policy rejection.
var ErrWeakPassword = errors.New("password rejected")

// Hasher hashes and verifies passwords with one parameter set. At most
// MaxConcurrent hashes run at once (each holds Params.Memory), so a burst of
// logins queues instead of exhausting the server's memory.
type Hasher struct {
	Params Argon2Params
	// MaxConcurrent caps simultaneous argon2id runs; 0 means the number of
	// CPUs, at most 8.
	MaxConcurrent int

	semOnce   sync.Once
	sem       chan struct{}
	dummyOnce sync.Once
	dummy     string
}

// idKey runs argon2id under the concurrency cap.
func (h *Hasher) idKey(password, salt []byte, p Argon2Params, keyLen uint32) []byte {
	h.semOnce.Do(func() {
		n := h.MaxConcurrent
		if n <= 0 {
			n = min(runtime.NumCPU(), 8)
		}
		h.sem = make(chan struct{}, max(n, 1))
	})
	h.sem <- struct{}{}
	defer func() { <-h.sem }()
	return argon2.IDKey(password, salt, p.Time, p.Memory, p.Threads, keyLen)
}

// NewHasher returns a hasher using p.
func NewHasher(p Argon2Params) *Hasher { return &Hasher{Params: p} }

// Hash returns the PHC-style encoding
// $argon2id$v=19$m=<KiB>,t=<passes>,p=<lanes>$<salt>$<key>, salt and key in
// unpadded standard base64.
func (h *Hasher) Hash(password string) (string, error) {
	p := h.Params
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("hash password: read salt: %w", err)
	}
	key := h.idKey([]byte(password), salt, p, p.KeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Time, p.Threads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// Verify checks password against an encoded hash in constant time. It
// returns ErrPasswordMismatch for a wrong password and another error for a
// hash it can not parse. needsRehash is true when the password matched but
// the hash was made with other parameters than h.Params.
func (h *Hasher) Verify(encoded, password string) (needsRehash bool, err error) {
	p, salt, want, err := decodeHash(encoded)
	if err != nil {
		return false, err
	}
	got := h.idKey([]byte(password), salt, p, uint32(len(want)))
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return false, ErrPasswordMismatch
	}
	cur := h.Params
	return p.Memory != cur.Memory || p.Time != cur.Time || p.Threads != cur.Threads ||
		uint32(len(want)) != cur.KeyLen || uint32(len(salt)) != cur.SaltLen, nil
}

// BurnVerify spends the same work as verifying a password against a real
// hash and always fails. Login calls it when there is no user, or no
// password, so the response time does not tell whether an account exists.
func (h *Hasher) BurnVerify(password string) {
	h.dummyOnce.Do(func() {
		d, err := h.Hash("heimdall-dummy-password-for-timing")
		if err == nil {
			h.dummy = d
		}
	})
	if h.dummy != "" {
		_, _ = h.Verify(h.dummy, password)
	}
}

// decodeHash parses the encoding Hash writes.
func decodeHash(encoded string) (Argon2Params, []byte, []byte, error) {
	var p Argon2Params
	parts := strings.Split(encoded, "$")
	// "", "argon2id", "v=19", "m=..,t=..,p=..", salt, key
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return p, nil, nil, errors.New("password hash: not an argon2id hash")
	}
	if parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return p, nil, nil, fmt.Errorf("password hash: unsupported version %q", parts[2])
	}
	for _, kv := range strings.Split(parts[3], ",") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return p, nil, nil, errors.New("password hash: malformed parameters")
		}
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return p, nil, nil, fmt.Errorf("password hash: parameter %s: %w", k, err)
		}
		switch k {
		case "m":
			p.Memory = uint32(n)
		case "t":
			p.Time = uint32(n)
		case "p":
			if n > 255 {
				return p, nil, nil, errors.New("password hash: too many lanes")
			}
			p.Threads = uint8(n)
		default:
			return p, nil, nil, fmt.Errorf("password hash: unknown parameter %q", k)
		}
	}
	if p.Memory == 0 || p.Time == 0 || p.Threads == 0 ||
		p.Memory > maxArgonMemory || p.Time > maxArgonTime || p.Threads > maxArgonThreads ||
		p.Memory < 8*uint32(p.Threads) {
		return p, nil, nil, errors.New("password hash: parameters out of range")
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil || len(salt) < 8 || len(salt) > maxArgonSaltLen {
		return p, nil, nil, errors.New("password hash: bad salt")
	}
	key, err := b64.DecodeString(parts[5])
	if err != nil || len(key) < 16 || len(key) > maxArgonKeyLen {
		return p, nil, nil, errors.New("password hash: bad key")
	}
	p.SaltLen, p.KeyLen = uint32(len(salt)), uint32(len(key))
	return p, salt, key, nil
}

//go:embed common_passwords.txt
var commonPasswordsText string

var (
	commonOnce      sync.Once
	commonPasswords map[string]struct{}
)

// weakStems are words that stay weak whatever digits or symbols follow them
// ("password2024!", "qwerty123456").
var weakStems = []string{
	"password", "passw0rd", "p@ssword", "p@ssw0rd", "qwerty", "qwertyuiop", "asdfgh", "asdfghjkl",
	"zxcvbn", "iloveyou", "letmein", "welcome", "admin", "administrator", "changeme", "default",
	"secret", "heimdall", "bifrost", "monkey", "dragon", "football", "baseball", "abc", "abcdef",
	"abcdefgh", "trustno1", "master", "login", "test", "guest", "root", "user",
}

func loadCommon() {
	commonPasswords = map[string]struct{}{}
	sc := bufio.NewScanner(strings.NewReader(commonPasswordsText))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		commonPasswords[strings.ToLower(line)] = struct{}{}
	}
}

// CheckPasswordPolicy enforces the password rules: at least 12 characters,
// at most 1024 bytes, valid UTF-8, not the account's email, not one
// character repeated, not on the embedded list of common passwords, and not
// a common word followed only by digits and symbols. The list is small and
// catches the obvious; it is not a breach corpus.
func CheckPasswordPolicy(password, email string) error {
	if !utf8.ValidString(password) {
		return fmt.Errorf("%w: not valid UTF-8", ErrWeakPassword)
	}
	if len(password) > MaxPasswordBytes {
		return fmt.Errorf("%w: longer than %d bytes", ErrWeakPassword, MaxPasswordBytes)
	}
	if utf8.RuneCountInString(password) < MinPasswordLength {
		return fmt.Errorf("%w: shorter than %d characters", ErrWeakPassword, MinPasswordLength)
	}
	lower := strings.ToLower(password)
	if email != "" {
		e := strings.ToLower(strings.TrimSpace(email))
		local, _, _ := strings.Cut(e, "@")
		if lower == e || (len(local) >= 4 && strings.Contains(lower, local) && len(lower)-len(local) < 6) {
			return fmt.Errorf("%w: too close to the email address", ErrWeakPassword)
		}
	}
	if allSame(lower) {
		return fmt.Errorf("%w: one character repeated", ErrWeakPassword)
	}
	commonOnce.Do(loadCommon)
	if _, ok := commonPasswords[lower]; ok {
		return fmt.Errorf("%w: a commonly used password", ErrWeakPassword)
	}
	stem := strings.TrimRightFunc(lower, func(r rune) bool {
		return (r >= '0' && r <= '9') || strings.ContainsRune("!@#$%^&*()_+-=.,?~ ", r)
	})
	if stem == "" {
		stem = lower
	}
	for _, w := range weakStems {
		if stem == w || stem == w+w {
			return fmt.Errorf("%w: a common word with digits or symbols added", ErrWeakPassword)
		}
	}
	if _, ok := commonPasswords[stem]; ok && stem != lower {
		return fmt.Errorf("%w: a common password with digits or symbols added", ErrWeakPassword)
	}
	return nil
}

func allSame(s string) bool {
	first, size := utf8.DecodeRuneInString(s)
	for _, r := range s[size:] {
		if r != first {
			return false
		}
	}
	return true
}
