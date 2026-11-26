package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/calibration"
	"github.com/maximhq/bifrost/bench/faultproxy"
	"github.com/maximhq/bifrost/bench/graders"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/schemas"
)

// calibrate.run (gap lane G06): judge calibration from the website, in
// process, in both modes. It replaces L02's CLI arg builder (that one was
// local only). Judges call a model and nothing else, so no container and no
// runner is needed.
//
// Hosted: the judge's calls go through the server's Bifrost gateway with
// the org's own provider key from the key store (registered as org slots
// with ApplyOrgCredentials, the key bound to a per-org calibration
// experiment so MintKey's runProvider picks those slots), never the
// server's environment keys. The caller must confirm the cost (yes: true),
// the forecast must fit a hard cap on calls and USD, the transport stops at
// the cap even if the forecast was wrong, and the spend is added to the
// key's month spend.
//
// Local: the gateway with the operator's environment keys, exactly like
// `judge calibrate -yes`; the caps apply only when max_usd is set.
func init() {
	registerJobKind(jobKindSpec{Kind: kindCalibrateRun, Local: true, Hosted: true,
		Opts:     []jobs.KindOption{jobs.WithLimit(4, 1)},
		Validate: validateCalibrateRun, Run: runCalibrateJob})
}

const kindCalibrateRun = "calibrate.run"

// Hosted caps: a calibration that would need more is refused before any
// call; max_usd may only lower the USD cap. Vars so tests can lower them.
var (
	hostedCalibrateMaxCalls = 400
	hostedCalibrateMaxUSD   = 5.0
)

const calibrateMaxSample = 500

// calibrateParams are calibrate.run's params.
type calibrateParams struct {
	Suite        string   `json:"suite"`
	Yes          bool     `json:"yes"`      // confirms the judge calls cost money
	Estimate     bool     `json:"estimate"` // forecast only: no call, yes not needed
	Provider     string   `json:"provider,omitempty"`
	Model        string   `json:"model,omitempty"`
	KeyID        string   `json:"key_id,omitempty"`
	Sample       int      `json:"sample,omitempty"`
	MaxUSD       float64  `json:"max_usd,omitempty"`
	SeedControls bool     `json:"seed_controls,omitempty"`
	FromReviews  []string `json:"from_reviews_exps,omitempty"`
	Policy       string   `json:"policy,omitempty"`
}

// calibrateResult is the job result the Calibration tab shows.
type calibrateResult struct {
	Suite       string   `json:"suite"`
	Estimate    bool     `json:"estimate"`
	Provider    string   `json:"provider"`
	Model       string   `json:"model"`
	Items       int      `json:"items"`
	Calls       int      `json:"calls"`
	ForecastUSD *float64 `json:"forecast_usd,omitempty"`
	PriceNote   string   `json:"price_note,omitempty"`
	CapCalls    int      `json:"cap_calls,omitempty"`
	CapUSD      float64  `json:"cap_usd,omitempty"`
	KeyID       string   `json:"key_id,omitempty"`
	SpentUSD    float64  `json:"spent_usd"`

	RecordID    string                    `json:"record_id,omitempty"`
	HumanLabels int                       `json:"human_labels,omitempty"`
	SeedItems   int                       `json:"seed_items,omitempty"`
	Human       *schemas.CalibrationStats `json:"human,omitempty"`
	Seed        *schemas.CalibrationStats `json:"seed,omitempty"`
}

var calibrateNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@-]{0,127}$`)

func parseCalibrateParams(raw json.RawMessage) (calibrateParams, error) {
	var p calibrateParams
	if err := decodeParams(raw, &p); err != nil {
		return p, err
	}
	if err := checkID("suite", p.Suite, true); err != nil {
		return p, err
	}
	if !p.Yes && !p.Estimate {
		return p, errors.New("judge calibration calls the judge model and costs money: set yes to true to confirm (or estimate to true for the forecast only)")
	}
	for f, v := range map[string]string{"provider": p.Provider, "model": p.Model} {
		if v != "" && (!calibrateNamePattern.MatchString(v) || strings.Contains(v, "..")) {
			return p, fmt.Errorf("%s %q is not a valid name", f, v)
		}
	}
	if err := checkID("key_id", p.KeyID, false); err != nil {
		return p, err
	}
	if p.Sample < 0 || p.Sample > calibrateMaxSample {
		return p, fmt.Errorf("sample must be between 0 (all labels) and %d", calibrateMaxSample)
	}
	if p.MaxUSD < 0 || math.IsNaN(p.MaxUSD) || math.IsInf(p.MaxUSD, 0) {
		return p, errors.New("max_usd must be 0 (default cap) or a positive amount")
	}
	if err := checkIDList("from_reviews_exps", p.FromReviews); err != nil {
		return p, err
	}
	pol, err := checkRelPath("policy", p.Policy, false)
	if err != nil {
		return p, err
	}
	p.Policy = pol
	return p, nil
}

func validateCalibrateRun(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
	p, err := parseCalibrateParams(raw)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", jobs.ErrInvalidParams, err)
	}
	if calibrateHasNoLabels(ctx, env, p) {
		return nil, "", fmt.Errorf("%w: %s", jobs.ErrInvalidParams, errCalibrateNoLabels)
	}
	return p, p.Suite, nil
}

// errCalibrateNoLabels refuses a check with nothing to check, in the web
// app's words (fix wave 3, N286); the CLI keeps its own message.
const errCalibrateNoLabels = "This benchmark has no labelled answers yet. Add labels on the Grader accuracy tab first."

// calibrateHasNoLabels reports whether the check would have no labelled
// answer at all: none stored, and neither the benchmark's own answers
// (seed_controls) nor reviews asked for. A suite that cannot be read is
// left to the job, which says why.
func calibrateHasNoLabels(ctx context.Context, env jobEnv, p calibrateParams) bool {
	if p.SeedControls || len(p.FromReviews) > 0 || env.App == nil {
		return false
	}
	s, err := calibrateLoadSuite(ctx, env.App, p.Suite)
	if err != nil || s == nil {
		return false
	}
	set, err := calibration.LoadSet(s.Root)
	if err != nil {
		return false
	}
	human, seed, _ := calibration.Resolve(set.Items)
	return len(human)+len(seed) == 0
}

// calibrateLoadSuite loads the suite to calibrate; a var for tests.
var calibrateLoadSuite = func(ctx context.Context, a *app, id string) (*schemas.Suite, error) {
	return (judgeSvc{a: a}).load(ctx, id)
}

// calibrateGateway is the gateway a hosted calibration needs: mint and
// revoke the judge key, and route it to the org's own provider keys.
type calibrateGateway interface {
	schemas.Gateway
	ApplyOrgCredentials(ctx context.Context, orgID string, creds []schemas.ProviderCredential) error
	BindExperimentOrg(expID, orgID string)
}

// calibrateGatewayFor builds the server's gateway client (the URL is its
// inference base URL) and a close func. A var so tests swap in a fake.
var calibrateGatewayFor = func(ctx context.Context, env jobEnv) (schemas.Gateway, string, func(), error) {
	if env.App == nil || env.App.cfg == nil {
		return nil, "", nil, errors.New("no project config on this server")
	}
	cfg := env.App.cfg
	if cfg.Gateway.URL == "" {
		return nil, "", nil, errors.New("this server has no Bifrost gateway configured (bench.yaml gateway.url); ask the operator to set it")
	}
	fp := faultproxy.NewClient(cfg.FaultProxy.ControlURL, faultproxy.WithToken(os.Getenv("BENCH_FAULTPROXY_TOKEN")))
	gw, err := newGateway(ctx, cfg, env.App.docker, env.App.logger, fp)
	if err != nil {
		return nil, "", nil, err
	}
	closeFn := func() {
		if c, ok := gw.(interface{ Close(context.Context) error }); ok {
			_ = c.Close(context.Background())
		}
	}
	return gw, cfg.Gateway.URL, closeFn, nil
}

// judgeKeyProvider maps a key store provider to the provider name a judge
// spec and the gateway use (as gateways/bifrost bifrostProvider).
func judgeKeyProvider(p string) string {
	switch p = strings.ToLower(strings.TrimSpace(p)); p {
	case "openai-compatible":
		return "openai"
	case "google":
		return "gemini"
	}
	return p
}

// calibrateOrgKey picks the org's key for the judge provider: key_id when
// given (it must be that provider's), else the first usable one.
func calibrateOrgKey(ctx context.Context, ks keystore.Store, orgID, provider, keyID string) (*keystore.Key, error) {
	if ks == nil {
		return nil, errors.New("this server has no key store; hosted calibration needs the org's own model key")
	}
	keys, err := ks.List(ctx, orgID, keystore.KindProvider)
	if err != nil {
		return nil, fmt.Errorf("list the org's model keys: %w", err)
	}
	want := judgeKeyProvider(provider)
	for i := range keys {
		k := keys[i]
		if keyID != "" && k.ID != keyID {
			continue
		}
		if judgeKeyProvider(k.Provider) != want {
			if keyID != "" {
				return nil, fmt.Errorf("key %s is a %s key, the judge provider is %s", k.ID, k.Provider, provider)
			}
			continue
		}
		switch strings.ToLower(k.Status) {
		case "disabled", "failed", "invalid", "revoked":
			if keyID != "" {
				return nil, fmt.Errorf("key %s is %s: test or replace it on /models", k.ID, k.Status)
			}
			continue
		}
		if k.BudgetUSD > 0 && k.MonthSpentUSD >= k.BudgetUSD {
			if keyID != "" {
				return nil, fmt.Errorf("key %s spent its month budget ($%.2f): raise it on /models", k.ID, k.BudgetUSD)
			}
			continue
		}
		return &k, nil
	}
	if keyID != "" {
		return nil, fmt.Errorf("key %s not found in this org", keyID)
	}
	return nil, fmt.Errorf("the org has no usable %s key for the judge: add a model key at /models?add=%s", provider, provider)
}

// spendMeter counts the judge's calls and priced spend; its transport
// refuses a call past the caps, so a wrong forecast can not overspend.
type spendMeter struct {
	mu       sync.Mutex
	calls    int
	usd      float64
	maxCalls int     // 0 = no cap
	maxUSD   float64 // 0 = no cap
	base     http.RoundTripper
}

func (m *spendMeter) RoundTrip(r *http.Request) (*http.Response, error) {
	m.mu.Lock()
	if m.maxCalls > 0 && m.calls >= m.maxCalls {
		m.mu.Unlock()
		return nil, fmt.Errorf("calibration cap reached: %d judge calls", m.maxCalls)
	}
	if m.maxUSD > 0 && m.usd >= m.maxUSD {
		m.mu.Unlock()
		return nil, fmt.Errorf("calibration cap reached: $%.4f of $%.2f", m.usd, m.maxUSD)
	}
	m.calls++
	m.mu.Unlock()
	return m.base.RoundTrip(r)
}

func (m *spendMeter) price(p func(string, schemas.Tokens) (float64, bool)) func(string, schemas.Tokens) (float64, bool) {
	if p == nil {
		return nil
	}
	return func(model string, tok schemas.Tokens) (float64, bool) {
		c, ok := p(model, tok)
		m.mu.Lock()
		m.usd += c
		m.mu.Unlock()
		return c, ok
	}
}

func (m *spendMeter) spent() (int, float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls, m.usd
}

func runCalibrateJob(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, _ func(any)) (any, error) {
	p, err := parseCalibrateParams(j.Params)
	if err != nil {
		return nil, err
	}
	if env.App == nil {
		return nil, errors.New("no project config on this server")
	}
	a := env.App
	s, err := calibrateLoadSuite(ctx, a, p.Suite)
	if err != nil {
		return nil, err
	}
	policy := ""
	if p.Policy != "" {
		policy = filepath.Join(env.ProjectRoot, filepath.FromSlash(p.Policy))
	}
	th, err := thresholdsFrom(policy)
	if err != nil {
		return nil, err
	}
	if !p.Estimate {
		if err := calibrateSourceLabels(ctx, a, s, p, log); err != nil {
			return nil, err
		}
	}
	if p.Estimate && p.SeedControls {
		// An estimate saves nothing: count the weak seed labels from a copy.
		shadow, cleanup, err := calibrateSeedShadow(s)
		if err != nil {
			return nil, err
		}
		defer cleanup()
		s = shadow
	}
	if set, err := calibration.LoadSet(s.Root); err == nil {
		if human, seed, _ := calibration.Resolve(set.Items); len(human)+len(seed) == 0 {
			return nil, errors.New(calibrateNoLabelsWeb)
		}
	}

	res := &calibrateResult{Suite: s.ID, Estimate: p.Estimate}
	meter := &spendMeter{base: http.DefaultTransport}
	if p.MaxUSD > 0 {
		meter.maxUSD = p.MaxUSD
	}
	var orgKey *keystore.Key
	opts := calibrateOptions{yes: p.Yes, thresh: th, sample: p.Sample, forecastOnly: p.Estimate}
	if p.Provider != "" || p.Model != "" {
		opts.judge = &schemas.JudgeSpec{Provider: p.Provider, Model: p.Model}
	}
	if env.Hosted {
		meter.maxCalls = hostedCalibrateMaxCalls
		if meter.maxUSD == 0 || meter.maxUSD > hostedCalibrateMaxUSD {
			meter.maxUSD = hostedCalibrateMaxUSD
		}
	}
	res.CapCalls, res.CapUSD = meter.maxCalls, meter.maxUSD
	opts.checkPlan = func(plan *graders.CalibrationPlan) error {
		res.Provider, res.Model, res.Items, res.Calls = plan.Spec.Provider, plan.Spec.Model, len(plan.Items), plan.Calls
		if plan.Priced {
			usd := plan.USD
			res.ForecastUSD = &usd
		} else {
			res.PriceNote = plan.PriceNote
		}
		if env.Hosted {
			k, err := calibrateOrgKey(ctx, env.Services.Keys, env.OrgID, plan.Spec.Provider, p.KeyID)
			if err != nil {
				return err
			}
			orgKey, res.KeyID = k, k.ID
			if !plan.Priced {
				return fmt.Errorf("the judge cost can not be forecast (%s), so the hosted cap can not be checked; pick a model in the price snapshot", plan.PriceNote)
			}
		}
		if meter.maxCalls > 0 && plan.Calls > meter.maxCalls {
			return fmt.Errorf("forecast %d judge calls is over the cap of %d: lower the sample size", plan.Calls, meter.maxCalls)
		}
		if meter.maxUSD > 0 && plan.Priced && plan.USD > meter.maxUSD {
			return fmt.Errorf("forecast $%.4f is over the cap of $%.2f: lower the sample size", plan.USD, meter.maxUSD)
		}
		if orgKey != nil && orgKey.BudgetUSD > 0 && orgKey.MonthSpentUSD+plan.USD > orgKey.BudgetUSD {
			return fmt.Errorf("forecast $%.4f would pass key %s's month budget ($%.2f spent of $%.2f): raise it on /models or lower the sample size",
				plan.USD, orgKey.ID, orgKey.MonthSpentUSD, orgKey.BudgetUSD)
		}
		return nil
	}
	var closeGW func()
	defer func() {
		if closeGW != nil {
			closeGW()
		}
	}()
	expID := ""
	opts.judgeCfg = func() (graders.JudgeConfig, error) {
		gw, url, closeFn, err := calibrateGatewayFor(ctx, env)
		if err != nil {
			return graders.JudgeConfig{}, err
		}
		closeGW = closeFn
		if env.Hosted {
			cg, ok := gw.(calibrateGateway)
			if !ok || orgKey == nil {
				return graders.JudgeConfig{}, errors.New("this server's gateway can not route calls to the org's own key")
			}
			value, err := env.Services.Keys.Reveal(ctx, env.OrgID, orgKey.ID)
			if err != nil || value == "" {
				return graders.JudgeConfig{}, fmt.Errorf("read key %s: it can not be decrypted; replace it on /models", orgKey.ID)
			}
			cred := schemas.ProviderCredential{KeyID: orgKey.ID, Provider: orgKey.Provider, BaseURL: orgKey.BaseURL, Models: orgKey.Models, Value: value}
			if err := cg.ApplyOrgCredentials(ctx, env.OrgID, []schemas.ProviderCredential{cred}); err != nil {
				return graders.JudgeConfig{}, fmt.Errorf("register the org key with the gateway: %w", err)
			}
			expID = "calibration-" + calibrateOrgSafe(env.OrgID)
			cg.BindExperimentOrg(expID, env.OrgID)
		}
		var price func(string, schemas.Tokens) (float64, bool)
		ref := ""
		if a.prices != nil {
			price, ref = graders.PricesFrom(a.prices.Lookup), a.prices.Ref()
		}
		return graders.JudgeConfig{Gateway: gw, URL: url, HTTP: &http.Client{Timeout: 2 * time.Minute, Transport: meter},
			Price: meter.price(price), PriceRef: ref, Logger: a.logger, CalibrationExpID: expID}, nil
	}

	rec, runErr := calibrateSuite(ctx, a, s, opts, nil, log)
	calls, usd := meter.spent()
	res.SpentUSD = usd
	if orgKey != nil && usd > 0 {
		if err := env.Services.Keys.AddSpend(ctx, env.OrgID, orgKey.ID, usd); err != nil {
			log.Warnf("add $%.4f to key %s's month spend: %v", usd, orgKey.ID, err)
		} else {
			log.Printf("$%.4f added to key %s's month spend (%d call(s))", usd, orgKey.ID, calls)
		}
	}
	if runErr != nil {
		return nil, runErr
	}
	if rec != nil {
		res.RecordID, res.HumanLabels, res.SeedItems = rec.RecordID, rec.HumanLabels, rec.SeedItems
		h := rec.Human
		res.Human, res.Seed = &h, rec.Seed
	}
	return res, nil
}

// calibrateNoLabelsWeb is the web job's answer for a suite with no labels
// (the command line keeps its own text, which names its commands).
const calibrateNoLabelsWeb = "This benchmark has no labels yet. Add labels on this page, or tick the box to use the benchmark's expected and wrong answers."

// calibrateSeedShadow is s with its labels copied to a temporary folder and
// the weak seed labels added there, so an estimate counts them without
// writing them into the suite. cleanup removes the folder.
func calibrateSeedShadow(s *schemas.Suite) (*schemas.Suite, func(), error) {
	tmp, err := os.MkdirTemp("", "calibrate-estimate-")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	set, err := calibration.LoadSet(s.Root)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	if len(set.Raw) > 0 {
		dst := calibration.LabelsPath(tmp)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			cleanup()
			return nil, nil, err
		}
		if err := os.WriteFile(dst, set.Raw, 0o644); err != nil {
			cleanup()
			return nil, nil, err
		}
	}
	if _, err := calibration.Append(tmp, seedLabels(s)); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("seed labels: %w", err)
	}
	cp := *s
	cp.Root = tmp
	return &cp, cleanup, nil
}

// calibrateOrgSafe keeps an experiment id plain.
func calibrateOrgSafe(org string) string {
	return orgSafePattern.ReplaceAllString(strings.ToLower(org), "")
}

var orgSafePattern = regexp.MustCompile(`[^a-z0-9]+`)

// calibrateSourceLabels adds the review and seed labels the job asked for
// (the job form of addSourceLabels).
func calibrateSourceLabels(ctx context.Context, a *app, s *schemas.Suite, p calibrateParams, log jobs.Logger) error {
	for _, e := range p.FromReviews {
		items, skipped, err := reviewLabels(ctx, a.store, e, s)
		if err != nil {
			return fmt.Errorf("labels from reviews of %s: %w", e, err)
		}
		n, err := calibration.Append(s.Root, items)
		if err != nil {
			return fmt.Errorf("labels from reviews of %s: %w", e, err)
		}
		log.Printf("experiment %s: %d reviewed run(s) labelled, %d new label row(s)", e, len(items), n)
		for _, sk := range skipped {
			log.Printf("  skipped: %s", sk)
		}
	}
	if p.SeedControls {
		n, err := calibration.Append(s.Root, seedLabels(s))
		if err != nil {
			return fmt.Errorf("seed labels: %w", err)
		}
		log.Printf("admission controls: %d new seed row(s) (weak: never counted as human labels)", n)
	}
	return nil
}
