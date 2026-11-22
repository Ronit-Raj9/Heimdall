package openmodel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/sandbox"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/toolproxy"
)

// Deployment states.
const (
	StateStarting = "starting" // deploying on Modal, or waiting for the model to answer
	StateReady    = "ready"    // registered as the org's provider key; runs can use it
	StateStopping = "stopping" // a stop is due or failed once; the reaper retries it
	StateStopped  = "stopped"  // the Modal app is stopped and the key deleted
	StateFailed   = "failed"   // never became ready; the Modal app is stopped
)

// Limits.
const (
	DefaultMaxHours = 2.0
	MaxMaxHours     = 24.0
	// ScaledownSeconds is how long an idle container stays before Modal
	// scales the deployment to zero (no GPU billed while idle).
	ScaledownSeconds = 300
	// StartupSeconds bounds one cold start: download plus load.
	StartupSeconds = 1200
	// MaxActivePerOrg bounds the deployments one org runs at once.
	MaxActivePerOrg = 4
	// HFTokenSecret is the org secret a gated model downloads with.
	HFTokenSecret = "HF_TOKEN"
)

// Errors.
var (
	ErrInvalid       = errors.New("invalid open model request")
	ErrNotConnected  = errors.New("connect Modal first (Settings, Workers, Connect a platform): the model runs on your Modal account")
	ErrTooMany       = fmt.Errorf("an organization runs at most %d open models at once; stop one first", MaxActivePerOrg)
	ErrNotActive     = errors.New("the deployment is not running")
	ErrNeedsHFSecret = errors.New("this model is gated on Hugging Face: accept its licence there and add your token as the org secret " + HFTokenSecret)
)

// Deployment is one open model served on an org's Modal account.
type Deployment struct {
	ID       string `json:"id"`
	OrgID    string `json:"org_id"`
	Model    string `json:"model"`
	Title    string `json:"title"`
	Engine   string `json:"engine"`
	Provider string `json:"provider"` // vllm | sgl
	// ModelName is what an agent config names: <provider>/<model>.
	ModelName string     `json:"model_name"`
	GPU       string     `json:"gpu"`
	GPUCount  int        `json:"gpu_count"`
	USDPerHr  float64    `json:"usd_per_hour"`  // GPU price while a container runs (Modal list price)
	MaxUSD    float64    `json:"max_usd"`       // USDPerHr times the time limit: the most the GPU can cost
	Platform  string     `json:"platform"`      // modal
	AppName   string     `json:"app_name"`      // the Modal app
	BaseURL   string     `json:"base_url"`      // https://...modal.run/v1, once deployed
	KeyID     string     `json:"key_id"`        // the org provider key Heimdall registered
	KeyLast4  string     `json:"key_last4"`     // of the deployment's API key
	ExpID     string     `json:"experiment_id"` // the evaluation it serves; it stops when that one ends
	State     string     `json:"state"`
	Detail    string     `json:"detail,omitempty"`
	CreatedBy string     `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
	ReadyAt   *time.Time `json:"ready_at,omitempty"`
	ExpiresAt time.Time  `json:"expires_at"`
	StoppedAt *time.Time `json:"stopped_at,omitempty"`
	StopWhy   string     `json:"stop_reason,omitempty"`
}

func (d Deployment) active() bool {
	return d.State == StateStarting || d.State == StateReady || d.State == StateStopping
}

// StartRequest is POST /open-models.
type StartRequest struct {
	Model    string  `json:"model"`
	Engine   string  `json:"engine,omitempty"`        // vllm (default) | sglang
	GPU      string  `json:"gpu,omitempty"`           // a Modal GPU name; "" is the catalog's recommended one
	ExpID    string  `json:"experiment_id,omitempty"` // stop when this evaluation ends
	MaxHours float64 `json:"max_hours,omitempty"`     // time limit, default 2, at most 24
}

// PlatformCreds reveals an org's connected platform token (sandbox.Connections).
type PlatformCreds interface {
	CredentialsOf(ctx context.Context, org, name string) (sandbox.Credentials, error)
}

// ExperimentEnded reports whether an org's evaluation has finished (done or
// rejected). An unknown experiment is not ended.
type ExperimentEnded func(ctx context.Context, org, expID string) (bool, error)

// Service runs deployments. Start returns at once with the deployment in
// state starting; the deploy, the readiness wait and the key registration
// run in the background. Run is the reaper: it stops deployments whose
// evaluation ended or whose time limit passed.
type Service struct {
	Keys      keystore.Store
	Platforms PlatformCreds
	Deployer  Deployer
	Store     Store
	Ended     ExperimentEnded // nil: deployments stop on their time limit or by hand only
	HTTP      *http.Client    // readiness checks; nil: a client with a 90 s timeout
	Now       func() time.Time
	Logger    schemas.Logger
	// ReadyWithin bounds deploy plus first answer (default 30 minutes).
	ReadyWithin time.Duration
	// PollEvery is the readiness poll and reaper interval (default 15 s).
	PollEvery time.Duration

	mu      sync.Mutex
	started map[string]bool // deployments whose background start runs in this process
	wg      sync.WaitGroup
	base    context.Context
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) logger() schemas.Logger {
	if s.Logger == nil {
		return schemas.NoopLogger{}
	}
	return s.Logger
}

func (s *Service) poll() time.Duration {
	if s.PollEvery > 0 {
		return s.PollEvery
	}
	return 15 * time.Second
}

func (s *Service) httpClient() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return &http.Client{Timeout: 90 * time.Second}
}

func (s *Service) bgCtx() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.base != nil {
		return s.base
	}
	return context.Background()
}

// Catalog is the models, engines and GPUs on offer.
func (s *Service) Catalog() CatalogView { return View() }

// List is an org's deployments, newest first.
func (s *Service) List(ctx context.Context, org string) ([]Deployment, error) {
	return s.Store.List(ctx, org)
}

// Get is one deployment.
func (s *Service) Get(ctx context.Context, org, id string) (*Deployment, error) {
	return s.Store.Get(ctx, org, id)
}

func newDeploymentID() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

func newServeKey() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return "hmo_" + hex.EncodeToString(b)
}

// plan validates a request into a deployment and its deploy spec.
func plan(req StartRequest) (Model, string, GPUChoice, float64, error) {
	m, ok := LookupModel(req.Model)
	if !ok {
		ids := make([]string, 0, len(Catalog))
		for _, c := range Catalog {
			ids = append(ids, c.ID)
		}
		return Model{}, "", GPUChoice{}, 0, fmt.Errorf("%w: model must be one of %s", ErrInvalid, strings.Join(ids, ", "))
	}
	engine := strings.ToLower(strings.TrimSpace(req.Engine))
	switch engine {
	case "":
		engine = EngineVLLM
	case EngineVLLM, EngineSGLang:
	case "sgl":
		engine = EngineSGLang
	default:
		return Model{}, "", GPUChoice{}, 0, fmt.Errorf("%w: engine must be vllm or sglang", ErrInvalid)
	}
	var pick *GPUChoice
	for _, g := range GPUsFor(m) {
		g := g
		if (req.GPU == "" && g.Recommended) || (req.GPU != "" && strings.EqualFold(g.Name, strings.TrimSpace(req.GPU))) {
			pick = &g
		}
	}
	if req.GPU != "" && strings.EqualFold(strings.TrimSpace(req.GPU), "A10G") {
		return plan(StartRequest{Model: req.Model, Engine: engine, GPU: "A10", ExpID: req.ExpID, MaxHours: req.MaxHours})
	}
	switch {
	case pick == nil:
		return Model{}, "", GPUChoice{}, 0, fmt.Errorf("%w: gpu must be one of %s", ErrInvalid, strings.Join(sandbox.ModalGPUNames(), ", "))
	case pick.Count == 0:
		return Model{}, "", GPUChoice{}, 0, fmt.Errorf("%w: %s cannot serve %s: %s", ErrInvalid, pick.Name, m.ID, pick.UnsupportedWhy)
	}
	hours := req.MaxHours
	switch {
	case hours == 0:
		hours = DefaultMaxHours
	case hours < 0.1 || hours > MaxMaxHours || math.IsNaN(hours):
		return Model{}, "", GPUChoice{}, 0, fmt.Errorf("%w: max_hours must be between 0.1 and %g", ErrInvalid, MaxMaxHours)
	}
	return m, engine, *pick, hours, nil
}

// Start validates the request, checks the org's Modal connection and starts
// the deployment in the background.
func (s *Service) Start(ctx context.Context, org, actor string, req StartRequest) (*Deployment, error) {
	m, engine, g, hours, err := plan(req)
	if err != nil {
		return nil, err
	}
	if exp := strings.TrimSpace(req.ExpID); exp != "" && (len(exp) > 128 || strings.ContainsAny(exp, " /\\\r\n")) {
		return nil, fmt.Errorf("%w: experiment_id is not an experiment id", ErrInvalid)
	}
	creds, err := s.Platforms.CredentialsOf(ctx, org, sandbox.PlatformModal)
	if err != nil {
		if errors.Is(err, sandbox.ErrNotConnected) {
			return nil, ErrNotConnected
		}
		return nil, err
	}
	hf := ""
	if m.Gated {
		sec, err := toolproxy.OrgSecretEnv(ctx, s.Keys, org, []string{HFTokenSecret})
		if err != nil || sec[HFTokenSecret] == "" {
			return nil, ErrNeedsHFSecret
		}
		hf = sec[HFTokenSecret]
	}
	all, err := s.Store.List(ctx, org)
	if err != nil {
		return nil, err
	}
	n := 0
	for _, d := range all {
		if d.active() {
			n++
		}
	}
	if n >= MaxActivePerOrg {
		return nil, ErrTooMany
	}
	now := s.now()
	id := newDeploymentID()
	key := newServeKey()
	d := &Deployment{ID: id, OrgID: org, Model: m.ID, Title: m.Title, Engine: engine, Provider: Provider(engine),
		ModelName: Provider(engine) + "/" + servedName(m), GPU: g.Name, GPUCount: g.Count, USDPerHr: g.USDPerHourAll,
		MaxUSD: math.Round(g.USDPerHourAll*hours*100) / 100, Platform: sandbox.PlatformModal, AppName: appName(id),
		KeyLast4: key[len(key)-4:], ExpID: strings.TrimSpace(req.ExpID), State: StateStarting, Detail: "deploying on Modal",
		CreatedBy: actor, CreatedAt: now, ExpiresAt: now.Add(time.Duration(hours * float64(time.Hour)))}
	if err := s.Store.Put(ctx, d); err != nil {
		return nil, err
	}
	spec := DeploySpec{AppName: d.AppName, Engine: engine, Model: m, GPU: g.GPUType, GPUCount: g.Count, APIKey: key, HFToken: hf,
		ScaledownS: ScaledownSeconds, StartupS: StartupSeconds, Concurrency: 32}
	s.mu.Lock()
	if s.started == nil {
		s.started = map[string]bool{}
	}
	s.started[id] = true
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() { s.mu.Lock(); delete(s.started, id); s.mu.Unlock() }()
		s.bring(s.bgCtx(), *d, creds, spec)
	}()
	out := *d
	return &out, nil
}

// Wait waits for the background starts of this process (tests, shutdown).
func (s *Service) Wait() { s.wg.Wait() }

// bring deploys, waits for the first answer and registers the key. Any
// failure stops the Modal app again.
func (s *Service) bring(ctx context.Context, d Deployment, creds sandbox.Credentials, spec DeploySpec) {
	within := s.ReadyWithin
	if within <= 0 {
		within = 30 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, within)
	defer cancel()
	spec.DeployWithin = within
	fail := func(why string) {
		s.logger().Warn("open model: start failed; stopping its Modal app", "org", d.OrgID, "deployment", d.ID, "error", why)
		d.Detail = why
		d.StopWhy = "start failed"
		s.stop(context.WithoutCancel(ctx), &d, creds, StateFailed)
	}
	url, err := s.Deployer.Deploy(ctx, creds, spec)
	if err != nil {
		fail(err.Error())
		return
	}
	d.BaseURL = url + "/v1"
	d.Detail = "deployed; waiting for the model to load (the first start downloads it)"
	if cur, err := s.Store.Get(ctx, d.OrgID, d.ID); err == nil && cur.State != StateStarting {
		d.State = cur.State // stopped by hand meanwhile
		s.stop(context.WithoutCancel(ctx), &d, creds, StateStopped)
		return
	}
	_ = s.Store.Put(ctx, &d)
	stoppedMeanwhile := func() bool {
		cur, err := s.Store.Get(ctx, d.OrgID, d.ID)
		return err == nil && cur.State != StateStarting
	}
	if err := s.waitReady(ctx, d.BaseURL, spec.APIKey, servedName(spec.Model), stoppedMeanwhile); err != nil {
		if stoppedMeanwhile() {
			s.stop(context.WithoutCancel(ctx), &d, creds, StateStopped)
			return
		}
		fail("the model did not answer: " + err.Error())
		return
	}
	if cur, err := s.Store.Get(ctx, d.OrgID, d.ID); err == nil && cur.State != StateStarting {
		s.stop(context.WithoutCancel(ctx), &d, creds, StateStopped)
		return
	}
	k, err := s.Keys.Create(ctx, d.OrgID, keystore.KindProvider, keystore.Put{
		Provider: d.Provider, BaseURL: d.BaseURL, Value: spec.APIKey, Models: []string{servedName(spec.Model)},
		Label: fmt.Sprintf("Open model %s on Modal %s (%s)", spec.Model.Title, d.GPU, d.ID),
	}, "heimdall:open-models:"+d.CreatedBy)
	if err != nil {
		fail("register the endpoint as a provider key: " + err.Error())
		return
	}
	at := s.now()
	d.KeyID, d.State, d.ReadyAt, d.Detail = k.ID, StateReady, &at, "ready; scales to zero after 5 idle minutes"
	_ = s.Keys.SetStatus(ctx, d.OrgID, k.ID, "ok", at)
	if err := s.Store.Put(ctx, &d); err != nil {
		s.logger().Error("open model: record the ready deployment", "deployment", d.ID, "error", err.Error())
	}
	s.logger().Info("open model ready", "org", d.OrgID, "deployment", d.ID, "model", d.Model, "gpu", d.GPU)
}

// waitReady polls GET /v1/models with the deployment's key until the served
// model is listed. The first request starts a container (cold start).
func (s *Service) waitReady(ctx context.Context, base, key, model string, stopped func() bool) error {
	var last error
	for {
		if stopped != nil && stopped() {
			return errors.New("stopped while starting")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := s.httpClient().Do(req)
		if err == nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()
			switch {
			case resp.StatusCode == http.StatusOK && strings.Contains(string(body), model):
				return nil
			case resp.StatusCode == http.StatusUnauthorized:
				return errors.New("the server refused the deployment's own key")
			default:
				last = fmt.Errorf("HTTP %d", resp.StatusCode)
			}
		} else {
			last = errors.New(strings.ReplaceAll(err.Error(), key, "[redacted]"))
		}
		select {
		case <-ctx.Done():
			if last != nil {
				return fmt.Errorf("%w (last: %v)", ctx.Err(), last)
			}
			return ctx.Err()
		case <-time.After(s.poll()):
		}
	}
}

// Stop stops a deployment by hand.
func (s *Service) Stop(ctx context.Context, org, id, actor string) (*Deployment, error) {
	d, err := s.Store.Get(ctx, org, id)
	if err != nil {
		return nil, err
	}
	if !d.active() {
		return d, nil
	}
	creds, err := s.Platforms.CredentialsOf(ctx, org, sandbox.PlatformModal)
	if err != nil {
		d.State, d.Detail = StateStopping, "Modal is no longer connected; stop the app "+d.AppName+" in the Modal dashboard"
		_ = s.Store.Put(ctx, d)
		return d, nil
	}
	d.StopWhy = "stopped by " + actor
	s.stop(ctx, d, creds, StateStopped)
	return d, nil
}

// stop stops the Modal app, deletes the provider key and records final. A
// failed stop leaves the deployment in stopping for the reaper.
func (s *Service) stop(ctx context.Context, d *Deployment, creds sandbox.Credentials, final string) {
	if d.KeyID != "" {
		if err := s.Keys.Delete(ctx, d.OrgID, d.KeyID, "heimdall:open-models"); err != nil && !errors.Is(err, keystore.ErrNotFound) {
			s.logger().Warn("open model: delete the provider key", "deployment", d.ID, "error", err.Error())
		} else {
			d.KeyID = ""
		}
	}
	sctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := s.Deployer.Stop(sctx, creds, d.AppName); err != nil {
		d.State = StateStopping
		d.Detail = "stopping the Modal app failed; retrying: " + err.Error()
		_ = s.Store.Put(ctx, d)
		return
	}
	at := s.now()
	d.State, d.StoppedAt = final, &at
	if final == StateStopped {
		d.Detail = "stopped; nothing runs or bills on Modal"
	}
	if err := s.Store.Put(ctx, d); err != nil {
		s.logger().Error("open model: record the stop", "deployment", d.ID, "error", err.Error())
	}
}

// Reap stops every deployment whose evaluation ended or whose time limit
// passed, and retries failed stops. It returns how many it stopped.
func (s *Service) Reap(ctx context.Context) int {
	act, err := s.Store.Active(ctx)
	if err != nil {
		s.logger().Warn("open model reaper: list", "error", err.Error())
		return 0
	}
	n := 0
	for i := range act {
		d := act[i]
		s.mu.Lock()
		running := s.started[d.ID]
		s.mu.Unlock()
		why := ""
		switch {
		case d.State == StateStopping:
			why = d.StopWhy
			if why == "" {
				why = "retry"
			}
		case !s.now().Before(d.ExpiresAt):
			why = "time limit reached"
		case d.ExpID != "" && s.Ended != nil && d.State == StateReady:
			if ended, err := s.Ended(ctx, d.OrgID, d.ExpID); err == nil && ended {
				why = "evaluation " + d.ExpID + " finished"
			}
		case d.State == StateStarting && !running && s.now().Sub(d.CreatedAt) > 2*time.Hour:
			why = "start never finished (server restarted)" // the start ran in a process that is gone
		}
		if why == "" {
			continue
		}
		if running && d.State == StateStarting {
			continue // its own start stops it on failure; a time limit waits for it
		}
		creds, err := s.Platforms.CredentialsOf(ctx, d.OrgID, sandbox.PlatformModal)
		if err != nil {
			s.logger().Warn("open model reaper: no Modal credentials", "org", d.OrgID, "deployment", d.ID, "error", err.Error())
			continue
		}
		final := StateStopped
		if d.State == StateStarting {
			final = StateFailed
		}
		d.StopWhy = why
		s.stop(ctx, &d, creds, final)
		if d.State == final {
			n++
		}
	}
	return n
}

// Run reaps every PollEvery until ctx ends. Background starts use ctx too.
func (s *Service) Run(ctx context.Context) {
	s.mu.Lock()
	s.base = ctx
	s.mu.Unlock()
	t := time.NewTicker(max(s.poll(), 30*time.Second))
	defer t.Stop()
	for {
		s.Reap(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// OrphanApps lists the org's heimdall-om-* apps on Modal that Heimdall has
// no active deployment for (a stop that never reached Modal). The settings
// screen offers to stop them.
func (s *Service) OrphanApps(ctx context.Context, org string) ([]AppInfo, error) {
	creds, err := s.Platforms.CredentialsOf(ctx, org, sandbox.PlatformModal)
	if err != nil {
		return nil, err
	}
	apps, err := s.Deployer.List(ctx, creds)
	if err != nil {
		return nil, err
	}
	all, err := s.Store.List(ctx, org)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, d := range all {
		if d.active() {
			known[d.AppName] = true
		}
	}
	out := []AppInfo{}
	for _, a := range apps {
		if !known[a.Name] && (a.State == "deployed" || a.State == "deploying" || a.State == "running") {
			out = append(out, a)
		}
	}
	return out, nil
}
