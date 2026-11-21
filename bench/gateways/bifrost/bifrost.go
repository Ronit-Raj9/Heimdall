// Package bifrost implements schemas.Gateway against a stock Bifrost over its
// HTTP management API (docs/DESIGN.md section 13, docs/spike.md).
//
// The bench owns one or more providers, each with a fixed pool of provider
// keys ("key slots"), and one virtual key per run on one slot. A proxied
// provider's base URL is the fault proxy; a direct provider talks to its
// real API (no ledger, no model faults).
// Every management call carries the setup token in X-Bifrost-Setup-Token; the
// token never reaches an agent container. Log rows are read back through
// /api/logs and joined to the fault proxy ledger, which is the completeness
// proof: Bifrost forwards no request id upstream, so the join runs on the
// upstream message id, the fault nonce and sha256(raw_request).
package bifrost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Name and version recorded in the run manifest.
const (
	Name    = "bifrost"
	Version = "1.0.0"
)

// Defaults applied by New and FetchRequests.
const (
	DefaultProvider  = "anthropic"
	DefaultTimeout   = 60 * time.Second
	DefaultPollEvery = time.Second

	setupTokenHeader   = "X-Bifrost-Setup-Token"
	httpTimeout        = 30 * time.Second
	retryBackoffMS     = 100
	retryBackoffMaxMS  = 200
	requestTimeoutS    = 300
	providerWorkers    = 16
	providerBufferSize = 256
)

var _ schemas.Gateway = (*Gateway)(nil)

// Provider modes. A proxied provider's base URL is the fault proxy and its
// keys are placeholder slots the proxy swaps for the real key; every call is
// in the proxy ledger and model faults can be injected. A direct provider
// talks to the real API with the real key held by Bifrost: no proxy, no
// ledger, no model-outage probe.
const (
	ModeProxied = "proxied"
	ModeDirect  = "direct"
)

// Config configures a Gateway. Providers lists every provider the bench
// uses. The single-provider fields Provider, Model and Slots are the older
// form: when Providers is empty they describe one proxied provider.
type Config struct {
	URL           string // Bifrost as the bench host sees it, e.g. http://127.0.0.1:18080
	SetupToken    string // management credential; never logged, never given to an agent
	Providers     []ProviderSetup
	Provider      string              // older form: the one provider, anthropic when empty
	FaultProxyURL string              // the fault proxy as Bifrost sees it, http://faultproxy:9100
	Model         string              // older form: model every key slot serves at setup
	Slots         []Slot              // older form: the provider's key slots
	MaxRetries    int                 // provider max_retries; 0 lets every fault reach the agent
	EvalNetwork   string              // internal docker network agents run on
	HelperImage   string              // image for the preflight egress probe; needs getent and python3
	Getenv        func(string) string // reads direct providers' keys; os.Getenv when nil
	Docker        utils.Docker
	Logger        schemas.Logger
	HTTP          *http.Client
	// F1: key slot ownership across bench processes (slotclaims.go). Claims
	// is the fault proxy's claim API, the same client that writes plans; nil
	// keeps slots process-local. ClaimTTL defaults to faultproxy.DefaultClaimTTL
	// and ClaimWait, how long a mint waits while every free slot is claimed
	// by another process, to DefaultClaimWait.
	Claims    SlotClaimer
	ClaimTTL  time.Duration
	ClaimWait time.Duration
}

// ProviderSetup is one Bifrost provider and its key slots. In proxied mode
// each slot's Value is the placeholder the fault proxy maps back to the slot.
// In direct mode every slot carries the real key read from KeyEnv at Setup;
// slot values are ignored and the key is never logged or written to disk.
type ProviderSetup struct {
	Name   string // Bifrost provider name: anthropic, openai, gemini, groq, ...
	Mode   string // proxied (default) | direct
	Slots  []Slot
	Model  string // model every slot serves at setup; MintKey adds the run's model
	KeyEnv string // direct only: environment variable with the real key
	// Custom: an OpenAI-compatible custom provider (lane A09); Bifrost
	// creates it with custom_provider_config base_provider_type openai.
	Custom bool
}

// Slot is one provider key in Bifrost. Value is the placeholder credential
// Bifrost sends upstream; the fault proxy maps it back to the slot.
type Slot struct {
	Name  string
	Value string
	// Extra is the provider-specific key config block written with the key
	// (azure_key_config, bedrock_key_config, ...; lane A09). It may hold
	// secrets of a direct provider: never logged.
	Extra map[string]any
}

// Gateway is the Bifrost client. It is safe for concurrent use.
type Gateway struct {
	cfg       Config
	providers []ProviderSetup // normalized from cfg, read-only
	http      *http.Client
	logger    schemas.Logger
	// pageSize and settle are the log list page size and emptyLedgerSettle;
	// fields so tests can shrink them.
	pageSize int
	settle   time.Duration

	mu       sync.Mutex
	keyIDs   map[string]string          // slot name -> Bifrost provider key id, set by Setup
	models   map[string]map[string]bool // slot name -> models the provider key serves
	inUse    map[string]string          // slot name -> virtual key id holding it
	minted   map[string]mintRecord      // virtual key id -> its slot and creation time
	teams    map[string]string          // run key id -> Bifrost team id of a team run (team.go)
	memberOf map[string]string          // member key id -> its run id (teamslots.go)
	claims   *claimState                // F1: this process's key slot claims at the fault proxy (slotclaims.go)
	orgSt    *orgCreds                  // F4: organization key slots (orgcreds.go)
	logSt    *orgLogs                   // X03: per-organization log tags and content logging (orglogs.go)
}

// mintRecord remembers what this process minted, so RevokeKey releases the
// right slot and FetchRequests can bound its time window.
type mintRecord struct {
	slot string
	at   time.Time
	run  string // the key's name (run id, or a derived service or member name)
}

// New validates cfg and returns a Gateway. It makes no network call; Setup
// configures Bifrost.
func New(cfg Config) (*Gateway, error) {
	cfg.URL = strings.TrimRight(cfg.URL, "/")
	if cfg.Getenv == nil {
		cfg.Getenv = os.Getenv
	}
	providers := normalizeProviders(cfg)
	if err := validateConfig(cfg, providers); err != nil {
		return nil, err
	}
	client := cfg.HTTP
	if client == nil {
		client = &http.Client{Timeout: httpTimeout}
	}
	logger := cfg.Logger
	if logger == nil {
		logger = schemas.NoopLogger{}
	}
	return &Gateway{
		cfg: cfg, providers: providers, http: client, logger: logger, pageSize: logPageSize, settle: emptyLedgerSettle,
		keyIDs: map[string]string{}, models: map[string]map[string]bool{},
		inUse: map[string]string{}, minted: map[string]mintRecord{}, teams: map[string]string{}, memberOf: map[string]string{},
		claims: newClaimState(),
	}, nil
}

// ProviderMode returns how the named provider is wired, ModeProxied or
// ModeDirect, or "" when the gateway does not configure it. Probes that
// inject model faults must refuse a direct provider at plan time.
func (g *Gateway) ProviderMode(name string) string {
	if p, ok := g.provider(name); ok {
		return p.Mode
	}
	return ""
}

// GetName implements schemas.Part.
func (g *Gateway) GetName() string { return Name }

// GetVersion implements schemas.Part.
func (g *Gateway) GetVersion() string { return Version }

// Setup configures every provider and its key slots, as the spike's setup.sh
// does, and records each slot's key id. It is idempotent: an existing
// provider is updated in place only when a recorded setting differs (every
// provider PUT makes Bifrost list models upstream for each key), and an
// existing key is updated rather than created, because Bifrost masks key
// values and its key names are unique across providers.
func (g *Gateway) Setup(ctx context.Context) error {
	for _, p := range g.providers {
		if err := g.setupProvider(ctx, p); err != nil {
			return err
		}
	}
	return nil
}

// setupProvider configures one provider and its slots.
func (g *Gateway) setupProvider(ctx context.Context, p ProviderSetup) error {
	secret := ""
	if p.Mode == ModeDirect {
		if secret = strings.TrimSpace(g.cfg.Getenv(p.KeyEnv)); secret == "" {
			return fmt.Errorf("direct provider %s: %s is not set", p.Name, p.KeyEnv)
		}
	}
	if err := g.ensureProvider(ctx, p); err != nil {
		return err
	}
	var existing struct {
		Keys []providerKey `json:"keys"`
	}
	if err := g.call(ctx, http.MethodGet, providerPath(p.Name)+"/keys", nil, &existing); err != nil {
		return fmt.Errorf("list %s keys: %w", p.Name, err)
	}
	byName := make(map[string]providerKey, len(existing.Keys))
	for _, k := range existing.Keys {
		byName[k.Name] = k
	}
	for _, s := range p.Slots {
		if secret != "" {
			s.Value = secret
		}
		models := mergeModels(byName[s.Name].Models, p.Model)
		id, models, err := g.upsertKey(ctx, p.Name, s, byName[s.Name].ID, models)
		if err != nil {
			return err
		}
		g.mu.Lock()
		g.keyIDs[s.Name] = id
		g.models[s.Name] = toSet(models)
		g.mu.Unlock()
	}
	g.logger.Info("bifrost provider set up", "provider", p.Name, "mode", p.Mode, "slots", len(p.Slots),
		"base_url", g.baseURL(p), "max_retries", g.cfg.MaxRetries)
	return nil
}

// providerKey is one provider key as Bifrost lists it. The value is masked.
type providerKey struct {
	ID      string                     `json:"id"`
	Name    string                     `json:"name"`
	Models  []string                   `json:"models"`
	Weight  float64                    `json:"weight"`
	Enabled *bool                      `json:"enabled"`
	Aliases map[string]json.RawMessage `json:"aliases,omitempty"` // model -> inference profile; read by the hygiene check
}

// providerConfig is the subset of GET /api/providers/{provider} the bench
// sets and checks.
type providerConfig struct {
	NetworkConfig struct {
		BaseURL             string `json:"base_url"`
		MaxRetries          int    `json:"max_retries"`
		RetryBackoffInitial int    `json:"retry_backoff_initial"`
		RetryBackoffMax     int    `json:"retry_backoff_max"`
		RequestTimeoutS     int    `json:"default_request_timeout_in_seconds"`
		AllowPrivateNetwork bool   `json:"allow_private_network"`
	} `json:"network_config"`
	StoreRaw      bool              `json:"store_raw_request_response"`
	InjectedTools []json.RawMessage `json:"injected_tools,omitempty"` // read by the hygiene check
}

// normalizeProviders turns cfg into the provider list: Providers as given,
// or the older single-provider fields as one proxied provider. Mode defaults
// to proxied. It never modifies cfg.
func normalizeProviders(cfg Config) []ProviderSetup {
	src := cfg.Providers
	if len(src) == 0 {
		name := cfg.Provider
		if name == "" {
			name = DefaultProvider
		}
		src = []ProviderSetup{{Name: name, Mode: ModeProxied, Slots: cfg.Slots, Model: cfg.Model}}
	}
	out := make([]ProviderSetup, 0, len(src))
	for _, p := range src {
		if p.Mode == "" {
			p.Mode = ModeProxied
		}
		p.Slots = append([]Slot{}, p.Slots...)
		out = append(out, p)
	}
	return out
}

func validateConfig(cfg Config, providers []ProviderSetup) error {
	var errs []error
	if cfg.URL == "" {
		errs = append(errs, errors.New("url is required"))
	}
	if cfg.SetupToken == "" {
		errs = append(errs, errors.New("setup token is required"))
	}
	if cfg.MaxRetries < 0 {
		errs = append(errs, fmt.Errorf("max retries %d is negative", cfg.MaxRetries))
	}
	names, slots := map[string]bool{}, map[string]bool{}
	for _, p := range providers {
		errs = append(errs, validateProvider(cfg, p, names, slots)...)
	}
	if len(errs) > 0 {
		return fmt.Errorf("bifrost gateway config: %w", errors.Join(errs...))
	}
	return nil
}

func validateProvider(cfg Config, p ProviderSetup, names, slots map[string]bool) []error {
	var errs []error
	switch {
	case p.Name == "":
		errs = append(errs, errors.New("provider name is required"))
	case names[p.Name]:
		errs = append(errs, fmt.Errorf("provider %s is listed twice", p.Name))
	}
	names[p.Name] = true
	switch p.Mode {
	case ModeProxied:
		if cfg.FaultProxyURL == "" {
			errs = append(errs, fmt.Errorf("provider %s: fault proxy url is required", p.Name))
		}
	case ModeDirect:
		if p.KeyEnv == "" {
			errs = append(errs, fmt.Errorf("direct provider %s: key env is required", p.Name))
		}
	default:
		errs = append(errs, fmt.Errorf("provider %s: mode must be proxied or direct, got %q", p.Name, p.Mode))
	}
	if p.Model == "" {
		errs = append(errs, fmt.Errorf("provider %s: model is required", p.Name))
	}
	if len(p.Slots) == 0 {
		errs = append(errs, fmt.Errorf("provider %s: at least one key slot is required", p.Name))
	}
	for i, s := range p.Slots {
		switch {
		case s.Name == "" || (s.Value == "" && p.Mode == ModeProxied):
			errs = append(errs, fmt.Errorf("provider %s: slot %d needs a name and a value", p.Name, i))
		case slots[s.Name]:
			errs = append(errs, fmt.Errorf("slot %s is listed twice", s.Name))
		}
		slots[s.Name] = true
	}
	return errs
}

// ensureProvider creates the provider, or updates it when a setting the bench
// owns differs from the wanted value.
func (g *Gateway) ensureProvider(ctx context.Context, p ProviderSetup) error {
	var cur providerConfig
	err := g.call(ctx, http.MethodGet, providerPath(p.Name), nil, &cur)
	switch {
	case isStatus(err, http.StatusNotFound):
		body := g.providerBody(p)
		body["provider"] = p.Name
		err := g.call(ctx, http.MethodPost, "/api/providers", body, nil)
		if !isStatus(err, http.StatusConflict) {
			if err != nil {
				return fmt.Errorf("create provider %s: %w", p.Name, err)
			}
			return nil
		}
		// Another bench process created it between our GET and POST: read
		// what it wrote and converge on it like an existing provider.
		cur = providerConfig{}
		if err := g.call(ctx, http.MethodGet, providerPath(p.Name), nil, &cur); err != nil {
			return fmt.Errorf("get provider %s after a create conflict: %w", p.Name, err)
		}
		if g.providerMatches(p, cur) {
			return nil
		}
	case err != nil:
		return fmt.Errorf("get provider %s: %w", p.Name, err)
	case g.providerMatches(p, cur):
		return nil
	}
	if err := g.call(ctx, http.MethodPut, providerPath(p.Name), g.providerBody(p), nil); err != nil {
		return fmt.Errorf("update provider %s: %w", p.Name, err)
	}
	return nil
}

// providerBody is the provider definition from the spike: explicit retries
// with short backoffs and raw request and response storage, which puts the
// upstream response id and the request bytes into each log row. A proxied
// provider's base URL is the fault proxy (a private address, hence
// allow_private_network); a direct provider keeps Bifrost's default base URL
// (an empty base_url).
func (g *Gateway) providerBody(p ProviderSetup) map[string]any {
	body := map[string]any{
		"network_config": map[string]any{
			"base_url":                           g.baseURL(p),
			"allow_private_network":              p.Mode == ModeProxied,
			"max_retries":                        g.cfg.MaxRetries,
			"retry_backoff_initial":              retryBackoffMS,
			"retry_backoff_max":                  retryBackoffMaxMS,
			"default_request_timeout_in_seconds": requestTimeoutS,
		},
		"concurrency_and_buffer_size": map[string]any{
			"concurrency": providerWorkers,
			"buffer_size": providerBufferSize,
		},
		"store_raw_request_response": true,
	}
	if p.Custom {
		body["custom_provider_config"] = map[string]any{"base_provider_type": "openai", "is_key_less": false}
	}
	return body
}

func (g *Gateway) providerMatches(p ProviderSetup, cur providerConfig) bool {
	n := cur.NetworkConfig
	return n.BaseURL == g.baseURL(p) && n.AllowPrivateNetwork == (p.Mode == ModeProxied) && n.MaxRetries == g.cfg.MaxRetries &&
		n.RetryBackoffInitial == retryBackoffMS && n.RetryBackoffMax == retryBackoffMaxMS &&
		n.RequestTimeoutS == requestTimeoutS && cur.StoreRaw
}

// baseURL is the base_url the provider must have: the fault proxy, or empty
// (Bifrost's default) for a direct provider.
func (g *Gateway) baseURL(p ProviderSetup) string {
	if p.Mode == ModeDirect {
		return ""
	}
	return g.cfg.FaultProxyURL
}

// upsertKey creates the slot's provider key, or updates it in place when id
// is set, and returns the key id. Weight 1 is required for routing. The value
// may be a real key (direct mode); it is sent only in the request body.
//
// It returns the key id and the models the key serves after the write
// (QC2-12-04): after a create conflict that is the merged list, not the
// caller's, so the caller's model cache never drops a model the existing
// key already served.
func (g *Gateway) upsertKey(ctx context.Context, provider string, s Slot, id string, models []string) (string, []string, error) {
	body := map[string]any{"name": s.Name, "value": s.Value, "models": models, "weight": 1, "enabled": true}
	for k, v := range s.Extra { // A09: provider key config block
		body[k] = v
	}
	var out providerKey
	if id == "" {
		err := g.call(ctx, http.MethodPost, providerPath(provider)+"/keys", body, &out)
		switch {
		case isStatus(err, http.StatusConflict):
			// Key names are unique across providers: a concurrent setup
			// (another run or doctor) created this slot after our listing.
			// Adopt it when it is under this provider and update it below,
			// keeping the models it already serves.
			k, ferr := g.findKey(ctx, provider, s.Name)
			if ferr != nil {
				return "", nil, fmt.Errorf("create key slot %s: %w (after %v)", s.Name, ferr, err)
			}
			id, out = k.ID, providerKey{}
			merged := k.Models
			for _, m := range models {
				merged = mergeModels(merged, m)
			}
			body["models"] = merged
			models = merged
		case err != nil:
			return "", nil, fmt.Errorf("create key slot %s: %w", s.Name, err)
		}
	}
	if out.ID == "" && id != "" {
		if err := g.call(ctx, http.MethodPut, providerPath(provider)+"/keys/"+id, body, &out); err != nil {
			return "", nil, fmt.Errorf("update key slot %s: %w", s.Name, err)
		}
	}
	if out.ID == "" {
		out.ID = id
	}
	if out.ID == "" {
		return "", nil, fmt.Errorf("key slot %s: bifrost returned no key id", s.Name)
	}
	if len(out.Models) > 0 { // the server's own list is authoritative
		models = out.Models
	}
	return out.ID, models, nil
}

// findKey reads the provider's keys again and returns the one named name.
// A name taken under another provider is an error: that slot is not ours.
func (g *Gateway) findKey(ctx context.Context, provider, name string) (providerKey, error) {
	var existing struct {
		Keys []providerKey `json:"keys"`
	}
	if err := g.call(ctx, http.MethodGet, providerPath(provider)+"/keys", nil, &existing); err != nil {
		return providerKey{}, fmt.Errorf("list %s keys: %w", provider, err)
	}
	for _, k := range existing.Keys {
		if k.Name == name && k.ID != "" {
			return k, nil
		}
	}
	return providerKey{}, fmt.Errorf("key name %s is taken, but not under provider %s", name, provider)
}

// provider returns the named provider's setup.
func (g *Gateway) provider(name string) (ProviderSetup, bool) {
	for _, p := range g.providers {
		if p.Name == name {
			return p, true
		}
	}
	return ProviderSetup{}, false
}

// slotProvider returns the provider that owns a slot.
func (g *Gateway) slotProvider(slot string) (ProviderSetup, bool) {
	for _, p := range g.providers {
		for _, s := range p.Slots {
			if s.Name == slot {
				return p, true
			}
		}
	}
	return g.orgSlotProvider(slot) // F4: organization key slots (orgcreds.go)
}

func providerPath(name string) string {
	return "/api/providers/" + url.PathEscape(name)
}

// mergeModels returns have plus want, without duplicates, in a stable order.
func mergeModels(have []string, want string) []string {
	out := make([]string, 0, len(have)+1)
	seen := map[string]bool{}
	for _, m := range append(append([]string{}, have...), want) {
		if m != "" && !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}

func toSet(xs []string) map[string]bool {
	out := make(map[string]bool, len(xs))
	for _, x := range xs {
		out[x] = true
	}
	return out
}
