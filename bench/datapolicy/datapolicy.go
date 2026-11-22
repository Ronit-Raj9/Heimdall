// Package datapolicy keeps each benchmark's data policy (lane Y15): how
// personal data in imported rows is handled (pii.Policy) and who in the
// organization may see the benchmark's task content. The policies live in
// the org's project folder at .heimdall/benchmark-policies.json, outside
// the benchmark folder, so editing a benchmark's files can never widen its
// access, and the redact key at .heimdall/pii.key (0600).
package datapolicy

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/pii"
)

// MaxAllowed caps the people one benchmark's access list names.
const MaxAllowed = 500

// Access modes.
const (
	AccessOrg        = "org"        // every member of the organization (the default)
	AccessRestricted = "restricted" // admins, owners and the people listed
)

// Access says who may see a benchmark's task content.
type Access struct {
	Mode string `json:"mode"`
	// Users are user ids allowed besides admins and owners when Mode is
	// restricted.
	Users []string `json:"users,omitempty"`
}

// Policy is one benchmark's data policy.
type Policy struct {
	PII       pii.Policy `json:"pii"`
	Access    Access     `json:"access"`
	UpdatedBy string     `json:"updated_by,omitempty"`
	UpdatedAt time.Time  `json:"updated_at,omitempty"`
}

// ErrInvalid marks a refused policy.
var ErrInvalid = errors.New("invalid data policy")

// Normalize fills defaults and checks the policy.
func (p *Policy) Normalize() error {
	if err := p.PII.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if p.PII.Mode == "" {
		p.PII.Mode = pii.ModeWarn
	}
	switch p.Access.Mode {
	case "":
		p.Access.Mode = AccessOrg
	case AccessOrg, AccessRestricted:
	default:
		return fmt.Errorf("%w: access mode %q: want org or restricted", ErrInvalid, p.Access.Mode)
	}
	seen := map[string]bool{}
	users := p.Access.Users[:0:0]
	for _, u := range p.Access.Users {
		u = strings.TrimSpace(u)
		if u == "" || seen[u] {
			continue
		}
		if len(u) > 128 || strings.ContainsAny(u, "\r\n\t ") {
			return fmt.Errorf("%w: user id %q", ErrInvalid, u)
		}
		seen[u] = true
		users = append(users, u)
	}
	if len(users) > MaxAllowed {
		return fmt.Errorf("%w: at most %d people on one access list", ErrInvalid, MaxAllowed)
	}
	sort.Strings(users)
	p.Access.Users = users
	if p.Access.Mode == AccessOrg {
		p.Access.Users = nil
	}
	return nil
}

// Default is the policy of a benchmark that has none: warn on personal
// data, visible to the whole organization.
func Default() Policy {
	return Policy{PII: pii.Policy{Mode: pii.ModeWarn}, Access: Access{Mode: AccessOrg}}
}

// Viewer is who asks to see task content.
type Viewer struct {
	UserID string
	// Manager is true for an org admin or owner, and for the local
	// principal of a single-user server.
	Manager bool
}

// Allows reports whether v may see the content of a benchmark under p.
func (p Policy) Allows(v Viewer) bool {
	if p.Access.Mode != AccessRestricted || v.Manager {
		return true
	}
	return v.UserID != "" && slices.Contains(p.Access.Users, v.UserID)
}

// Store is the policies of one project root.
type Store struct{ dir string }

// Open returns the store of project root.
func Open(root string) *Store { return &Store{dir: filepath.Join(root, ".heimdall")} }

var locks sync.Map

func (s *Store) lock() func() {
	m, _ := locks.LoadOrStore(s.dir, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func (s *Store) path() string    { return filepath.Join(s.dir, "benchmark-policies.json") }
func (s *Store) keyPath() string { return filepath.Join(s.dir, "pii.key") }

type file struct {
	Benchmarks map[string]Policy `json:"benchmarks"`
}

func (s *Store) read() (map[string]Policy, error) {
	b, err := os.ReadFile(s.path())
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]Policy{}, nil
	}
	if err != nil {
		return nil, err
	}
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("read benchmark policies: %w", err)
	}
	if f.Benchmarks == nil {
		f.Benchmarks = map[string]Policy{}
	}
	return f.Benchmarks, nil
}

// Get is the policy of one benchmark (Default when none is stored).
func (s *Store) Get(benchmark string) (Policy, error) {
	unlock := s.lock()
	defer unlock()
	all, err := s.read()
	if err != nil {
		return Policy{}, err
	}
	p, ok := all[benchmark]
	if !ok {
		return Default(), nil
	}
	_ = p.Normalize()
	return p, nil
}

// IDs lists the benchmarks with a stored policy, sorted.
func (s *Store) IDs() ([]string, error) {
	unlock := s.lock()
	defer unlock()
	all, err := s.read()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(all))
	for id := range all {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

// Restricted lists the benchmarks whose access is restricted.
func (s *Store) Restricted() (map[string]Policy, error) {
	unlock := s.lock()
	defer unlock()
	all, err := s.read()
	if err != nil {
		return nil, err
	}
	out := map[string]Policy{}
	for id, p := range all {
		if p.Access.Mode == AccessRestricted {
			out[id] = p
		}
	}
	return out, nil
}

// Set stores the policy of one benchmark after normalizing it.
func (s *Store) Set(benchmark string, p Policy, actor string, now time.Time) (Policy, error) {
	if err := p.Normalize(); err != nil {
		return Policy{}, err
	}
	p.UpdatedBy, p.UpdatedAt = actor, now.UTC().Truncate(time.Second)
	unlock := s.lock()
	defer unlock()
	all, err := s.read()
	if err != nil {
		return Policy{}, err
	}
	all[benchmark] = p
	b, err := json.MarshalIndent(file{Benchmarks: all}, "", "  ")
	if err != nil {
		return Policy{}, err
	}
	if err := WriteAtomic(s.dir, s.path(), append(b, '\n'), 0o600); err != nil {
		return Policy{}, err
	}
	return p, nil
}

// Key is the org's redact key, made on first use: redact placeholders are
// an HMAC of the value under it, so they stay the same across imports and
// never reveal the value.
func (s *Store) Key() ([]byte, error) {
	unlock := s.lock()
	defer unlock()
	b, err := os.ReadFile(s.keyPath())
	if err == nil {
		k, derr := hex.DecodeString(strings.TrimSpace(string(b)))
		if derr != nil || len(k) != 32 {
			return nil, errors.New("the redact key .heimdall/pii.key is unreadable")
		}
		return k, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	if err := WriteAtomic(s.dir, s.keyPath(), []byte(hex.EncodeToString(k)+"\n"), 0o600); err != nil {
		return nil, err
	}
	return k, nil
}

// Redactor is the redactor of one benchmark's policy under the org key.
func (s *Store) Redactor(benchmark string) (pii.Redactor, Policy, error) {
	p, err := s.Get(benchmark)
	if err != nil {
		return pii.Redactor{}, Policy{}, err
	}
	r := pii.Redactor{Policy: p.PII}
	if p.PII.Effective() == pii.ModeRedact {
		if r.Key, err = s.Key(); err != nil {
			return pii.Redactor{}, Policy{}, err
		}
	}
	return r, p, nil
}

// WriteAtomic writes data to path through a temporary file in dir.
func WriteAtomic(dir, path string, data []byte, perm os.FileMode) error {
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
