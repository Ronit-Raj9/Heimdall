package main

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/maximhq/bifrost/bench/issuebench"
	"github.com/maximhq/bifrost/bench/issuebench/validate"
	"github.com/maximhq/bifrost/bench/jobs"
)

// Lane H04: issuebench.check runs the G04 model checks on one validated
// candidate (leak, clarity, interface note, near-miss wrong patches) on the
// org's model through the gateway, on a per-job key with a dollar cap. It is
// a job like issuebench.build so it gets the same gateway, key and spend
// accounting; the validate stage queues it once the tests passed.

const kindIssuebenchCheck = "issuebench.check"

// issuebenchCheckFallbackUSD prices a call the price snapshot does not know,
// so the cap still holds.
const issuebenchCheckFallbackUSD = 0.05

func init() {
	registerJobKind(jobKindSpec{Kind: kindIssuebenchCheck, Local: true, Hosted: true, Validate: validateIssuebenchCheck, Run: runIssuebenchCheck})
}

// issuebenchCheckParams are the params of issuebench.check.
type issuebenchCheckParams struct {
	Candidate       issuebench.Candidate `json:"candidate"`
	KeepErrorToPass bool                 `json:"keep_error_to_pass,omitempty"` // ask for the interface note too
	Provider        string               `json:"provider"`
	Model           string               `json:"model"`
	KeyID           string               `json:"key_id,omitempty"`
	MaxUSD          float64              `json:"max_usd"`
	Consent         bool                 `json:"consent"`
	// NoAblations: the hunk ablations were verified on the worker and come
	// from the Validation (hosted); the check adds no unverified ones.
	NoAblations bool `json:"no_ablations,omitempty"`
}

// issuebenchCheckResult is the job's result.
type issuebenchCheckResult struct {
	Checks  issuebench.LLMChecks `json:"checks"`
	Stop    string               `json:"stop,omitempty"` // "" keeps the task
	CostUSD float64              `json:"cost_usd"`
	Calls   int                  `json:"calls"`
}

func (p issuebenchCheckParams) check() error {
	switch {
	case !p.Consent:
		return liveInvalid("consent is required: the checks send the issue and the fix to your model provider with your key")
	case p.Candidate.Repo == "" || p.Candidate.FixPatch == "":
		return liveInvalid("candidate needs repo and fix_patch")
	case p.Provider == "" || p.Model == "":
		return liveInvalid("provider and model are required")
	case p.MaxUSD <= 0:
		return liveInvalid("max_usd is required: the checks' dollar cap")
	}
	return nil
}

func validateIssuebenchCheck(_ context.Context, _ jobEnv, raw json.RawMessage) (any, string, error) {
	var p issuebenchCheckParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, "", liveInvalid("params: %v", err)
	}
	if err := p.check(); err != nil {
		return nil, "", err
	}
	return p, p.Candidate.Repo + "#" + strconv.Itoa(p.Candidate.Issue), nil
}

// issuebenchCheckModel is the spend-tracked model behind the checks.
type issuebenchCheckModel interface {
	validate.Model
	Spent() (float64, int)
}

// issuebenchCheckModelFor wires the checks' gateway model; tests replace it.
var issuebenchCheckModelFor = func(ctx context.Context, env jobEnv, p issuebenchCheckParams, jobID string) (issuebenchCheckModel, func(), error) {
	gw, url, expID, closeGW, err := issuebenchGatewayFor(ctx, env, p.Provider, p.KeyID)
	if err != nil {
		return nil, nil, err
	}
	m := &validate.GatewayModel{Gateway: gw, URL: url, Provider: p.Provider, Model: p.Model,
		JobID: "r_ibc" + issuebenchKeySafe(jobID), ExpID: expID, Price: issuebenchPrice(env),
		FallbackUSD: issuebenchCheckFallbackUSD, BudgetUSD: p.MaxUSD}
	return m, func() { _ = m.Close(ctx); closeGW() }, nil
}

func runIssuebenchCheck(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
	var p issuebenchCheckParams
	if err := json.Unmarshal(j.Params, &p); err != nil {
		return nil, liveInvalid("params: %v", err)
	}
	if err := p.check(); err != nil {
		return nil, err
	}
	model, closeFn, err := issuebenchCheckModelFor(ctx, env, p, j.ID)
	if err != nil {
		return nil, err
	}
	defer closeFn()
	log.Printf("checking issue #%d of %s on %s/%s (cap $%.2f)", p.Candidate.Issue, p.Candidate.Repo, p.Provider, p.Model, p.MaxUSD)
	checks, stop, err := (&validate.Checker{Model: model, NoAblations: p.NoAblations}).Run(ctx, p.Candidate, p.KeepErrorToPass)
	if err != nil {
		return nil, err
	}
	cost, calls := model.Spent()
	if progress != nil {
		progress(map[string]any{"stop": stop, "calls": calls, "cost_usd": cost})
	}
	if stop != "" {
		log.Warnf("checks stopped the task: %s", stop)
	}
	issuebenchAddSpend(ctx, env, log, p.Provider, p.KeyID, cost)
	return issuebenchCheckResult{Checks: checks, Stop: stop, CostUSD: cost, Calls: calls}, nil
}
