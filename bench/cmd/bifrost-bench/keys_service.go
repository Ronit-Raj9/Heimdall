package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/keystore"
)

// Models and keys area (web parity L04): every org gets the key store. It is
// opened lazily on the first key request, so a server that never touches
// keys never opens the database or creates a local master key.
func init() {
	serviceHooks = append(serviceHooks, func(a *app, s *handlers.Services) {
		s.Keys = sharedKeyStore(a)
	})
}

var (
	keyStoresMu sync.Mutex
	keyStores   = map[string]*lazyKeyStore{} // by database config, one per process
)

func sharedKeyStore(a *app) keystore.Store {
	cfg := a.cfg.DatabaseConfig()
	id := fmt.Sprintf("%s|%s|%s", cfg.Driver, cfg.Path, cfg.DSNEnv)
	keyStoresMu.Lock()
	defer keyStoresMu.Unlock()
	if s, ok := keyStores[id]; ok {
		return s
	}
	s := &lazyKeyStore{cfg: cfg}
	keyStores[id] = s
	return s
}

// lazyKeyStore opens the database and the master key on first use. A failure
// is retried on the next call (after 10s), so fixing the config does not
// need a restart of every org.
type lazyKeyStore struct {
	cfg db.Config

	mu    sync.Mutex
	store keystore.Store
	err   error
	errAt time.Time
}

func (l *lazyKeyStore) get(ctx context.Context) (keystore.Store, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.store != nil {
		return l.store, nil
	}
	if l.err != nil && time.Since(l.errAt) < 10*time.Second {
		return nil, l.err
	}
	st, err := l.open(ctx)
	if err != nil {
		l.err, l.errAt = err, time.Now()
		return nil, err
	}
	l.store, l.err = st, nil
	return st, nil
}

func (l *lazyKeyStore) open(ctx context.Context) (keystore.Store, error) {
	master, err := keystore.MasterKey()
	if err != nil {
		return nil, err
	}
	d, err := db.Open(ctx, l.cfg)
	if err != nil {
		return nil, fmt.Errorf("%w: database: %v", keystore.ErrNoMasterKey, err)
	}
	if err := d.Migrate(ctx); err != nil {
		d.Close()
		return nil, fmt.Errorf("%w: migrate database: %v", keystore.ErrNoMasterKey, err)
	}
	return keystore.Open(d, master)
}

func (l *lazyKeyStore) List(ctx context.Context, orgID string, k keystore.Kind) ([]keystore.Key, error) {
	s, err := l.get(ctx)
	if err != nil {
		return nil, err
	}
	return s.List(ctx, orgID, k)
}

func (l *lazyKeyStore) Get(ctx context.Context, orgID, id string) (*keystore.Key, error) {
	s, err := l.get(ctx)
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, orgID, id)
}

func (l *lazyKeyStore) Create(ctx context.Context, orgID string, k keystore.Kind, in keystore.Put, actor string) (*keystore.Key, error) {
	s, err := l.get(ctx)
	if err != nil {
		return nil, err
	}
	return s.Create(ctx, orgID, k, in, actor)
}

func (l *lazyKeyStore) Update(ctx context.Context, orgID, id string, in keystore.Put, actor string) (*keystore.Key, error) {
	s, err := l.get(ctx)
	if err != nil {
		return nil, err
	}
	return s.Update(ctx, orgID, id, in, actor)
}

func (l *lazyKeyStore) Delete(ctx context.Context, orgID, id, actor string) error {
	s, err := l.get(ctx)
	if err != nil {
		return err
	}
	return s.Delete(ctx, orgID, id, actor)
}

func (l *lazyKeyStore) Reveal(ctx context.Context, orgID, id string) (string, error) {
	s, err := l.get(ctx)
	if err != nil {
		return "", err
	}
	return s.Reveal(ctx, orgID, id)
}

func (l *lazyKeyStore) SetStatus(ctx context.Context, orgID, id, status string, at time.Time) error {
	s, err := l.get(ctx)
	if err != nil {
		return err
	}
	return s.SetStatus(ctx, orgID, id, status, at)
}

func (l *lazyKeyStore) AddSpend(ctx context.Context, orgID, id string, usd float64) error {
	s, err := l.get(ctx)
	if err != nil {
		return err
	}
	return s.AddSpend(ctx, orgID, id, usd)
}
