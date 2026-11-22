package openmodel

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/db"
)

//go:embed migrations/*.sql
var migrations embed.FS

func init() { db.Register("openmodel", migrations) }

// ErrNotFound is an unknown deployment.
var ErrNotFound = errors.New("open model deployment not found")

// Store keeps deployments. Every read is scoped to one org except Active,
// which the reaper uses across orgs.
type Store interface {
	Put(ctx context.Context, d *Deployment) error
	Get(ctx context.Context, org, id string) (*Deployment, error)
	List(ctx context.Context, org string) ([]Deployment, error)
	// Active lists every deployment, of any org, that is not stopped or
	// failed-and-cleaned.
	Active(ctx context.Context) ([]Deployment, error)
}

// NewMemoryStore returns a Store in memory (tests, servers without a
// database).
func NewMemoryStore() Store { return &memStore{m: map[string]Deployment{}} }

type memStore struct {
	mu sync.Mutex
	m  map[string]Deployment
}

func (s *memStore) Put(_ context.Context, d *Deployment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[d.ID] = *d
	return nil
}

func (s *memStore) Get(_ context.Context, org, id string) (*Deployment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.m[id]
	if !ok || d.OrgID != org {
		return nil, ErrNotFound
	}
	return &d, nil
}

func (s *memStore) List(_ context.Context, org string) ([]Deployment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Deployment{}
	for _, d := range s.m {
		if d.OrgID == org {
			out = append(out, d)
		}
	}
	sortNewest(out)
	return out, nil
}

func (s *memStore) Active(_ context.Context) ([]Deployment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Deployment{}
	for _, d := range s.m {
		if d.active() {
			out = append(out, d)
		}
	}
	sortNewest(out)
	return out, nil
}

func sortNewest(ds []Deployment) {
	sort.SliceStable(ds, func(i, j int) bool {
		if !ds[i].CreatedAt.Equal(ds[j].CreatedAt) {
			return ds[i].CreatedAt.After(ds[j].CreatedAt)
		}
		return ds[i].ID < ds[j].ID
	})
}

// NewDBStore returns the database Store (table open_model_deployments).
func NewDBStore(d *db.DB) Store { return &dbStore{d: d} }

type dbStore struct{ d *db.DB }

func (s *dbStore) Put(ctx context.Context, dep *Deployment) error {
	raw, err := json.Marshal(dep)
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	res, err := s.d.Exec(ctx, `UPDATE open_model_deployments SET state = ?, data = ?, updated_at = ? WHERE id = ? AND org_id = ?`,
		dep.State, string(raw), now, dep.ID, dep.OrgID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	_, err = s.d.Exec(ctx, `INSERT INTO open_model_deployments (id, org_id, state, data, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		dep.ID, dep.OrgID, dep.State, string(raw), dep.CreatedAt.UnixMilli(), now)
	return err
}

func scanDeployment(row interface{ Scan(...any) error }) (*Deployment, error) {
	var raw string
	if err := row.Scan(&raw); err != nil {
		return nil, err
	}
	var d Deployment
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *dbStore) Get(ctx context.Context, org, id string) (*Deployment, error) {
	d, err := scanDeployment(s.d.QueryRow(ctx, `SELECT data FROM open_model_deployments WHERE org_id = ? AND id = ?`, org, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

func (s *dbStore) query(ctx context.Context, q string, args ...any) ([]Deployment, error) {
	rows, err := s.d.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Deployment{}
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

func (s *dbStore) List(ctx context.Context, org string) ([]Deployment, error) {
	return s.query(ctx, `SELECT data FROM open_model_deployments WHERE org_id = ? ORDER BY created_at DESC, id LIMIT 200`, org)
}

func (s *dbStore) Active(ctx context.Context) ([]Deployment, error) {
	return s.query(ctx, `SELECT data FROM open_model_deployments WHERE state IN (?, ?, ?) ORDER BY created_at DESC, id`,
		StateStarting, StateReady, StateStopping)
}
