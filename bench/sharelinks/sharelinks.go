// Package sharelinks keeps the read-only share links of one organization
// (lane X14): a signed link to one report card, evaluation, verdict, run
// trace or leaderboard view that anyone holding it may open without an
// account, until it expires or is revoked.
//
// A link's token is <org id>.<share id>.<signature>. The signature is an
// HMAC-SHA256 of "<org id>.<share id>" under a random 32-byte key kept in
// the organization's project folder (.heimdall/share.key, mode 0600), so a
// token cannot be guessed or moved to another share or organization. The
// share records live beside it in .heimdall/shares.json; projectfs never
// serves .heimdall, so both are reachable only through the share routes. A
// token is accepted only while its record exists, is not revoked and has
// not expired: revoking is immediate, and deleting the key file revokes
// every link of the organization at once.
//
// The hosted server hands out the opaque form instead (fix wave 3, N293):
// s_<HMAC-SHA256 of "v2|<share id>" under the same key>, which names no
// organization. The platform's share index maps a hash of it to the org and
// the share id. Old links keep working and are moved to the opaque form.
package sharelinks

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Kinds of shared item.
const (
	KindEvaluation  = "evaluation"  // an evaluation and its report card; target: evaluation id
	KindReportCard  = "report_card" // an agent's newest report card; target: agent id, query: version, model
	KindVerdict     = "verdict"     // a comparison's verdict; target: experiment id
	KindRunTrace    = "run_trace"   // one run and its trace; target: run id
	KindLeaderboard = "leaderboard" // a leaderboard view; target: agents or models, query: its filters
)

// Kinds lists every kind, in display order.
var Kinds = []string{KindEvaluation, KindReportCard, KindVerdict, KindRunTrace, KindLeaderboard}

// Link states.
const (
	StateActive  = "active"
	StateExpired = "expired"
	StateRevoked = "revoked"
)

// Limits.
const (
	// MaxActive is how many links that are neither revoked nor expired an
	// organization may hold at once.
	MaxActive = 500
	// MaxExpiryDays is the longest expiry a link may be given; 0 means the
	// link never expires.
	MaxExpiryDays = 365
	// MaxLabel is the longest label, in bytes.
	MaxLabel = 120
)

// Errors. ErrNotFound, ErrExpired and ErrRevoked are what a visitor's token
// can fail with; the share routes answer them alike so a token never tells
// which.
var (
	ErrBadToken = errors.New("share link is not valid")
	ErrNotFound = errors.New("share link not found")
	ErrExpired  = errors.New("share link has expired")
	ErrRevoked  = errors.New("share link was revoked")
	ErrInvalid  = errors.New("invalid share link request")
	ErrTooMany  = errors.New("too many active share links")
)

// Share is one link.
type Share struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Target string `json:"target"`
	// Query holds the view's filters as a URL query (report_card: version,
	// model; leaderboard: its filters). Only keys AllowedQuery admits.
	Query string `json:"query,omitempty"`
	Label string `json:"label,omitempty"`
	// HidePrompts blanks prompts, instructions, tool arguments and agent
	// text in the shared page. Secrets are always removed.
	HidePrompts bool       `json:"hide_prompts"`
	CreatedAt   time.Time  `json:"created_at"`
	CreatedBy   string     `json:"created_by,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"` // nil: never
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
	RevokedBy   string     `json:"revoked_by,omitempty"`
}

// State is active, expired or revoked at now.
func (s *Share) State(now time.Time) string {
	switch {
	case s.RevokedAt != nil:
		return StateRevoked
	case s.ExpiresAt != nil && !now.Before(*s.ExpiresAt):
		return StateExpired
	}
	return StateActive
}

// NewShare is what a member asks for.
type NewShare struct {
	Kind          string `json:"kind"`
	Target        string `json:"target"`
	Query         string `json:"query,omitempty"`
	Label         string `json:"label,omitempty"`
	HidePrompts   bool   `json:"hide_prompts,omitempty"`
	ExpiresInDays *int   `json:"expires_in_days,omitempty"` // nil: DefaultExpiryDays; 0: never
}

// DefaultExpiryDays is the expiry of a link that names none.
const DefaultExpiryDays = 30

var (
	targetRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	idRe     = regexp.MustCompile(`^shr_[0-9a-f]{20}$`)
)

// allowedQuery is, per kind and target, the query keys a shared view may
// carry. The held-out board (split) is never shareable.
func allowedQuery(kind, target string) map[string]bool {
	switch kind {
	case KindReportCard:
		return map[string]bool{"version": true, "model": true}
	case KindLeaderboard:
		if target == "agents" {
			return map[string]bool{"capability": true, "model": true}
		}
		return map[string]bool{"k": true, "suite": true, "condition": true, "scope": true, "synthetic": true, "grade_source": true}
	}
	return nil
}

// CleanQuery keeps the keys the kind admits, drops empty values and
// returns the canonical encoding. A key it does not admit is an error, so a
// member never thinks a filter is shared when it is not.
func CleanQuery(kind, target, raw string) (string, error) {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "?")
	if raw == "" {
		return "", nil
	}
	vals, err := url.ParseQuery(raw)
	if err != nil {
		return "", fmt.Errorf("%w: query: %v", ErrInvalid, err)
	}
	allowed := allowedQuery(kind, target)
	out := url.Values{}
	for k, vs := range vals {
		if !allowed[k] {
			return "", fmt.Errorf("%w: a %s link cannot carry the filter %q", ErrInvalid, kind, k)
		}
		for _, v := range vs {
			if v = strings.TrimSpace(v); v != "" {
				if len(v) > 200 {
					return "", fmt.Errorf("%w: filter %q is too long", ErrInvalid, k)
				}
				out.Add(k, v)
			}
		}
	}
	return out.Encode(), nil
}

// Validate checks a request and returns the share it describes (without id
// or timestamps).
func (n NewShare) Validate() (Share, error) {
	s := Share{Kind: strings.TrimSpace(n.Kind), Target: strings.TrimSpace(n.Target), Label: strings.TrimSpace(n.Label), HidePrompts: n.HidePrompts}
	known := false
	for _, k := range Kinds {
		known = known || k == s.Kind
	}
	if !known {
		return Share{}, fmt.Errorf("%w: kind must be one of %s", ErrInvalid, strings.Join(Kinds, ", "))
	}
	if !targetRe.MatchString(s.Target) || s.Target == "." || s.Target == ".." {
		return Share{}, fmt.Errorf("%w: target %q is not an id", ErrInvalid, s.Target)
	}
	if s.Kind == KindLeaderboard && s.Target != "agents" && s.Target != "models" {
		return Share{}, fmt.Errorf("%w: a leaderboard link's target is agents or models", ErrInvalid)
	}
	if len(s.Label) > MaxLabel {
		return Share{}, fmt.Errorf("%w: label is longer than %d characters", ErrInvalid, MaxLabel)
	}
	q, err := CleanQuery(s.Kind, s.Target, n.Query)
	if err != nil {
		return Share{}, err
	}
	s.Query = q
	if n.ExpiresInDays != nil && (*n.ExpiresInDays < 0 || *n.ExpiresInDays > MaxExpiryDays) {
		return Share{}, fmt.Errorf("%w: expires_in_days must be 0 (never) to %d", ErrInvalid, MaxExpiryDays)
	}
	return s, nil
}

// Store is the share links of one project root.
type Store struct {
	dir string
	now func() time.Time
}

// Open returns the store of project root.
func Open(root string) *Store {
	return &Store{dir: filepath.Join(root, ".heimdall"), now: func() time.Time { return time.Now().UTC() }}
}

// WithClock returns s reading the time from now (tests).
func (s *Store) WithClock(now func() time.Time) *Store {
	c := *s
	c.now = now
	return &c
}

var locks sync.Map // dir -> *sync.Mutex

func (s *Store) lock() func() {
	m, _ := locks.LoadOrStore(s.dir, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func (s *Store) sharesPath() string { return filepath.Join(s.dir, "shares.json") }
func (s *Store) keyPath() string    { return filepath.Join(s.dir, "share.key") }

type file struct {
	Shares []Share `json:"shares"`
}

func (s *Store) read() ([]Share, error) {
	data, err := os.ReadFile(s.sharesPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("read share links: %w", err)
	}
	return f.Shares, nil
}

func (s *Store) write(shares []Share) error {
	data, err := json.MarshalIndent(file{Shares: shares}, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.dir, s.sharesPath(), append(data, '\n'), 0o600)
}

func writeAtomic(dir, path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// key returns the signing key, creating it on first use. Callers hold the
// lock.
func (s *Store) key(create bool) ([]byte, error) {
	data, err := os.ReadFile(s.keyPath())
	if err == nil {
		k, derr := hex.DecodeString(strings.TrimSpace(string(data)))
		if derr != nil || len(k) != 32 {
			return nil, errors.New("share link key is unreadable: delete .heimdall/share.key to start over (this revokes every link)")
		}
		return k, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if !create {
		return nil, ErrNotFound
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	if err := writeAtomic(s.dir, s.keyPath(), []byte(hex.EncodeToString(k)+"\n"), 0o600); err != nil {
		return nil, err
	}
	return k, nil
}

func sign(key []byte, orgID, id string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(orgID + "." + id))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// Token returns the link token of share id in orgID.
func (s *Store) Token(orgID, id string) (string, error) {
	unlock := s.lock()
	defer unlock()
	k, err := s.key(true)
	if err != nil {
		return "", err
	}
	return orgID + "." + id + "." + sign(k, orgID, id), nil
}

// TokenV2Prefix starts every opaque (v2) token.
const TokenV2Prefix = "s_"

// IsTokenV2 reports whether token has the opaque form.
func IsTokenV2(token string) bool {
	return strings.HasPrefix(token, TokenV2Prefix) && len(token) == len(TokenV2Prefix)+43 && !strings.Contains(token, ".")
}

func signV2(key []byte, id string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("v2|" + id))
	return TokenV2Prefix + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// TokenV2 returns the opaque token of share id: it holds no organization
// id and is the same every time for one link.
func (s *Store) TokenV2(id string) (string, error) {
	unlock := s.lock()
	defer unlock()
	k, err := s.key(true)
	if err != nil {
		return "", err
	}
	return signV2(k, id), nil
}

// Create stores a new link for orgID made by actor.
func (s *Store) Create(n NewShare, actor string) (*Share, error) {
	sh, err := n.Validate()
	if err != nil {
		return nil, err
	}
	unlock := s.lock()
	defer unlock()
	shares, err := s.read()
	if err != nil {
		return nil, err
	}
	now := s.now()
	active := 0
	for i := range shares {
		if shares[i].State(now) == StateActive {
			active++
		}
	}
	if active >= MaxActive {
		return nil, fmt.Errorf("%w: revoke some of the %d active links first", ErrTooMany, active)
	}
	if _, err := s.key(true); err != nil {
		return nil, err
	}
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	sh.ID = "shr_" + hex.EncodeToString(b)
	sh.CreatedAt = now
	sh.CreatedBy = actor
	days := DefaultExpiryDays
	if n.ExpiresInDays != nil {
		days = *n.ExpiresInDays
	}
	if days > 0 {
		exp := now.Add(time.Duration(days) * 24 * time.Hour)
		sh.ExpiresAt = &exp
	}
	shares = append(shares, sh)
	if err := s.write(shares); err != nil {
		return nil, err
	}
	return &sh, nil
}

// List returns every link, newest first, optionally only those of kind and
// target (empty matches any).
func (s *Store) List(kind, target string) ([]Share, error) {
	unlock := s.lock()
	defer unlock()
	shares, err := s.read()
	if err != nil {
		return nil, err
	}
	out := make([]Share, 0, len(shares))
	for _, sh := range shares {
		if (kind == "" || sh.Kind == kind) && (target == "" || sh.Target == target) {
			out = append(out, sh)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// Get returns one link by id.
func (s *Store) Get(id string) (*Share, error) {
	if !idRe.MatchString(id) {
		return nil, ErrNotFound
	}
	unlock := s.lock()
	defer unlock()
	shares, err := s.read()
	if err != nil {
		return nil, err
	}
	for i := range shares {
		if shares[i].ID == id {
			sh := shares[i]
			return &sh, nil
		}
	}
	return nil, ErrNotFound
}

// Revoke ends a link at once. Revoking a revoked link is a no-op that
// returns it unchanged.
func (s *Store) Revoke(id, actor string) (*Share, error) {
	if !idRe.MatchString(id) {
		return nil, ErrNotFound
	}
	unlock := s.lock()
	defer unlock()
	shares, err := s.read()
	if err != nil {
		return nil, err
	}
	for i := range shares {
		if shares[i].ID != id {
			continue
		}
		if shares[i].RevokedAt == nil {
			now := s.now()
			shares[i].RevokedAt = &now
			shares[i].RevokedBy = actor
			if err := s.write(shares); err != nil {
				return nil, err
			}
		}
		sh := shares[i]
		return &sh, nil
	}
	return nil, ErrNotFound
}

// Verify checks a visitor's token for orgID: well formed, signed with this
// organization's key, and naming an active link. It returns ErrBadToken,
// ErrNotFound, ErrExpired or ErrRevoked otherwise. An opaque (v2) token is
// matched against every link of the store.
func (s *Store) Verify(orgID, token string) (*Share, error) {
	if strings.HasPrefix(token, TokenV2Prefix) {
		return s.verifyV2(token)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != orgID || !idRe.MatchString(parts[1]) || len(parts[2]) != 43 {
		return nil, ErrBadToken
	}
	unlock := s.lock()
	k, err := s.key(false)
	if err != nil {
		unlock()
		if errors.Is(err, ErrNotFound) {
			return nil, ErrBadToken
		}
		return nil, err
	}
	want := sign(k, orgID, parts[1])
	unlock()
	if subtle.ConstantTimeCompare([]byte(want), []byte(parts[2])) != 1 {
		return nil, ErrBadToken
	}
	sh, err := s.Get(parts[1])
	if err != nil {
		return nil, err
	}
	switch sh.State(s.now()) {
	case StateRevoked:
		return nil, ErrRevoked
	case StateExpired:
		return nil, ErrExpired
	}
	return sh, nil
}

// verifyV2 finds the link whose opaque token is token, comparing every
// link's token in constant time, then checks its state as Verify does.
func (s *Store) verifyV2(token string) (*Share, error) {
	if !IsTokenV2(token) {
		return nil, ErrBadToken
	}
	unlock := s.lock()
	k, err := s.key(false)
	if err != nil {
		unlock()
		if errors.Is(err, ErrNotFound) {
			return nil, ErrBadToken
		}
		return nil, err
	}
	shares, err := s.read()
	unlock()
	if err != nil {
		return nil, err
	}
	var found *Share
	for i := range shares {
		if subtle.ConstantTimeCompare([]byte(signV2(k, shares[i].ID)), []byte(token)) == 1 {
			found = &shares[i]
		}
	}
	if found == nil {
		return nil, ErrBadToken
	}
	switch found.State(s.now()) {
	case StateRevoked:
		return nil, ErrRevoked
	case StateExpired:
		return nil, ErrExpired
	}
	sh := *found
	return &sh, nil
}
