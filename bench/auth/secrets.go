package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// Token formats. Every secret is 32 random bytes, shown once and stored only
// as its sha256 (docs/platform-design.md rule 5).
const (
	// APITokenPrefix starts an API token: hm_<8 char lookup prefix>_<secret>.
	APITokenPrefix = "hm_"
	// RunnerTokenPrefix starts a runner token, verified by the runners area.
	RunnerTokenPrefix = "hmr_"
	// InboxTokenPrefix starts an org's inbox ingest token (gap G02,
	// bench/inboxtoken). The middleware makes no principal of it: the
	// request goes on anonymous and the ingest handler checks the token
	// against that org's stored hashes.
	InboxTokenPrefix = "hmi_"

	// SharedPoolAssignmentsPath is the route the shared runner calls with
	// the pool secret (handlers/sharedpool.go) instead of an account token;
	// so is SharedPoolAssignmentsPath/{org}/token (IsSharedPoolPath).
	SharedPoolAssignmentsPath = "/api/bench/admin/pool/assignments"

	secretBytes     = 32
	secretB64Len    = 43 // base64url, no padding, of 32 bytes
	tokenLookupLen  = 8
	lookupAlphabet  = "abcdefghijklmnopqrstuvwxyz0123456789"
	apiTokenFullLen = len(APITokenPrefix) + tokenLookupLen + 1 + secretB64Len
)

// randomSecret returns 32 random bytes as unpadded base64url.
func randomSecret() (string, error) {
	buf := make([]byte, secretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// randomLookup returns n characters of [a-z0-9], unbiased.
func randomLookup(n int) (string, error) {
	out := make([]byte, 0, n)
	buf := make([]byte, 1)
	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("read random: %w", err)
		}
		// 252 = 7*36: reject the top values so every character is equally likely.
		if buf[0] < 252 {
			out = append(out, lookupAlphabet[int(buf[0])%len(lookupAlphabet)])
		}
	}
	return string(out), nil
}

// HashSecret is the stored form of a session id, token or invitation token:
// lowercase hex sha256 of the exact string the client holds.
func HashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// equalHashes compares two stored hashes in constant time.
func equalHashes(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// NewAPIToken returns a fresh API token and its lookup prefix.
func NewAPIToken() (token, prefix string, err error) {
	prefix, err = randomLookup(tokenLookupLen)
	if err != nil {
		return "", "", err
	}
	secret, err := randomSecret()
	if err != nil {
		return "", "", err
	}
	return APITokenPrefix + prefix + "_" + secret, prefix, nil
}

// ParseAPIToken checks the shape hm_<8 [a-z0-9]>_<43 base64url> and returns
// the lookup prefix. The secret part may itself contain '_' and '-', so the
// shape is checked by position.
func ParseAPIToken(token string) (prefix string, ok bool) {
	if len(token) != apiTokenFullLen || !strings.HasPrefix(token, APITokenPrefix) {
		return "", false
	}
	prefix = token[len(APITokenPrefix) : len(APITokenPrefix)+tokenLookupLen]
	if token[len(APITokenPrefix)+tokenLookupLen] != '_' {
		return "", false
	}
	for _, c := range prefix {
		if !strings.ContainsRune(lookupAlphabet, c) {
			return "", false
		}
	}
	secret := token[len(APITokenPrefix)+tokenLookupLen+1:]
	if _, err := base64.RawURLEncoding.DecodeString(secret); err != nil {
		return "", false
	}
	return prefix, true
}

// IsSharedPoolPath reports whether path is one of the routes the shared
// runner calls with the pool secret: the assignments list and
// <assignments>/{org}/token. Both handlers check the secret themselves.
func IsSharedPoolPath(path string) bool {
	if path == SharedPoolAssignmentsPath {
		return true
	}
	rest, ok := strings.CutPrefix(path, SharedPoolAssignmentsPath+"/")
	if !ok {
		return false
	}
	org, tail, ok := strings.Cut(rest, "/")
	return ok && org != "" && tail == "token"
}
