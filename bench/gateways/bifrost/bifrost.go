// Package bifrost implements schemas.Gateway against a stock Bifrost over its
// HTTP management API (docs/DESIGN.md section 13, docs/spike.md).
//
// The bench owns one provider (anthropic) whose base URL is the fault proxy,
// a fixed pool of provider keys ("key slots") and one virtual key per run.
// Every management call carries the setup token in X-Bifrost-Setup-Token; the
// token never reaches an agent container. Log rows are read back through
// /api/logs and joined to the fault proxy ledger, which is the completeness
// proof: Bifrost forwards no request id upstream, so the join runs on the
// upstream message id, the fault nonce and sha256(raw_request).
package bifrost

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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

// Config configures a Gateway.
type Config struct {
	URL           string // Bifrost as the bench host sees it, e.g. http://127.0.0.1:18080
	SetupToken    string // management credential; never logged, never given to an agent
	Provider      string // anthropic
	FaultProxyURL string // the fault proxy as Bifrost sees it, http://faultproxy:9100
	Model         string // model every key slot serves at setup
	Slots         []Slot
	MaxRetries    int    // provider max_retries; 0 lets every fault reach the agent
	EvalNetwork   string // internal docker network agents run on
	HelperImage   string // image for the preflight egress probe; needs getent and python3
	Docker        utils.Docker
	Logger        schemas.Logger
	HTTP          *http.Client
}

// Slot is one provider key in Bifrost. Value is the placeholder credential
// Bifrost sends upstream; the fault proxy maps it back to the slot.
type Slot struct {
	Name  string
	Value string
}

// Gateway is the Bifrost client. It is safe for concurrent use.
type Gateway struct {
	cfg    Config
	http   *http.Client
	logger schemas.Logger
	// pageSize and settle are the log list page size and emptyLedgerSettle;
	// fields so tests can shrink them.
	pageSize int
	settle   time.Duration

	mu     sync.Mutex
	keyIDs map[string]string          // slot name -> Bifrost provider key id, set by Setup
	models map[string]map[string]bool // slot name -> models the provider key serves
	inUse  map[string]string          // slot name -> virtual key id holding it
	minted map[string]mintRecord      // virtual key id -> its slot and creation time
}

// mintRecord remembers what this process minted, so RevokeKey releases the
// right slot and FetchRequests can bound its time window.
type mintRecord struct {
	slot string
	at   time.Time
}

// New validates cfg and returns a Gateway. It makes no network call; Setup
// configures Bifrost.
func New(cfg Config) (*Gateway, error) {
	cfg.URL = strings.TrimRight(cfg.URL, "/")
	if cfg.Provider == "" {
		cfg.Provider = DefaultProvider
	}
	if err := validateConfig(cfg); err != nil {
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
		cfg: cfg, http: client, logger: logger, pageSize: logPageSize, settle: emptyLedgerSettle,
		keyIDs: map[string]string{}, models: map[string]map[string]bool{},
		inUse: map[string]string{}, minted: map[string]mintRecord{},
	}, nil
}

// GetName implements schemas.Part.
func (g *Gateway) GetName() string { return Name }

// GetVersion implements schemas.Part.
func (g *Gateway) GetVersion() string { return Version }

// Setup configures the provider and its key slots, as the spike's setup.sh
// does, and records each slot's key id. It is idempotent: an existing
// provider is updated in place only when a recorded setting differs (every
// provider PUT makes Bifrost list models on the proxy for each key), and an
// existing key is updated rather than created, because Bifrost masks key
// values and its key names are unique across providers.
func (g *Gateway) Setup(ctx context.Context) error {
	if err := g.ensureProvider(ctx); err != nil {
		return err
	}
	var existing struct {
		Keys []providerKey `json:"keys"`
	}
	if err := g.call(ctx, http.MethodGet, g.providerPath()+"/keys", nil, &existing); err != nil {
		return fmt.Errorf("list %s keys: %w", g.cfg.Provider, err)
	}
	byName := make(map[string]providerKey, len(existing.Keys))
	for _, k := range existing.Keys {
		byName[k.Name] = k
	}
	for _, s := range g.cfg.Slots {
		models := mergeModels(byName[s.Name].Models, g.cfg.Model)
		id, err := g.upsertKey(ctx, s, byName[s.Name].ID, models)
		if err != nil {
			return err
		}
		g.mu.Lock()
		g.keyIDs[s.Name] = id
		g.models[s.Name] = toSet(models)
		g.mu.Unlock()
	}
	g.logger.Info("bifrost gateway set up", "provider", g.cfg.Provider, "slots", len(g.cfg.Slots),
		"base_url", g.cfg.FaultProxyURL, "max_retries", g.cfg.MaxRetries)
	return nil
}

// providerKey is one provider key as Bifrost lists it. The value is masked.
type providerKey struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Models  []string `json:"models"`
	Weight  float64  `json:"weight"`
	Enabled *bool    `json:"enabled"`
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
	StoreRaw bool `json:"store_raw_request_response"`
}

func validateConfig(cfg Config) error {
	var errs []error
	if cfg.URL == "" {
		errs = append(errs, errors.New("url is required"))
	}
	if cfg.SetupToken == "" {
		errs = append(errs, errors.New("setup token is required"))
	}
	if cfg.FaultProxyURL == "" {
		errs = append(errs, errors.New("fault proxy url is required"))
	}
	if cfg.Model == "" {
		errs = append(errs, errors.New("model is required"))
	}
	if cfg.MaxRetries < 0 {
		errs = append(errs, fmt.Errorf("max retries %d is negative", cfg.MaxRetries))
	}
	if len(cfg.Slots) == 0 {
		errs = append(errs, errors.New("at least one key slot is required"))
	}
	seen := map[string]bool{}
	for i, s := range cfg.Slots {
		switch {
		case s.Name == "" || s.Value == "":
			errs = append(errs, fmt.Errorf("slot %d needs a name and a value", i))
		case seen[s.Name]:
			errs = append(errs, fmt.Errorf("slot %s is listed twice", s.Name))
		}
		seen[s.Name] = true
	}
	if len(errs) > 0 {
		return fmt.Errorf("bifrost gateway config: %w", errors.Join(errs...))
	}
	return nil
}

// ensureProvider creates the provider, or updates it when a setting the bench
// owns differs from the wanted value.
func (g *Gateway) ensureProvider(ctx context.Context) error {
	var cur providerConfig
	err := g.call(ctx, http.MethodGet, g.providerPath(), nil, &cur)
	switch {
	case isStatus(err, http.StatusNotFound):
		body := g.providerBody()
		body["provider"] = g.cfg.Provider
		if err := g.call(ctx, http.MethodPost, "/api/providers", body, nil); err != nil {
			return fmt.Errorf("create provider %s: %w", g.cfg.Provider, err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("get provider %s: %w", g.cfg.Provider, err)
	case g.providerMatches(cur):
		return nil
	}
	if err := g.call(ctx, http.MethodPut, g.providerPath(), g.providerBody(), nil); err != nil {
		return fmt.Errorf("update provider %s: %w", g.cfg.Provider, err)
	}
	return nil
}

// providerBody is the provider definition from the spike: the fault proxy as
// base URL (a private address, hence allow_private_network), explicit
// retries with short backoffs, and raw request and response storage, which
// puts the upstream message id and the request bytes into each log row.
func (g *Gateway) providerBody() map[string]any {
	return map[string]any{
		"network_config": map[string]any{
			"base_url":                           g.cfg.FaultProxyURL,
			"allow_private_network":              true,
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
}

func (g *Gateway) providerMatches(cur providerConfig) bool {
	n := cur.NetworkConfig
	return n.BaseURL == g.cfg.FaultProxyURL && n.AllowPrivateNetwork && n.MaxRetries == g.cfg.MaxRetries &&
		n.RetryBackoffInitial == retryBackoffMS && n.RetryBackoffMax == retryBackoffMaxMS &&
		n.RequestTimeoutS == requestTimeoutS && cur.StoreRaw
}

// upsertKey creates the slot's provider key, or updates it in place when id
// is set, and returns the key id. Weight 1 is required for routing.
func (g *Gateway) upsertKey(ctx context.Context, s Slot, id string, models []string) (string, error) {
	body := map[string]any{"name": s.Name, "value": s.Value, "models": models, "weight": 1, "enabled": true}
	var out providerKey
	if id == "" {
		if err := g.call(ctx, http.MethodPost, g.providerPath()+"/keys", body, &out); err != nil {
			return "", fmt.Errorf("create key slot %s: %w", s.Name, err)
		}
	} else if err := g.call(ctx, http.MethodPut, g.providerPath()+"/keys/"+id, body, &out); err != nil {
		return "", fmt.Errorf("update key slot %s: %w", s.Name, err)
	}
	if out.ID == "" {
		out.ID = id
	}
	if out.ID == "" {
		return "", fmt.Errorf("key slot %s: bifrost returned no key id", s.Name)
	}
	return out.ID, nil
}

func (g *Gateway) providerPath() string {
	return "/api/providers/" + g.cfg.Provider
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
