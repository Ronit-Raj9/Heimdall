// Package work runs one issuebench.work job on a worker: check out the
// issue's base commit, build the environment with the builder agent (build
// mode), build the environment image, and run the candidate's tests in it
// with no network. It is pure Go over seams: the caller (the runner's job
// executor) wires Docker under the org's policy, the checkout and the build
// sandbox. Secrets are only held in memory and never reach argv or a file.
package work

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/issuebench"
	"github.com/maximhq/bifrost/bench/issuebench/builder"
	_ "github.com/maximhq/bifrost/bench/issuebench/lang/python" // registers the languages a worker builds
	"github.com/maximhq/bifrost/bench/schemas"
)

// Deps are the seams one job runs on.
type Deps struct {
	Docker           issuebench.DockerFunc
	Checkout         func(ctx context.Context, repo, sha, dir, token string) error                                 // W03 Checkouter.Checkout
	Sandbox          func(ctx context.Context, req issuebench.WorkRequest) (builder.SandboxFactory, func(), error) // W04 via W05
	Price            func(model string, tok schemas.Tokens) (float64, bool)
	Scratch          string // the job's folder; the caller removes it
	Runtime          string // OCI runtime for test containers; "" leaves the default (the shared policy adds runsc)
	UsePublicGateway bool   // an org's own worker: SecretGatewayPublicURL, else SecretGatewayURL
	Log              func(format string, args ...any)
}

// ErrNoGateway is the error of a build job on a worker that has no URL for
// the server's model gateway.
var ErrNoGateway = errors.New("this worker can not reach the server's model gateway: the operator sets BENCH_GATEWAY_PUBLIC_URL")

var keySafeRE = regexp.MustCompile(`[^a-z0-9]+`)

func safe(id string) string { return keySafeRE.ReplaceAllString(strings.ToLower(id), "") }

// Run runs one job. A build that stops (a blocker, the budget, a base image
// Heimdall does not allow) is a result with Stop set, not an error. An error
// means the job itself could not run (a bad request, the checkout, docker).
// Error texts never hold a secret value.
func Run(ctx context.Context, d Deps, req issuebench.WorkRequest, secrets map[string]string) (res issuebench.WorkResult, err error) {
	start := time.Now()
	res = issuebench.WorkResult{Mode: req.Mode}
	defer func() {
		res.Seconds = time.Since(start).Seconds()
		if err != nil {
			err = errors.New(issuebench.RedactValues(err.Error(), secrets))
		}
		res.Stop = issuebench.RedactValues(res.Stop, secrets)
	}()
	if err := req.Check(); err != nil {
		return res, fmt.Errorf("the work request is not valid: %w", err)
	}
	if d.Docker == nil || d.Checkout == nil || d.Scratch == "" {
		return res, errors.New("this worker is not set up for issue jobs (no docker, checkout or scratch folder)")
	}
	logf := d.Log
	if logf == nil {
		logf = func(string, ...any) {}
	}
	ctx, cancel := context.WithTimeout(ctx, issuebench.MaxWorkWallSeconds*time.Second)
	defer cancel()

	token := ""
	if req.Private {
		if token = secrets[issuebench.SecretGitHubToken]; token == "" {
			return res, errors.New("this repository is private and the job carries no GitHub token")
		}
	}
	repoDir := filepath.Join(d.Scratch, "repo")
	logf("checking out %s at %s", req.Repo, req.Candidate.BaseSHA)
	if err := d.Checkout(ctx, req.Repo, req.Candidate.BaseSHA, repoDir, token); err != nil {
		return res, fmt.Errorf("check out %s at %s: %w", req.Repo, req.Candidate.BaseSHA, err)
	}

	var env issuebench.EnvSpec
	if req.Mode == issuebench.WorkModeBuild {
		br, err := build(ctx, d, req, secrets, repoDir, logf)
		if err != nil {
			return res, err
		}
		res.Build, res.CostUSD = &br, br.CostUSD
		if br.Stop != issuebench.StopOK || br.Env == nil {
			res.Stop = "the builder stopped: " + string(br.Stop)
			if br.Detail != "" {
				res.Stop += ": " + br.Detail
			}
			return res, nil
		}
		env = *br.Env
	} else {
		env = *req.Env
	}
	if env.Repo == "" {
		env.Repo = req.Repo
	}
	if env.Version == "" {
		env.Version = req.Version
	}
	if !baseImageAllowed(env.BaseImage) {
		res.Stop = fmt.Sprintf("the environment's base image %q is not a base image Heimdall allows (%s)", env.BaseImage, strings.Join(allowedBaseImages(), ", "))
		return res, nil
	}

	tag := EnvImageTag(req)
	defer RemoveImage(ctx, d.Docker, tag)
	logf("building the environment image %s", tag)
	if err := BuildEnvImage(ctx, d.Docker, env, repoDir, d.Scratch, tag); err != nil {
		return stopOrErr(res, err)
	}
	timeout := time.Duration(issuebench.DefaultTestTimeoutSeconds) * time.Second
	if s := req.Limits.TestTimeoutSeconds; s > 0 {
		timeout = time.Duration(s) * time.Second
	}
	logf("running the tests in %s with no network", tag)
	v, err := ValidateTests(ctx, TestRunner{Docker: d.Docker, Image: tag, Runtime: d.Runtime, JobID: req.JobID, Timeout: timeout}, env, req.Candidate)
	if err != nil {
		return stopOrErr(res, fmt.Errorf("the tests could not run: %w", err))
	}
	v.Checks = issuebench.LLMChecks{}
	res.Validation = &v
	logf("tests done: %d fail to pass, %d pass to pass", len(v.FailToPass), len(v.PassToPass))
	return res, nil
}

// stopOrErr keeps a build's result (and its cost) when the step after it
// fails: a build-mode job returns the environment with Stop set, so the
// server saves it and records the spend. A validate-mode job fails.
func stopOrErr(res issuebench.WorkResult, err error) (issuebench.WorkResult, error) {
	if res.Build != nil {
		res.Stop = err.Error()
		return res, nil
	}
	return res, err
}

func build(ctx context.Context, d Deps, req issuebench.WorkRequest, secrets map[string]string, repoDir string, logf func(string, ...any)) (issuebench.BuildResult, error) {
	url := secrets[issuebench.SecretGatewayURL]
	if d.UsePublicGateway {
		url = secrets[issuebench.SecretGatewayPublicURL]
	}
	if strings.TrimSpace(url) == "" {
		return issuebench.BuildResult{}, ErrNoGateway
	}
	key := secrets[issuebench.SecretGatewayKey]
	if key == "" {
		return issuebench.BuildResult{}, errors.New("the job carries no model key for the builder")
	}
	if d.Sandbox == nil {
		return issuebench.BuildResult{}, errors.New("this worker has no build sandbox")
	}
	model := builder.NewGatewayModel(builder.GatewayConfig{
		Key:      &schemas.RunKey{Value: key, Provider: req.Provider, Model: req.Model},
		URL:      url,
		Provider: req.Provider,
		Model:    req.Model,
		Price:    d.Price,
		KeyName:  "r_ibw" + safe(req.JobID),
	})
	defer model.Close(ctx)
	factory, cleanup, err := d.Sandbox(ctx, req)
	if err != nil {
		return issuebench.BuildResult{}, fmt.Errorf("start the build sandbox: %w", err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	logf("building the environment for %s at %s on %s/%s (cap $%.2f)", req.Repo, req.Version, req.Provider, req.Model, req.MaxUSD)
	b := builder.New(builder.Config{Model: model, Sandbox: factory, Limits: limitsOf(req), Log: logf, LogPath: filepath.Join(d.Scratch, "build.jsonl")})
	return b.Build(ctx, builder.Request{Repo: req.Repo, Version: req.Version, RepoDir: repoDir, Candidates: []issuebench.Candidate{req.Candidate}, Prior: req.Env}), nil
}

func limitsOf(req issuebench.WorkRequest) builder.Limits {
	l := req.Limits
	return builder.Limits{
		SetupSteps:     l.SetupSteps,
		VerifySteps:    l.VerifySteps,
		FeedbackRounds: l.FeedbackRounds,
		Attempts:       l.Attempts,
		MaxUSD:         req.MaxUSD,
		CmdTimeout:     time.Duration(l.CmdTimeoutSeconds) * time.Second,
		AttemptTimeout: time.Duration(l.AttemptTimeoutSeconds) * time.Second,
	}
}

func allowedBaseImages() []string {
	var out []string
	for _, l := range issuebench.Languages() {
		out = append(out, l.BaseImages()...)
	}
	return out
}

func baseImageAllowed(img string) bool {
	if img == "" {
		return false
	}
	for _, a := range allowedBaseImages() {
		if a == img {
			return true
		}
	}
	return false
}
