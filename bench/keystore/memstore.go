package keystore

import (
	"context"
	"sort"
	"sync"
	"time"
)

// NewMemory returns a store that keeps keys in process memory only.
func NewMemory() Store { return &memStore{keys: map[string]*entry{}} }

type entry struct {
	key   Key
	value string
	month string // the calendar month MonthSpentUSD counts
}

type memStore struct {
	mu   sync.Mutex
	keys map[string]*entry
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
	in, err := normalize(k, in, true)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	e := &entry{value: in.Value, key: Key{ID: newID(), OrgID: orgID, Kind: k, Provider: in.Provider, Label: in.Label,
		BaseURL: in.BaseURL, Last4: last4(in.Value), Status: StatusUntested, CreatedBy: actor, Models: in.Models,
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
	if in, err = normalize(e.key.Kind, in, false); err != nil {
		return nil, err
	}
	e.key.Provider, e.key.Label, e.key.BaseURL = in.Provider, in.Label, in.BaseURL
	e.key.Models, e.key.BudgetUSD = in.Models, in.BudgetUSD
	if in.Value != "" {
		e.value, e.key.Last4 = in.Value, last4(in.Value)
		e.key.Status, e.key.LastTestedAt = StatusUntested, nil
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
	if usd < 0 {
		return ErrInvalid
	}
	if e.month != spendMonth(time.Now()) {
		e.month, e.key.MonthSpentUSD = spendMonth(time.Now()), 0
	}
	e.key.MonthSpentUSD += usd
	return nil
}

func (m *memStore) SetModelsAvailable(ctx context.Context, orgID, id string, models []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, err := m.get(orgID, id)
	if err != nil {
		return err
	}
	e.key.ModelsAvailable = normalizeAvailable(models)
	return nil
}
