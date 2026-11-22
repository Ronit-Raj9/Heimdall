package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/maximhq/bifrost/bench/graders"
	"github.com/maximhq/bifrost/bench/issuebench"
	"github.com/maximhq/bifrost/bench/issuebench/builder"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Lane G02: issuebench.build runs the environment builder agent for one
// repository version on the org's model through the gateway, in a sandbox
// container on this worker, and returns the BuildResult. The environment is
// cached per repository and version and reused.

const kindIssuebenchBuild = "issuebench.build"

func init() {
	registerJobKind(jobKindSpec{Kind: kindIssuebenchBuild, Local: true, Hosted: true, Validate: validateIssuebenchBuild, Run: runIssuebenchBuild})
}

// issuebenchBuildParams are the params of issuebench.build.
type issuebenchBuildParams struct {
	Repo       string                 `json:"repo"`
	Version    string                 `json:"version"`
	RepoDir    string                 `json:"repo_dir"` // checkout at the base commit, under the project root, cloned without a token in its config
	Candidates []issuebench.Candidate `json:"candidates"`
	Provider   string                 `json:"provider"`
	Model      string                 `json:"model"`
	KeyID      string                 `json:"key_id,omitempty"`
	MaxUSD     float64                `json:"max_usd"`
	Network    string                 `json:"network,omitempty"` // build network; default env BENCH_ISSUEBENCH_NETWORK
	Consent    bool                   `json:"consent"`           // the user ticked "sends your code to your model provider with your key"
	Limits     *builder.Limits        `json:"limits,omitempty"`
}

var issuebenchVersionRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,80}$`)

func (p issuebenchBuildParams) check(root string) (string, error) {
	switch {
	case !p.Consent:
		return "", liveInvalid("consent is required: the builder sends files from this repository to your model provider with your key")
	case p.Repo == "" || !strings.Contains(p.Repo, "/"):
		return "", liveInvalid("repo must be owner/name")
	case !issuebenchVersionRE.MatchString(p.Version):
		return "", liveInvalid("version is required (letters, digits, . _ - only)")
	case len(p.Candidates) == 0:
		return "", liveInvalid("candidates are required: the issues whose tests the environment must run")
	case p.Provider == "" || p.Model == "":
		return "", liveInvalid("provider and model are required")
	case p.MaxUSD <= 0:
		return "", liveInvalid("max_usd is required: the build's dollar cap")
	}
	dir, err := filepath.Abs(filepath.Join(root, p.RepoDir))
	if filepath.IsAbs(p.RepoDir) {
		dir, err = filepath.Clean(p.RepoDir), nil
	}
	rootAbs, _ := filepath.Abs(root)
	if err != nil || p.RepoDir == "" || (dir != rootAbs && !strings.HasPrefix(dir, rootAbs+string(filepath.Separator))) {
		return "", liveInvalid("repo_dir must be a checkout under the project root")
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "", liveInvalid("repo_dir %s is not a directory", p.RepoDir)
	}
	return dir, nil
}

func validateIssuebenchBuild(_ context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
	var p issuebenchBuildParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, "", liveInvalid("params: %v", err)
	}
	if _, err := p.check(env.ProjectRoot); err != nil {
		return nil, "", err
	}
	return p, p.Repo + "@" + p.Version, nil
}

// issuebenchGatewayFor opens the job's gateway and, hosted, registers the
// org's own key for provider with it. expID binds the per-job key to the org.
func issuebenchGatewayFor(ctx context.Context, env jobEnv, provider, keyID string) (gw schemas.Gateway, url, expID string, closeGW func(), err error) {
	gw, url, closeGW, err = calibrateGatewayFor(ctx, env)
	if err != nil {
		return nil, "", "", nil, err
	}
	if env.Hosted {
		cg, ok := gw.(calibrateGateway)
		if !ok {
			closeGW()
			return nil, "", "", nil, errors.New("this server's gateway can not route calls to the org's own key")
		}
		k, err := calibrateOrgKey(ctx, env.Services.Keys, env.OrgID, provider, keyID)
		if err != nil {
			closeGW()
			return nil, "", "", nil, err
		}
		value, err := env.Services.Keys.Reveal(ctx, env.OrgID, k.ID)
		if err != nil || value == "" {
			closeGW()
			return nil, "", "", nil, fmt.Errorf("read key %s: it can not be decrypted; replace it on /models", k.ID)
		}
		cred := schemas.ProviderCredential{KeyID: k.ID, Provider: k.Provider, BaseURL: k.BaseURL, Models: k.Models, Value: value}
		if err := cg.ApplyOrgCredentials(ctx, env.OrgID, []schemas.ProviderCredential{cred}); err != nil {
			closeGW()
			return nil, "", "", nil, fmt.Errorf("register the org key with the gateway: %w", err)
		}
		expID = "issuebench-" + calibrateOrgSafe(env.OrgID)
		cg.BindExperimentOrg(expID, env.OrgID)
	}
	return gw, url, expID, closeGW, nil
}

func issuebenchPrice(env jobEnv) func(string, schemas.Tokens) (float64, bool) {
	if env.App != nil && env.App.prices != nil {
		return graders.PricesFrom(env.App.prices.Lookup)
	}
	return nil
}

// issuebenchAddSpend adds a job's model cost to the org key's month spend.
func issuebenchAddSpend(ctx context.Context, env jobEnv, log jobs.Logger, provider, keyID string, cost float64) {
	if !env.Hosted || cost <= 0 || env.Services.Keys == nil {
		return
	}
	if k, err := calibrateOrgKey(ctx, env.Services.Keys, env.OrgID, provider, keyID); err == nil {
		if err := env.Services.Keys.AddSpend(ctx, env.OrgID, k.ID, cost); err != nil {
			log.Warnf("add $%.4f to key %s's month spend: %v", cost, k.ID, err)
		}
	}
}

// issuebenchModelFor wires the builder's gateway model; tests replace it.
var issuebenchModelFor = func(ctx context.Context, env jobEnv, p issuebenchBuildParams, jobID string) (builder.Model, func(), error) {
	gw, url, expID, closeGW, err := issuebenchGatewayFor(ctx, env, p.Provider, p.KeyID)
	if err != nil {
		return nil, nil, err
	}
	price := issuebenchPrice(env)
	var logger schemas.Logger
	if env.App != nil {
		logger = env.App.logger
	}
	m := builder.NewGatewayModel(builder.GatewayConfig{Gateway: gw, URL: url, Provider: p.Provider, Model: p.Model, Temperature: 0,
		Price: price, KeyName: "r_ib" + issuebenchKeySafe(jobID), ExpID: expID, Logger: logger})
	return m, func() { m.Close(ctx); closeGW() }, nil
}

// issuebenchSandboxFor is the build sandbox; tests replace it.
var issuebenchSandboxFor = func(p issuebenchBuildParams) builder.SandboxFactory {
	network := p.Network
	if network == "" {
		network = os.Getenv("BENCH_ISSUEBENCH_NETWORK")
	}
	return builder.DockerFactory(builder.DockerConfig{Network: network})
}

var issuebenchKeyRE = regexp.MustCompile(`[^a-z0-9]+`)

func issuebenchKeySafe(id string) string {
	return issuebenchKeyRE.ReplaceAllString(strings.ToLower(id), "")
}

func runIssuebenchBuild(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
	var p issuebenchBuildParams
	if err := json.Unmarshal(j.Params, &p); err != nil {
		return nil, liveInvalid("params: %v", err)
	}
	dir, err := p.check(env.ProjectRoot)
	if err != nil {
		return nil, err
	}
	model, closeFn, err := issuebenchModelFor(ctx, env, p, j.ID)
	if err != nil {
		return nil, err
	}
	defer closeFn()
	lim := builder.DefaultLimits()
	if p.Limits != nil {
		lim = *p.Limits
	}
	lim.MaxUSD = p.MaxUSD
	base := filepath.Join(env.ProjectRoot, "issuebench")
	if env.App != nil && env.App.cfg != nil {
		base = env.App.cfg.Path("issuebench")
	}
	b := builder.New(builder.Config{
		Model: model, Sandbox: issuebenchSandboxFor(p), Limits: lim,
		Cache:   &builder.FileCache{Dir: filepath.Join(base, "envs")},
		Log:     func(f string, args ...any) { log.Printf(f, args...) },
		LogPath: filepath.Join(base, "logs", issuebenchKeySafe(j.ID)+".jsonl"),
	})
	log.Printf("building %s at %s on %s/%s (cap $%.2f)", p.Repo, p.Version, p.Provider, p.Model, p.MaxUSD)
	res := b.Build(ctx, builder.Request{Repo: p.Repo, Version: p.Version, RepoDir: dir, Candidates: p.Candidates})
	if progress != nil {
		progress(map[string]any{"stop": res.Stop, "steps": res.Steps, "cost_usd": res.CostUSD})
	}
	if res.Stop != issuebench.StopOK {
		log.Warnf("build stopped: %s: %s", res.Stop, res.Detail)
	}
	issuebenchAddSpend(ctx, env, log, p.Provider, p.KeyID, res.CostUSD)
	return res, nil
}
