package byoworker

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/schemas"
)

//go:embed migrations/*.sql
var migrations embed.FS

func init() { db.Register("byoworker", migrations) }

// Keystore rows of a connection. Actor starts with "heimdall:", so the
// secrets page never lists them (handlers.systemSecret).
const (
	Actor          = "heimdall:worker"
	secretProvider = "worker-credential"
	tokenProvider  = "worker-runner-token"
)

// Minter mints and revokes runner tokens (runners.Service).
type Minter interface {
	CreateToken(ctx context.Context, orgID, createdBy string, req schemas.RunnerTokenCreate) (*schemas.RunnerTokenCreated, error)
	RevokeToken(ctx context.Context, orgID, id string) (*schemas.RunnerTokenView, error)
}

// RunnerLister lists an org's registered runners with their live state
// (runners.Service).
type RunnerLister interface {
	ListRunners(ctx context.Context, orgID string) ([]schemas.RunnerView, error)
}

// Options configures a Service.
type Options struct {
	Drivers Registry
	Keys    keystore.Store
	Minter  Minter
	Runners RunnerLister
	// PublicURL is the hosted server's address the runners call.
	PublicURL string
	// RunnerImage is the image of a runner for the platforms that run an
	// image and whose connection names none (HEIMDALL_RUNNER_IMAGE).
	RunnerImage string
	// Slots is how many runs one started worker runs at once; default 2.
	Slots int
	Now   func() time.Time
}

// Service is the worker connections area over the database.
type Service struct {
	db   *db.DB
	opts Options
	mu   sync.Mutex // one create or delete per process at a time: names and tokens stay paired
}

// New returns the service; d must be migrated.
func New(d *db.DB, opts Options) *Service {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Slots <= 0 {
		opts.Slots = 2
	}
	if opts.Drivers == nil {
		opts.Drivers = Registry{}
	}
	opts.PublicURL = strings.TrimRight(opts.PublicURL, "/")
	return &Service{db: d, opts: opts}
}

func (s *Service) now() time.Time { return s.opts.Now().UTC() }

// row is one worker_connections row.
type row struct {
	ID, OrgID, Name, Platform                 string
	Settings                                  map[string]string
	SecretKeyID, RunnerTokenID, RunnerKeyID   string
	InstanceID, InstanceState, InstanceDetail string
	Health                                    *schemas.WorkerHealth
	CreatedBy                                 string
	CreatedAt, UpdatedAt                      time.Time
}

const cols = `id, org_id, name, platform, settings, secret_key_id, runner_token_id, runner_key_id,
	instance_id, instance_state, instance_detail, health, created_by, created_at, updated_at`

func scanRow(r interface{ Scan(...any) error }) (*row, error) {
	var w row
	var settings, health string
	var created, updated int64
	if err := r.Scan(&w.ID, &w.OrgID, &w.Name, &w.Platform, &settings, &w.SecretKeyID, &w.RunnerTokenID, &w.RunnerKeyID,
		&w.InstanceID, &w.InstanceState, &w.InstanceDetail, &health, &w.CreatedBy, &created, &updated); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(settings), &w.Settings); err != nil || w.Settings == nil {
		w.Settings = map[string]string{}
	}
	if health != "" {
		var h schemas.WorkerHealth
		if json.Unmarshal([]byte(health), &h) == nil {
			w.Health = &h
		}
	}
	w.CreatedAt, w.UpdatedAt = db.Time(created), db.Time(updated)
	return &w, nil
}

func (s *Service) get(ctx context.Context, orgID, id string) (*row, error) {
	w, err := scanRow(s.db.QueryRow(ctx, `SELECT `+cols+` FROM worker_connections WHERE org_id = ? AND id = ?`, orgID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("worker %s: %w", id, ErrNotFound)
	}
	return w, err
}

// Platforms lists what an org can connect: every registered driver and a VM.
func (s *Service) Platforms() []schemas.WorkerPlatform {
	out := []schemas.WorkerPlatform{}
	for _, p := range s.opts.Drivers.Platforms() {
		out = append(out, schemas.WorkerPlatform{ID: p, Title: PlatformTitle(p), Starts: true})
	}
	out = append(out, schemas.WorkerPlatform{ID: PlatformVM, Title: PlatformTitle(PlatformVM)})
	return out
}

// reserved pool names: the shared worker's.
func reservedName(n string) bool {
	return n == schemas.DefaultRunnerPool || n == schemas.SharedRunnerPool
}

// Create connects a worker: checks its settings and credential, keeps the
// credential and a fresh runner token of its own pool in the keystore, and
// records the connection. A VM's answer carries its install command.
func (s *Service) Create(ctx context.Context, orgID, actor string, in schemas.WorkerConnectionCreate) (*schemas.WorkerConnectionCreated, error) {
	if s.opts.Keys == nil || s.opts.Minter == nil {
		return nil, errors.New("bring your own worker needs the keystore (HEIMDALL_MASTER_KEY) and the runners area")
	}
	name := strings.TrimSpace(in.Name)
	if !schemas.ValidPoolName(name) || reservedName(name) {
		return nil, fmt.Errorf("%w: name must be 1 to 64 letters, digits, '.', '_' or '-', and not %q or %q", ErrInvalid, schemas.DefaultRunnerPool, schemas.SharedRunnerPool)
	}
	platform := strings.TrimSpace(in.Platform)
	settings := map[string]string{}
	secret := strings.TrimSpace(in.Secret)
	if platform == PlatformVM {
		if len(in.Settings) > 0 || secret != "" {
			return nil, fmt.Errorf("%w: a VM takes no settings or credential", ErrInvalid)
		}
	} else {
		d, ok := s.opts.Drivers[platform]
		if !ok {
			return nil, fmt.Errorf("%w: platform %q is not one of %s", ErrInvalid, platform, strings.Join(append(s.opts.Drivers.Platforms(), PlatformVM), ", "))
		}
		var err error
		if settings, err = d.Validate(in.Settings, secret); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM worker_connections WHERE org_id = ? AND name = ?`, orgID, name).Scan(&n); err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, fmt.Errorf("%w: a worker named %s exists already", ErrInvalid, name)
	}
	// Undo what was made when a later step fails.
	var undo []func()
	fail := func(err error) (*schemas.WorkerConnectionCreated, error) {
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
		return nil, err
	}
	bg := context.WithoutCancel(ctx)
	var secretKey string
	if secret != "" {
		k, err := s.opts.Keys.Create(ctx, orgID, keystore.KindSecret, keystore.Put{Provider: secretProvider, Label: "worker:" + name, Value: secret}, Actor)
		if err != nil {
			return fail(fmt.Errorf("keep the credential: %w", err))
		}
		secretKey = k.ID
		undo = append(undo, func() { _ = s.opts.Keys.Delete(bg, orgID, k.ID, Actor) })
	}
	tok, err := s.opts.Minter.CreateToken(ctx, orgID, Actor, schemas.RunnerTokenCreate{Pool: name, Name: name})
	if err != nil {
		return fail(fmt.Errorf("mint the runner token: %w", err))
	}
	undo = append(undo, func() { _, _ = s.opts.Minter.RevokeToken(bg, orgID, tok.Info.ID) })
	tk, err := s.opts.Keys.Create(ctx, orgID, keystore.KindSecret, keystore.Put{Provider: tokenProvider, Label: "worker:" + name, Value: tok.Token}, Actor)
	if err != nil {
		return fail(fmt.Errorf("keep the runner token: %w", err))
	}
	undo = append(undo, func() { _ = s.opts.Keys.Delete(bg, orgID, tk.ID, Actor) })
	sb, err := json.Marshal(settings)
	if err != nil {
		return fail(err)
	}
	id := db.NewID("wkc")
	now := db.Millis(s.now())
	if _, err := s.db.Exec(ctx, `INSERT INTO worker_connections (id, org_id, name, platform, settings, secret_key_id, runner_token_id, runner_key_id, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, orgID, name, platform, string(sb), secretKey, tok.Info.ID, tk.ID, actor, now, now); err != nil {
		if db.IsUnique(err) {
			return fail(fmt.Errorf("%w: a worker named %s exists already", ErrInvalid, name))
		}
		return fail(fmt.Errorf("record the worker: %w", err))
	}
	view, err := s.Get(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	out := &schemas.WorkerConnectionCreated{Connection: *view}
	if platform == PlatformVM {
		out.Install = s.installLine(name, tok.Token)
	}
	return out, nil
}

// installLine is the one-line install of a VM worker. The token is in the
// environment of the root shell, never written to the box's disk by the
// line itself (install.sh keeps it in /etc/heimdall-runner/token, 0600).
func (s *Service) installLine(name, token string) string {
	server := s.opts.PublicURL
	if server == "" {
		server = "https://heimdall.example.com"
	}
	return "curl -fsSL " + server + InstallPath + " | sudo " + EnvRunnerToken + "=" + token +
		" " + EnvPool + "=" + name + " " + EnvRunnerName + "=" + name + " " + EnvLabels + "=" + PlatformLabel(PlatformVM) + " sh"
}

// InstallCommand shows a VM worker's install command again.
func (s *Service) InstallCommand(ctx context.Context, orgID, id string) (string, error) {
	w, err := s.get(ctx, orgID, id)
	if err != nil {
		return "", err
	}
	if w.Platform != PlatformVM {
		return "", fmt.Errorf("%w: %s is started by Heimdall on %s; it has no install command", ErrInvalid, w.Name, PlatformTitle(w.Platform))
	}
	tok, err := s.opts.Keys.Reveal(ctx, orgID, w.RunnerKeyID)
	if err != nil {
		return "", fmt.Errorf("read the runner token: %w", err)
	}
	return s.installLine(w.Name, tok), nil
}

func (s *Service) target(ctx context.Context, w *row) (Driver, Target, error) {
	d, ok := s.opts.Drivers[w.Platform]
	if !ok {
		return nil, Target{}, fmt.Errorf("%w: %s is a %s worker, which Heimdall does not start", ErrInvalid, w.Name, PlatformTitle(w.Platform))
	}
	t := Target{Settings: w.Settings}
	if w.SecretKeyID != "" {
		v, err := s.opts.Keys.Reveal(ctx, w.OrgID, w.SecretKeyID)
		if err != nil {
			return nil, Target{}, fmt.Errorf("read the platform credential: %w", err)
		}
		t.Secret = v
	}
	return d, t, nil
}

func (s *Service) saveInstance(ctx context.Context, w *row, in *Instance) error {
	id, state, detail := "", "", ""
	if in != nil {
		id, state, detail = in.ID, in.State, in.Detail
	}
	if len(detail) > 500 {
		detail = detail[:500]
	}
	_, err := s.db.Exec(ctx, `UPDATE worker_connections SET instance_id = ?, instance_state = ?, instance_detail = ?, updated_at = ? WHERE org_id = ? AND id = ?`,
		id, state, detail, db.Millis(s.now()), w.OrgID, w.ID)
	return err
}

// Check reaches the connection's platform, refreshes the started box's
// state, and records both.
func (s *Service) Check(ctx context.Context, orgID, id string) (*schemas.WorkerConnection, error) {
	w, err := s.get(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if w.Platform == PlatformVM {
		return s.Get(ctx, orgID, id) // its health is its runner's
	}
	d, t, err := s.target(ctx, w)
	if err != nil {
		return nil, err
	}
	h := d.Check(ctx, t)
	if len(h.Detail) > 500 {
		h.Detail = h.Detail[:500]
	}
	hb, _ := json.Marshal(h)
	if _, err := s.db.Exec(ctx, `UPDATE worker_connections SET health = ?, updated_at = ? WHERE org_id = ? AND id = ?`,
		string(hb), db.Millis(s.now()), orgID, id); err != nil {
		return nil, err
	}
	if w.InstanceID != "" && h.OK {
		if in, err := d.Status(ctx, t, w.InstanceID); err == nil {
			if err := s.saveInstance(ctx, w, &in); err != nil {
				return nil, err
			}
		}
	}
	return s.Get(ctx, orgID, id)
}

// Start starts the connection's worker on its platform. A worker whose box
// is still starting or running is not started twice.
func (s *Service) Start(ctx context.Context, orgID, id string) (*schemas.WorkerConnection, error) {
	w, err := s.get(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	d, t, err := s.target(ctx, w)
	if err != nil {
		return nil, err
	}
	if w.InstanceID != "" {
		cur, err := d.Status(ctx, t, w.InstanceID)
		if err == nil && (cur.State == InstanceStarting || cur.State == InstanceRunning) {
			_ = s.saveInstance(ctx, w, &cur)
			return nil, fmt.Errorf("%w: %s is %s already; stop it first", ErrInvalid, w.Name, cur.State)
		}
	}
	tok, err := s.opts.Keys.Reveal(ctx, orgID, w.RunnerKeyID)
	if err != nil {
		return nil, fmt.Errorf("read the runner token: %w", err)
	}
	in, err := d.Start(ctx, t, Boot{Name: w.Name, Pool: w.Name, Slots: s.opts.Slots, ServerURL: s.opts.PublicURL, RunnerToken: tok, Image: s.opts.RunnerImage})
	if err != nil {
		return nil, err
	}
	if err := s.saveInstance(ctx, w, &in); err != nil {
		return nil, err
	}
	return s.Get(ctx, orgID, id)
}

// Stop stops and removes the connection's box. The connection stays, so it
// can be started again.
func (s *Service) Stop(ctx context.Context, orgID, id string) (*schemas.WorkerConnection, error) {
	w, err := s.get(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if w.InstanceID == "" {
		return s.Get(ctx, orgID, id)
	}
	d, t, err := s.target(ctx, w)
	if err != nil {
		return nil, err
	}
	if err := d.Stop(ctx, t, w.InstanceID); err != nil {
		return nil, err
	}
	if err := s.saveInstance(ctx, w, &Instance{ID: w.InstanceID, State: InstanceStopped, Detail: "stopped from Heimdall"}); err != nil {
		return nil, err
	}
	return s.Get(ctx, orgID, id)
}

// Delete disconnects a worker: stops its box, revokes its runner token (its
// runner is refused on its next call), deletes its keystore rows and the
// connection. A box the platform cannot stop is reported and nothing is
// deleted, so the org never loses track of compute it pays for.
func (s *Service) Delete(ctx context.Context, orgID, id string) (*schemas.WorkerConnection, error) {
	view, err := s.Get(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	w, err := s.get(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if w.InstanceID != "" && w.InstanceState != InstanceStopped {
		if d, t, err := s.target(ctx, w); err == nil {
			if err := d.Stop(ctx, t, w.InstanceID); err != nil {
				return nil, fmt.Errorf("%w; the worker was not removed (stop it on %s, then remove it again)", err, PlatformTitle(w.Platform))
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if w.RunnerTokenID != "" {
		if _, err := s.opts.Minter.RevokeToken(ctx, orgID, w.RunnerTokenID); err != nil {
			return nil, fmt.Errorf("revoke the runner token: %w", err)
		}
	}
	for _, k := range []string{w.SecretKeyID, w.RunnerKeyID} {
		if k != "" {
			if err := s.opts.Keys.Delete(ctx, orgID, k, Actor); err != nil && !errors.Is(err, keystore.ErrNotFound) {
				return nil, fmt.Errorf("delete the stored secret: %w", err)
			}
		}
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM worker_connections WHERE org_id = ? AND id = ?`, orgID, id); err != nil {
		return nil, err
	}
	if _, err := s.db.Exec(ctx, `UPDATE worker_prefs SET default_pool = '' WHERE org_id = ? AND default_pool = ?`, orgID, w.Name); err != nil {
		return nil, err
	}
	return view, nil
}

// Get returns one connection with its runner and state.
func (s *Service) Get(ctx context.Context, orgID, id string) (*schemas.WorkerConnection, error) {
	w, err := s.get(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	runners, def, err := s.context(ctx, orgID)
	if err != nil {
		return nil, err
	}
	v := s.view(w, runners, def)
	return &v, nil
}

// List lists the org's connections by name, with the default worker and
// the platforms it can connect.
func (s *Service) List(ctx context.Context, orgID string) (*schemas.WorkerConnectionsList, error) {
	rows, err := s.db.Query(ctx, `SELECT `+cols+` FROM worker_connections WHERE org_id = ? ORDER BY name`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list workers: %w", err)
	}
	var ws []*row
	for rows.Next() {
		w, err := scanRow(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		ws = append(ws, w)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	runners, def, err := s.context(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := &schemas.WorkerConnectionsList{Connections: []schemas.WorkerConnection{}, DefaultWorker: def, Platforms: s.Platforms()}
	for _, w := range ws {
		out.Connections = append(out.Connections, s.view(w, runners, def))
	}
	sort.SliceStable(out.Connections, func(i, j int) bool { return out.Connections[i].Name < out.Connections[j].Name })
	return out, nil
}

// context reads the org's runners (by token id) and default worker.
func (s *Service) context(ctx context.Context, orgID string) (map[string]schemas.RunnerView, string, error) {
	byToken := map[string]schemas.RunnerView{}
	if s.opts.Runners != nil {
		list, err := s.opts.Runners.ListRunners(ctx, orgID)
		if err != nil {
			return nil, "", err
		}
		for _, r := range list {
			if cur, ok := byToken[r.TokenID]; !ok || r.LastSeen.After(cur.LastSeen) {
				byToken[r.TokenID] = r
			}
		}
	}
	def, err := s.DefaultWorker(ctx, orgID)
	return byToken, def, err
}

// view builds the API view: the runner registered with the connection's
// token decides the state when there is one; else the started box; else
// the last check.
func (s *Service) view(w *row, runners map[string]schemas.RunnerView, def string) schemas.WorkerConnection {
	v := schemas.WorkerConnection{ID: w.ID, Name: w.Name, Pool: w.Name, Platform: w.Platform, PlatformTitle: PlatformTitle(w.Platform),
		Settings: w.Settings, Health: w.Health, Default: def != "" && def == w.Name, CreatedBy: w.CreatedBy, CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt}
	if w.InstanceID != "" {
		v.Instance = &schemas.WorkerInstance{ID: w.InstanceID, State: w.InstanceState, Detail: w.InstanceDetail}
	}
	if r, ok := runners[w.RunnerTokenID]; ok && w.RunnerTokenID != "" {
		rc := r
		v.Runner = &rc
	}
	switch {
	case w.Health != nil && !w.Health.OK:
		v.Status, v.StatusDetail = schemas.WorkerConnError, w.Health.Detail
	case v.Instance != nil && v.Instance.State == InstanceFailed:
		v.Status, v.StatusDetail = schemas.WorkerConnError, "The worker failed on "+PlatformTitle(w.Platform)+": "+v.Instance.Detail
	case v.Instance != nil && v.Instance.State == InstanceStopped:
		v.Status, v.StatusDetail = schemas.WorkerConnStopped, "Stopped. Start it to run evaluations here."
	case v.Runner != nil && v.Runner.State != schemas.RunnerLost:
		v.Status = schemas.WorkerConnReady
		v.StatusDetail = fmt.Sprintf("Connected, %d of %d slots in use.", v.Runner.RunningSlots, v.Runner.Slots)
	case v.Runner != nil:
		v.Status, v.StatusDetail = schemas.WorkerConnOffline, "The worker stopped calling in."
	case v.Instance != nil:
		v.Status, v.StatusDetail = schemas.WorkerConnStarting, "Started on "+PlatformTitle(w.Platform)+"; the worker connects once it has installed."
	case w.Platform == PlatformVM:
		v.Status, v.StatusDetail = schemas.WorkerConnWaiting, "Waiting for the install command to run on your VM."
	default:
		v.Status, v.StatusDetail = schemas.WorkerConnWaiting, "Not started yet."
	}
	return v
}

// DefaultWorker is the pool the org's evaluations run on when they name
// none; "" is the shared worker.
func (s *Service) DefaultWorker(ctx context.Context, orgID string) (string, error) {
	var pool string
	err := s.db.QueryRow(ctx, `SELECT default_pool FROM worker_prefs WHERE org_id = ?`, orgID).Scan(&pool)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return pool, err
}

// SetDefaultWorker makes pool (a connection's, or "" for the shared worker)
// the org's default.
func (s *Service) SetDefaultWorker(ctx context.Context, orgID, actor, pool string) (string, error) {
	pool = strings.TrimSpace(pool)
	if pool == schemas.DefaultRunnerPool || pool == schemas.SharedRunnerPool {
		pool = ""
	}
	if pool != "" {
		if ok, err := s.HasWorker(ctx, orgID, pool); err != nil {
			return "", err
		} else if !ok {
			return "", fmt.Errorf("%w: no worker named %s", ErrInvalid, pool)
		}
	}
	now := db.Millis(s.now())
	_, err := s.db.Exec(ctx, `INSERT INTO worker_prefs (org_id, default_pool, updated_by, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (org_id) DO UPDATE SET default_pool = excluded.default_pool, updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
		orgID, pool, actor, now)
	if err != nil {
		return "", err
	}
	return pool, nil
}

// HasWorker reports whether the org has a connection named pool.
func (s *Service) HasWorker(ctx context.Context, orgID, pool string) (bool, error) {
	var n int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM worker_connections WHERE org_id = ? AND name = ?`, orgID, pool).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// EvaluationPool resolves the pool an evaluation runs on: worker when set
// (a connection's name, or default/shared for the shared worker), else the
// org's default worker; "" means the shared worker (pool default).
func (s *Service) EvaluationPool(ctx context.Context, orgID, worker string) (string, error) {
	worker = strings.TrimSpace(worker)
	if worker == schemas.DefaultRunnerPool || worker == schemas.SharedRunnerPool {
		return "", nil
	}
	if worker == "" {
		return s.DefaultWorker(ctx, orgID)
	}
	ok, err := s.HasWorker(ctx, orgID, worker)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("%w: no worker named %s (Workers page)", ErrInvalid, worker)
	}
	return worker, nil
}

// KubeManifest is the RBAC an org applies before connecting a cluster.
func (s *Service) KubeManifest(namespace string) string { return KubeManifest(namespace) }
