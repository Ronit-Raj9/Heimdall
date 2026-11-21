package keystore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// Key statuses: set to untested on create and whenever the value changes,
// then by the test call (ok, invalid: the provider refused the key, error:
// the provider could not be reached or answered something unexpected).
const (
	StatusUntested = "untested"
	StatusOK       = "ok"
	StatusInvalid  = "invalid"
	StatusError    = "error"
)

// Providers are the provider ids a provider key may name.
var Providers = []string{"openai", "anthropic", "gemini", "openrouter", "groq", "sarvam", "openai-compatible"}

// Limits on stored fields.
const (
	MaxValueBytes = 8 << 10
	MaxLabelChars = 200
	MaxURLChars   = 500
	MaxModels     = 200
	MaxModelChars = 200
	MaxBudgetUSD  = 1_000_000
)

// KnownProvider reports whether p is one of Providers.
func KnownProvider(p string) bool {
	for _, q := range Providers {
		if p == q {
			return true
		}
	}
	return false
}

// normalize trims and validates a Put. create requires a value.
func normalize(k Kind, in Put, create bool) (Put, error) {
	if k != KindProvider && k != KindSecret {
		return in, fmt.Errorf("%w: kind must be provider or secret", ErrInvalid)
	}
	in.Provider = strings.TrimSpace(in.Provider)
	in.Label = strings.TrimSpace(in.Label)
	in.BaseURL = strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	in.Value = strings.TrimSpace(in.Value)
	if create && in.Value == "" {
		return in, fmt.Errorf("%w: the key value is required", ErrInvalid)
	}
	if len(in.Value) > MaxValueBytes {
		return in, fmt.Errorf("%w: the key value is longer than %d bytes", ErrInvalid, MaxValueBytes)
	}
	if k == KindProvider && !KnownProvider(in.Provider) {
		return in, fmt.Errorf("%w: provider must be one of %s", ErrInvalid, strings.Join(Providers, ", "))
	}
	if k == KindSecret && in.Provider == "" && in.Label == "" {
		return in, fmt.Errorf("%w: a secret needs a name", ErrInvalid)
	}
	if utf8.RuneCountInString(in.Label) > MaxLabelChars || utf8.RuneCountInString(in.Provider) > MaxLabelChars {
		return in, fmt.Errorf("%w: label is longer than %d characters", ErrInvalid, MaxLabelChars)
	}
	if in.BaseURL != "" {
		u, err := url.Parse(in.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(in.BaseURL) > MaxURLChars {
			return in, fmt.Errorf("%w: base URL must be an http(s) URL such as https://api.example.com/v1", ErrInvalid)
		}
		if u.User != nil {
			return in, fmt.Errorf("%w: base URL must not carry credentials; put the key in the value", ErrInvalid)
		}
	}
	if k == KindProvider && in.Provider == "openai-compatible" && in.BaseURL == "" {
		return in, fmt.Errorf("%w: an OpenAI-compatible key needs a base URL", ErrInvalid)
	}
	if in.BudgetUSD < 0 || in.BudgetUSD > MaxBudgetUSD || in.BudgetUSD != in.BudgetUSD {
		return in, fmt.Errorf("%w: budget must be between 0 (no limit) and %d USD", ErrInvalid, MaxBudgetUSD)
	}
	if len(in.Models) > MaxModels {
		return in, fmt.Errorf("%w: at most %d models", ErrInvalid, MaxModels)
	}
	models := make([]string, 0, len(in.Models))
	seen := map[string]bool{}
	for _, m := range in.Models {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] {
			continue
		}
		if utf8.RuneCountInString(m) > MaxModelChars {
			return in, fmt.Errorf("%w: model name longer than %d characters", ErrInvalid, MaxModelChars)
		}
		seen[m] = true
		models = append(models, m)
	}
	in.Models = models
	return in, nil
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "key_" + hex.EncodeToString(b)
}

// last4 is what lists show of a value: its last 4 characters, or nothing
// for a value of 8 characters or fewer (too short to show a part of).
func last4(v string) string {
	if len(v) <= 8 {
		return ""
	}
	return v[len(v)-4:]
}

// spendMonth is the calendar month (UTC) a spend counts toward.
func spendMonth(t time.Time) string { return t.UTC().Format("2006-01") }

// aad binds a ciphertext to its org and key id.
func aad(orgID, id string) []byte { return []byte("heimdall-keystore-v1\x00" + orgID + "\x00" + id) }

func seal(master []byte, orgID, id, value string) (string, error) {
	block, err := aes.NewCipher(master)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := gcm.Seal(nonce, nonce, []byte(value), aad(orgID, id))
	return "v1:" + base64.StdEncoding.EncodeToString(out), nil
}

func open(master []byte, orgID, id, sealed string) (string, error) {
	raw, ok := strings.CutPrefix(sealed, "v1:")
	if !ok {
		return "", fmt.Errorf("keystore: unknown ciphertext version")
	}
	b, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return "", fmt.Errorf("keystore: corrupt ciphertext")
	}
	block, err := aes.NewCipher(master)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(b) < gcm.NonceSize() {
		return "", fmt.Errorf("keystore: corrupt ciphertext")
	}
	pt, err := gcm.Open(nil, b[:gcm.NonceSize()], b[gcm.NonceSize():], aad(orgID, id))
	if err != nil {
		return "", fmt.Errorf("keystore: cannot decrypt key %s (was the master key changed?)", id)
	}
	return string(pt), nil
}
