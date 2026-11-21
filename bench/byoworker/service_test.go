package byoworker

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/db/dbtest"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/schemas"
)

// fakeDriver is a platform driver in memory.
type fakeDriver struct {
	mu       sync.Mutex
	health   Health
	state    map[string]string
	boots    []Boot
	secrets  []string
	stopErr  error
	startErr error
}

func (f *fakeDriver) Platform() string { return PlatformRunloop }
func (f *fakeDriver) Validate(in map[string]string, secret string) (map[string]string, error) {
	if secret == "" {
		return nil, ErrInvalid
	}
	return in, nil
}
func (f *fakeDriver) Check(context.Context, Target) Health { return f.health }
func (f *fakeDriver) Start(_ context.Context, t Target, b Boot) (Instance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return Instance{}, f.startErr
	}
	f.boots = append(f.boots, b)
	f.secrets = append(f.secrets, t.Secret)
	id := "box-" + b.Name
	f.state[id] = InstanceStarting
	return Instance{ID: id, State: InstanceStarting}, nil
}
func (f *fakeDriver) Status(_ context.Context, _ Target, id string) (Instance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.state[id]
	if !ok {
		st = InstanceStopped
	}
	return Instance{ID: id, State: st}, nil
}
func (f *fakeDriver) Stop(_ context.Context, _ Target, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopErr != nil {
		return f.stopErr
	}
	delete(f.state, id)
	return nil
}

type fakeMinter struct {
	mu      sync.Mutex
	n       int
	revoked []string
}

func (m *fakeMinter) CreateToken(_ context.Context, org, by string, req schemas.RunnerTokenCreate) (*schemas.RunnerTokenCreated, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.n++
	id := "rtk_" + req.Pool
	return &schemas.RunnerTokenCreated{Token: "hmr_abcdefgh_" + req.Pool, Info: schemas.RunnerTokenView{ID: id, OrgID: org, Pool: req.Pool}}, nil
}

func (m *fakeMinter) RevokeToken(_ context.Context, org, id string) (*schemas.RunnerTokenView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.revoked = append(m.revoked, id)
	return &schemas.RunnerTokenView{ID: id}, nil
}

type fakeRunners struct{ list []schemas.RunnerView }

func (f *fakeRunners) ListRunners(context.Context, string) ([]schemas.RunnerView, error) {
	return f.list, nil
}

func newTestService(t *testing.T, d *db.DB) (*Service, *fakeDriver, *fakeMinter, *fakeRunners, keystore.Store) {
	t.Helper()
	ctx := context.Background()
	for _, org := range []string{"org_a", "org_b"} {
		if _, err := d.Exec(ctx, `INSERT INTO orgs (id, slug, name, public_read, created_at) VALUES (?, ?, ?, 0, ?)`,
			org, strings.ReplaceAll(org, "_", "-"), org, db.Now()); err != nil {
			t.Fatal(err)
		}
	}
	drv := &fakeDriver{health: Health{OK: true, Detail: "ok", Capacity: "4 CPU"}, state: map[string]string{}}
	m := &fakeMinter{}
	rs := &fakeRunners{}
	ks := keystore.NewMemory()
	s := New(d, Options{Drivers: NewRegistry(drv), Keys: ks, Minter: m, Runners: rs, PublicURL: "https://heimdall.example.com/",
		Now: func() time.Time { return time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC) }})
	return s, drv, m, rs, ks
}

func TestServiceLifecycle(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		s, drv, m, rs, ks := newTestService(t, d)
		ctx := context.Background()
		created, err := s.Create(ctx, "org_a", "usr_1", schemas.WorkerConnectionCreate{Name: "rl-1", Platform: PlatformRunloop, Secret: "rl_key_1", Settings: map[string]string{"size": "LARGE"}})
		if err != nil {
			t.Fatal(err)
		}
		c := created.Connection
		if created.Install != "" || c.Status != schemas.WorkerConnWaiting || c.Pool != "rl-1" || c.Settings["size"] != "LARGE" {
			t.Fatalf("created: %+v", created)
		}
		// Secrets are in the keystore under the system actor only.
		keys, _ := ks.List(ctx, "org_a", keystore.KindSecret)
		if len(keys) != 2 {
			t.Fatalf("keystore rows: %d", len(keys))
		}
		for _, k := range keys {
			if !strings.HasPrefix(k.CreatedBy, "heimdall:") {
				t.Fatalf("visible secret %+v", k)
			}
		}
		if _, err := s.Create(ctx, "org_a", "usr_1", schemas.WorkerConnectionCreate{Name: "rl-1", Platform: PlatformRunloop, Secret: "x"}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("duplicate: %v", err)
		}
		for _, bad := range []schemas.WorkerConnectionCreate{{Name: "default", Platform: PlatformVM}, {Name: "shared", Platform: PlatformVM},
			{Name: "a b", Platform: PlatformVM}, {Name: "x", Platform: "nomad"}, {Name: "x", Platform: PlatformRunloop}, {Name: "x", Platform: PlatformVM, Secret: "s"}} {
			if _, err := s.Create(ctx, "org_a", "usr_1", bad); !errors.Is(err, ErrInvalid) {
				t.Fatalf("create %+v: %v", bad, err)
			}
		}
		// Another org sees nothing.
		if l, _ := s.List(ctx, "org_b"); len(l.Connections) != 0 {
			t.Fatalf("org_b sees %v", l.Connections)
		}
		if _, err := s.Start(ctx, "org_b", c.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("cross-org start: %v", err)
		}
		v, err := s.Check(ctx, "org_a", c.ID)
		if err != nil || v.Health == nil || !v.Health.OK || v.Health.Capacity != "4 CPU" {
			t.Fatalf("check: %+v %v", v, err)
		}
		v, err = s.Start(ctx, "org_a", c.ID)
		if err != nil || v.Status != schemas.WorkerConnStarting || v.Instance.ID != "box-rl-1" {
			t.Fatalf("start: %+v %v", v, err)
		}
		b := drv.boots[0]
		if b.RunnerToken != "hmr_abcdefgh_rl-1" || b.Pool != "rl-1" || b.ServerURL != "https://heimdall.example.com" || drv.secrets[0] != "rl_key_1" {
			t.Fatalf("boot: %+v %v", b, drv.secrets)
		}
		if _, err := s.Start(ctx, "org_a", c.ID); !errors.Is(err, ErrInvalid) {
			t.Fatalf("second start: %v", err)
		}
		// The runner registers with the connection's token: ready.
		rs.list = []schemas.RunnerView{{Name: "rl-1", TokenID: "rtk_rl-1", State: schemas.RunnerIdle, Slots: 2, LastSeen: time.Now()}}
		v, _ = s.Get(ctx, "org_a", c.ID)
		if v.Status != schemas.WorkerConnReady || v.Runner == nil || !strings.Contains(v.StatusDetail, "0 of 2") {
			t.Fatalf("ready: %+v", v)
		}
		rs.list[0].State = schemas.RunnerLost
		if v, _ = s.Get(ctx, "org_a", c.ID); v.Status != schemas.WorkerConnOffline {
			t.Fatalf("offline: %+v", v)
		}
		// Default worker and evaluation routing.
		if _, err := s.SetDefaultWorker(ctx, "org_a", "usr_1", "nope"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("unknown default: %v", err)
		}
		if p, err := s.SetDefaultWorker(ctx, "org_a", "usr_1", "rl-1"); err != nil || p != "rl-1" {
			t.Fatal(p, err)
		}
		if p, _ := s.EvaluationPool(ctx, "org_a", ""); p != "rl-1" {
			t.Fatalf("default pool %q", p)
		}
		if p, _ := s.EvaluationPool(ctx, "org_a", "shared"); p != "" {
			t.Fatalf("shared pool %q", p)
		}
		if _, err := s.EvaluationPool(ctx, "org_a", "ghost"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("ghost: %v", err)
		}
		if p, _ := s.EvaluationPool(ctx, "org_b", ""); p != "" {
			t.Fatalf("org_b default %q", p)
		}
		l, _ := s.List(ctx, "org_a")
		if l.DefaultWorker != "rl-1" || !l.Connections[0].Default || len(l.Platforms) != 2 {
			t.Fatalf("list: %+v", l)
		}
		// Stop, then delete: token revoked, secrets gone, default cleared.
		if v, err = s.Stop(ctx, "org_a", c.ID); err != nil || v.Status != schemas.WorkerConnStopped {
			t.Fatalf("stop: %+v %v", v, err)
		}
		if _, err := s.Delete(ctx, "org_a", c.ID); err != nil {
			t.Fatal(err)
		}
		if len(m.revoked) != 1 || m.revoked[0] != "rtk_rl-1" {
			t.Fatalf("revoked %v", m.revoked)
		}
		if keys, _ := ks.List(ctx, "org_a", keystore.KindSecret); len(keys) != 0 {
			t.Fatalf("keys left: %v", keys)
		}
		if p, _ := s.DefaultWorker(ctx, "org_a"); p != "" {
			t.Fatalf("default after delete %q", p)
		}
	})
}

func TestServiceVMInstallAndStuckDelete(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		s, drv, m, _, _ := newTestService(t, d)
		ctx := context.Background()
		created, err := s.Create(ctx, "org_a", "usr_1", schemas.WorkerConnectionCreate{Name: "lab-box", Platform: PlatformVM})
		if err != nil {
			t.Fatal(err)
		}
		want := "curl -fsSL https://heimdall.example.com/api/bench/runners/install.sh | sudo HEIMDALL_RUNNER_TOKEN=hmr_abcdefgh_lab-box HEIMDALL_POOL=lab-box HEIMDALL_RUNNER_NAME=lab-box HEIMDALL_LABELS=platform=vm sh"
		if created.Install != want {
			t.Fatalf("install:\n%s\nwant\n%s", created.Install, want)
		}
		if again, err := s.InstallCommand(ctx, "org_a", created.Connection.ID); err != nil || again != want {
			t.Fatalf("again: %q %v", again, err)
		}
		if _, err := s.Start(ctx, "org_a", created.Connection.ID); !errors.Is(err, ErrInvalid) {
			t.Fatalf("start a VM: %v", err)
		}
		// A box the platform cannot stop keeps its connection.
		rl, err := s.Create(ctx, "org_a", "usr_1", schemas.WorkerConnectionCreate{Name: "rl-2", Platform: PlatformRunloop, Secret: "k"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Start(ctx, "org_a", rl.Connection.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.InstallCommand(ctx, "org_a", rl.Connection.ID); !errors.Is(err, ErrInvalid) {
			t.Fatalf("install command of a platform worker: %v", err)
		}
		drv.stopErr = errors.New("platform down")
		if _, err := s.Delete(ctx, "org_a", rl.Connection.ID); err == nil || !strings.Contains(err.Error(), "not removed") {
			t.Fatalf("stuck delete: %v", err)
		}
		if _, err := s.Get(ctx, "org_a", rl.Connection.ID); err != nil {
			t.Fatalf("connection lost: %v", err)
		}
		if len(m.revoked) != 0 {
			t.Fatalf("revoked early: %v", m.revoked)
		}
		// A failed health check is the status.
		drv.health = Health{OK: false, Detail: "The platform refused the token"}
		if v, _ := s.Check(ctx, "org_a", rl.Connection.ID); v.Status != schemas.WorkerConnError {
			t.Fatalf("error status: %+v", v)
		}
	})
}
