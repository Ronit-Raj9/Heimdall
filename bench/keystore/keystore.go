// Package keystore stores provider keys and secrets per org, encrypted with a
// master key. Phase 0 skeleton: an in-memory stub; lane L04 builds the real
// database-backed store.
package keystore

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/db"
)

// Kind is "provider" or "secret".
type Kind string // "provider" | "secret"

// Kinds.
const (
	KindProvider Kind = "provider"
	KindSecret   Kind = "secret"
)

// MasterKeyEnv holds the base64 32-byte master key.
const MasterKeyEnv = "HEIMDALL_MASTER_KEY"

// Key is a stored key's metadata; the value is never part of it.
type Key struct {
	ID, OrgID                                          string
	Kind                                               Kind
	Provider, Label, BaseURL, Last4, Status, CreatedBy string
	Models                                             []string
	BudgetUSD, MonthSpentUSD                           float64
	CreatedAt, UpdatedAt                               time.Time
	LastTestedAt                                       *time.Time
}

// Put is a create or update request.
type Put struct {
	Provider, Label, BaseURL, Value string
	Models                          []string
	BudgetUSD                       float64
} // Value plaintext; "" on Update keeps it

// Store is the key store.
type Store interface {
	List(ctx context.Context, orgID string, k Kind) ([]Key, error)
	Get(ctx context.Context, orgID, id string) (*Key, error)
	Create(ctx context.Context, orgID string, k Kind, in Put, actor string) (*Key, error)
	Update(ctx context.Context, orgID, id string, in Put, actor string) (*Key, error)
	Delete(ctx context.Context, orgID, id, actor string) error
	Reveal(ctx context.Context, orgID, id string) (string, error) // server-internal only, never over HTTP to users
	SetStatus(ctx context.Context, orgID, id, status string, at time.Time) error
	AddSpend(ctx context.Context, orgID, id string, usd float64) error
}

// Errors.
var (
	ErrNotFound    = errors.New("keystore: key not found")
	ErrInvalid     = errors.New("keystore: invalid key")
	ErrNoMasterKey = errors.New("keystore: no master key (set " + MasterKeyEnv + ")")
)

// Open returns the store. Phase 0: d is ignored and the stub keeps keys in memory.
func Open(d *db.DB, master []byte) (Store, error) {
	if len(master) != 32 {
		return nil, ErrNoMasterKey
	}
	return &memStore{keys: map[string]*entry{}}, nil
}

// MasterKey reads HEIMDALL_MASTER_KEY (base64, 32 bytes); without it, local
// mode uses ~/.bifrost-bench/master.key, created on first use.
func MasterKey() ([]byte, error) {
	if v := strings.TrimSpace(os.Getenv(MasterKeyEnv)); v != "" {
		k, err := base64.StdEncoding.DecodeString(v)
		if err != nil || len(k) != 32 {
			return nil, fmt.Errorf("%w: %s must be base64 of 32 bytes", ErrNoMasterKey, MasterKeyEnv)
		}
		return k, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, ErrNoMasterKey
	}
	p := filepath.Join(home, ".bifrost-bench", "master.key")
	if b, err := os.ReadFile(p); err == nil {
		k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
		if err != nil || len(k) != 32 {
			return nil, fmt.Errorf("%w: %s is not base64 of 32 bytes", ErrNoMasterKey, p)
		}
		return k, nil
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(p, []byte(base64.StdEncoding.EncodeToString(k)+"\n"), 0o600); err != nil {
		return nil, err
	}
	return k, nil
}

type entry struct {
	key   Key
	value string
}

type memStore struct {
	mu   sync.Mutex
	keys map[string]*entry
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "key_" + hex.EncodeToString(b)
}

func last4(v string) string {
	if len(v) <= 4 {
		return v
	}
	return v[len(v)-4:]
}

func (m *memStore) List(ctx context.Context, orgID string, k Kind) ([]Key, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Key{}
	for _, e := range m.keys {
		if e.key.OrgID == orgID && (k == "" || e.key.Kind == k) {
			out = append(out, e.key)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *memStore) get(orgID, id string) (*entry, error) {
	e, ok := m.keys[id]
	if !ok || e.key.OrgID != orgID {
		return nil, ErrNotFound
	}
	return e, nil
}

func (m *memStore) Get(ctx context.Context, orgID, id string) (*Key, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, err := m.get(orgID, id)
	if err != nil {
		return nil, err
	}
	k := e.key
	return &k, nil
}

func (m *memStore) Create(ctx context.Context, orgID string, k Kind, in Put, actor string) (*Key, error) {
	if k != KindProvider && k != KindSecret || in.Value == "" {
		return nil, ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	e := &entry{value: in.Value, key: Key{ID: newID(), OrgID: orgID, Kind: k, Provider: in.Provider, Label: in.Label,
		BaseURL: in.BaseURL, Last4: last4(in.Value), Status: "untested", CreatedBy: actor, Models: in.Models,
		BudgetUSD: in.BudgetUSD, CreatedAt: now, UpdatedAt: now}}
	m.keys[e.key.ID] = e
	out := e.key
	return &out, nil
}

func (m *memStore) Update(ctx context.Context, orgID, id string, in Put, actor string) (*Key, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, err := m.get(orgID, id)
	if err != nil {
		return nil, err
	}
	e.key.Provider, e.key.Label, e.key.BaseURL = in.Provider, in.Label, in.BaseURL
	e.key.Models, e.key.BudgetUSD = in.Models, in.BudgetUSD
	if in.Value != "" {
		e.value, e.key.Last4 = in.Value, last4(in.Value)
	}
	e.key.UpdatedAt = time.Now().UTC()
	out := e.key
	return &out, nil
}

func (m *memStore) Delete(ctx context.Context, orgID, id, actor string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.get(orgID, id); err != nil {
		return err
	}
	delete(m.keys, id)
	return nil
}

func (m *memStore) Reveal(ctx context.Context, orgID, id string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, err := m.get(orgID, id)
	if err != nil {
		return "", err
	}
	return e.value, nil
}

func (m *memStore) SetStatus(ctx context.Context, orgID, id, status string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, err := m.get(orgID, id)
	if err != nil {
		return err
	}
	e.key.Status = status
	t := at
	e.key.LastTestedAt = &t
	return nil
}

func (m *memStore) AddSpend(ctx context.Context, orgID, id string, usd float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, err := m.get(orgID, id)
	if err != nil {
		return err
	}
	e.key.MonthSpentUSD += usd
	return nil
}
