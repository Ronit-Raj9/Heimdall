package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/issuebench"
	"github.com/maximhq/bifrost/bench/issuebench/pipeline"
	"github.com/maximhq/bifrost/bench/issuebench/work"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Lane W06 (audit/ISSUEWORKER_CONTRACT.md): issuebench.work is the hosted
// job that builds and tests one issue on a worker. The server never runs
// Docker for it. It mints one Bifrost virtual key on its own gateway with
// the org's key (build mode), reads the GitHub token only for a private
// repository, and queues a runner job lease on the org's coordinator whose
// snapshot is one file (issuebench-job/request.json) and whose secrets ride
// in JobLease.Secrets. When the job ends the key is revoked and the cost
// the worker reported is added to the key's month spend.

func init() {
	registerJobKind(jobKindSpec{Kind: issuebench.WorkJobKind, Hosted: true, Opts: []jobs.KindOption{jobs.WithMaxAttempts(1)},
		Validate: validateIssuebenchWork, Run: runIssuebenchWork})
	handlers.IssueBenchReadiness = issuebenchReadiness
}

// ibRepoPublic tells whether a repository is fetchable without a token;
// tests replace it.
var ibRepoPublic = work.RepoPublic

// ibRevokeWait bounds the key revoke after the job, also on cancel.
const ibRevokeWait = 30 * time.Second

// errIssuebenchPrivate is the error of a private (or missing) repository
// when the org has no GitHub token.
var errIssuebenchPrivate = errors.New("this repository is private or does not exist, and GitHub is not connected for this organization: an admin connects it in Settings > GitHub")

// errIssuebenchNoGateway is the readiness message of a server without a
// model gateway.
const errIssuebenchNoGateway = "this server has no model gateway, so environments can not be built: the operator starts the shared-runner profile (docs/hosting.md)"

// decodeIssuebenchWork reads and checks the params of one job. The org is
// always the job's own; JobID is checked as a placeholder when unset.
func decodeIssuebenchWork(raw json.RawMessage, orgID string) (issuebench.WorkParams, error) {
	var p issuebench.WorkParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, liveInvalid("params: %v", err)
	}
	if !p.Consent {
		return p, liveInvalid("consent is required: the build sends files from this repository to your model provider with your key")
	}
	p.Request.OrgID = orgID
	r := p.Request
	if r.JobID == "" {
		r.JobID = "pending"
	}
	if err := r.Check(); err != nil {
		return p, liveInvalid("%v", err)
	}
	return p, nil
}

func validateIssuebenchWork(_ context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
	p, err := decodeIssuebenchWork(raw, env.OrgID)
	if err != nil {
		return nil, "", err
	}
	p.Request.JobID = "" // set from the job id at run time
	return p, p.Request.Repo + "#" + strconv.Itoa(p.Request.Candidate.Issue), nil
}

// issuebenchWorkZip is the lease snapshot: the request file only.
func issuebenchWorkZip(req issuebench.WorkRequest) ([]byte, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if len(body) > issuebench.MaxWorkRequestBytes {
		return nil, fmt.Errorf("the work request of %d bytes passes the %d byte cap", len(body), issuebench.MaxWorkRequestBytes)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(issuebench.WorkRequestFile)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(body); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func runIssuebenchWork(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
	p, err := decodeIssuebenchWork(j.Params, env.OrgID)
	if err != nil {
		return nil, err
	}
	req := p.Request
	req.JobID = j.ID
	if err := req.Check(); err != nil {
		return nil, liveInvalid("%v", err)
	}
	c := jobCoordinator(env)
	if c == nil || !c.HasJobRunnerFor(issuebench.WorkJobKind) {
		return nil, errNoJobRunner
	}
	secrets := map[string]string{}
	redact := func(s string) string { return issuebench.RedactValues(s, secrets) }

	// Repository access: a public repository gets no token.
	if !ibRepoPublic(ctx, work.ExecGit, req.Repo) {
		tok, _, err := auth.FindRepoToken(ctx, env.Services.Keys, env.OrgID)
		switch {
		case errors.Is(err, keystore.ErrNotFound) || (err == nil && strings.TrimSpace(tok) == ""):
			return nil, errIssuebenchPrivate
		case err != nil:
			return nil, fmt.Errorf("read the organization's GitHub token: %w", err)
		}
		req.Private = true
		secrets[issuebench.SecretGitHubToken] = tok
	} else {
		req.Private = false
	}

	// Build mode: one virtual key on the server's gateway, revoked at the end.
	if req.Mode == issuebench.WorkModeBuild {
		gw, url, expID, closeGW, err := issuebenchGatewayFor(ctx, env, req.Provider, p.KeyID)
		if err != nil {
			return nil, err
		}
		defer closeGW()
		key, err := gw.MintKey(ctx, &schemas.RunContext{
			Run:   &schemas.Run{RunID: "r_ibw" + issuebenchKeySafe(j.ID), ExpID: expID},
			Agent: &schemas.AgentConfig{ID: "issuebench-builder", Model: req.Model, Gateway: schemas.GatewayPolicy{Provider: req.Provider}},
		})
		if err != nil {
			return nil, fmt.Errorf("mint the build's gateway key: %s", redact(err.Error()))
		}
		defer func() {
			rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ibRevokeWait)
			defer cancel()
			if err := gw.RevokeKey(rctx, key); err != nil {
				log.Warnf("revoke the build's gateway key: %s", redact(err.Error()))
			}
		}()
		secrets[issuebench.SecretGatewayKey] = key.Value
		secrets[issuebench.SecretGatewayURL] = url
		if env.App != nil && env.App.cfg != nil && env.App.cfg.Gateway.PublicURL != "" {
			secrets[issuebench.SecretGatewayPublicURL] = env.App.cfg.Gateway.PublicURL
		}
	}

	files, err := issuebenchWorkZip(req)
	if err != nil {
		return nil, err
	}
	log.Printf("queued for a worker: %s issue #%d of %s (version %s)", req.Mode, req.Candidate.Issue, req.Repo, req.Version)
	out, err := c.RunJob(ctx, schemas.JobLease{JobID: j.ID, JobKind: issuebench.WorkJobKind, Argv: issuebench.WorkArgv(), Files: files, Secrets: secrets},
		func(line string) { log.Printf("%s", redact(line)) })
	if errors.Is(err, engine.ErrNoRunner) {
		return nil, errNoJobRunner
	}
	if err != nil {
		return nil, errors.New(redact(err.Error()))
	}
	if out.Archive != "" {
		defer os.Remove(out.Archive)
	}
	var res issuebench.WorkResult
	var haveResult bool
	if out.Archive != "" {
		raw, extra, err := engine.ReadJobResult(out.Archive, runnerJobResultFileCap)
		if err != nil {
			return nil, fmt.Errorf("read the worker's result: %s", redact(err.Error()))
		}
		if len(extra) > 0 {
			log.Warnf("the worker sent %d files this job does not take; they were dropped", len(extra))
		}
		if len(raw) > issuebench.MaxWorkResultBytes {
			return nil, fmt.Errorf("the worker's result of %d bytes passes the %d byte cap", len(raw), issuebench.MaxWorkResultBytes)
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &res); err != nil {
				return nil, fmt.Errorf("the worker's result is not a work result: %s", redact(err.Error()))
			}
			haveResult = true
		}
	}
	res.Worker = out.Worker
	if haveResult {
		issuebenchAddSpend(ctx, env, log, req.Provider, p.KeyID, res.CostUSD)
	}
	if out.Error != "" {
		return nil, errors.New(redact(out.Error))
	}
	if !haveResult {
		return nil, fmt.Errorf("worker %s finished the job without a result", out.Worker)
	}
	if res.Mode == "" {
		res.Mode = req.Mode
	}
	log.Printf("worker %s finished the job in %.0f s, $%.4f", out.Worker, res.Seconds, res.CostUSD)
	if progress != nil {
		progress(map[string]any{"stop": res.Stop, "cost_usd": res.CostUSD, "mode": res.Mode})
	}
	return res, nil
}

// issuebenchReadiness is GET /api/bench/issuebench/readiness: whether the
// From issues builds can run here now, with the first missing piece in
// plain words.
func issuebenchReadiness(ctx context.Context, env handlers.OrgEnv) issuebench.Readiness {
	mc := ibModel(pipeline.ModelChoice{})
	r := issuebench.Readiness{
		Hosted:       env.Services.Hosted,
		Gateway:      env.Cfg != nil && strings.TrimSpace(env.Cfg.Gateway.URL) != "",
		DefaultModel: mc.Provider != "" && mc.Model != "",
	}
	if _, err := auth.RepoKey(ctx, env.Services.Keys, env.Services.OrgID); err == nil {
		r.GitHub = true
	}
	if !r.Hosted {
		r.WorkerOnline = true
		return r
	}
	if bl := baseLauncher(env.Services.Launcher); bl != nil && bl.coord != nil {
		r.WorkerOnline = bl.coord.HasJobRunnerFor(issuebench.WorkJobKind)
	}
	switch {
	case !r.WorkerOnline:
		r.Message = errNoJobRunner.Error()
	case !r.Gateway:
		r.Message = errIssuebenchNoGateway
	}
	return r
}
