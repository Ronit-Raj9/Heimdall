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
	"encoding/hex"
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
	ModelsAvailable                                    []string // model ids the provider listed at the last successful test
	BudgetUSD, MonthSpentUSD                           float64
	CreatedAt, UpdatedAt                               time.Time
	LastTestedAt                                       *time.Time
	// Price is what the org pays per million tokens on this key, used to
	// cost runs of a model the pinned price snapshot does not list (a
	// custom endpoint). nil: no price set.
	Price *Price
}

// Price is a key's price per million tokens. Cache is the price of a
// cached input token (read or write); 0 prices cached tokens at the input
// price (CacheRate), so a key without a cache price never undercounts.
type Price struct {
	InputPerMTok  float64 `json:"input_per_mtok"`
	OutputPerMTok float64 `json:"output_per_mtok"`
	CachePerMTok  float64 `json:"cache_per_mtok"`
}

// CacheRate is the price of a cached token: CachePerMTok, or the input
// price when no cache price is set.
func (p Price) CacheRate() float64 {
	if p.CachePerMTok > 0 {
		return p.CachePerMTok
	}
	return p.InputPerMTok
}

// MaxPricePerMTok bounds each price field (USD per million tokens).
const MaxPricePerMTok = 10_000

// Put is a create or update request.
type Put struct {
	Provider, Label, BaseURL, Value string
	Models                          []string
	BudgetUSD                       float64
	Price                           *Price // nil: no price (on Update too: the price is replaced, not kept)
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

// AvailableSetter is implemented by stores that remember the model ids a
// key's provider listed at its last successful test. It is separate from
// Store so existing Store implementations keep compiling.
type AvailableSetter interface {
	SetModelsAvailable(ctx context.Context, orgID, id string, models []string) error
}

// MaxModelsAvailable caps how many listed model ids a key keeps.
const MaxModelsAvailable = 200

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

// MasterKey reads HEIMDALL_MASTER_KEY (32 bytes, base64 or hex); without it, local
// mode uses ~/.bifrost-bench/master.key, created on first use.
func MasterKey() ([]byte, error) {
	if v := strings.TrimSpace(os.Getenv(MasterKeyEnv)); v != "" {
		if k, err := hex.DecodeString(v); err == nil && len(k) == 32 { // openssl rand -hex 32
			return k, nil
		}
		k, err := base64.StdEncoding.DecodeString(v)
		if err != nil || len(k) != 32 {
			return nil, fmt.Errorf("%w: %s must be 32 random bytes, base64 (openssl rand -base64 32) or hex (openssl rand -hex 32)", ErrNoMasterKey, MasterKeyEnv)
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
