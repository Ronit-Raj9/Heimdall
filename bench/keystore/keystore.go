// Package keystore stores provider keys and secrets per org. Values are
// encrypted with AES-256-GCM under a master key (HEIMDALL_MASTER_KEY) and the
// org and key id as associated data, so a ciphertext copied to another row or
// org does not decrypt. Only metadata (provider, label, last 4 characters)
// ever leaves the store; Reveal is for server-internal callers.
package keystore

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// Open returns the store: the database-backed one (AES-256-GCM values in
// the model_keys table) when d is set, an in-memory one when d is nil (tests
// and servers without a database).
func Open(d *db.DB, master []byte) (Store, error) {
	if len(master) != 32 {
		return nil, ErrNoMasterKey
	}
	if d == nil {
		return NewMemory(), nil
	}
	return newDBStore(d, master)
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
