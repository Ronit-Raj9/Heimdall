package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/livescore"
	"github.com/maximhq/bifrost/bench/traceinbox"
)

// Web parity lane L18: live scoring and the failure inbox as background
// jobs, so the Live and Inbox pages can set up, pull and compare without a
// terminal. Every kind calls the livescore and traceinbox packages in
// process, the same code the CLI subcommands run.
//
// A pull reads a gateway or an observability tool with a credential. Local
// servers read the credential from their environment. The hosted server runs
// the pull in process (it only makes HTTP calls) with the credential from an
// org secret and the guardrails of jobs_live_hosted.go. Init and compare only
// touch the org's own files and run in both modes.

const (
	jobLiveInit    = "livescore.init"
	jobLivePull    = "livescore.pull"
	jobLiveCompare = "livescore.compare"
	jobInboxPull   = "inbox.pull"
)

func init() {
	registerJobKind(jobKindSpec{Kind: jobLiveInit, Local: true, Hosted: true, Validate: validateLiveInit, Run: runLiveInit})
	registerJobKind(jobKindSpec{Kind: jobLivePull, Local: true, Hosted: true, Validate: validateLivePull, Run: runLivePull})
	registerJobKind(jobKindSpec{Kind: jobLiveCompare, Local: true, Hosted: true, Validate: validateLiveCompare, Run: runLiveCompare})
	registerJobKind(jobKindSpec{Kind: jobInboxPull, Local: true, Hosted: true, Validate: validateInboxPull, Run: runInboxPull})
}

// liveInitParams are the params of livescore.init (the CLI's init flags).
type liveInitParams struct {
	URL          string `json:"url,omitempty"`
	TokenEnv     string `json:"token_env,omitempty"`
	BaselineKey  string `json:"baseline_key,omitempty"`
	CandidateKey string `json:"candidate_key,omitempty"`
	TagHeader    string `json:"tag_header,omitempty"`
	BaselineTag  string `json:"baseline_tag,omitempty"`
	CandidateTag string `json:"candidate_tag,omitempty"`
	Force        bool   `json:"force,omitempty"`
}

// livePullParams are the params of livescore.pull. Judge turns on the paid
// live judge for this pull; it is off unless asked, like a scheduled pull.
type livePullParams struct {
	Judge bool `json:"judge,omitempty"`
}

// liveCompareParams are the params of livescore.compare.
type liveCompareParams struct {
	Restart  bool    `json:"restart,omitempty"`
	Simulate int     `json:"simulate,omitempty"`
	Sessions int     `json:"sessions,omitempty"`
	Effect   float64 `json:"effect,omitempty"`
}

// inboxPullParams are the params of inbox.pull.
type inboxPullParams struct {
	Sources []string `json:"sources,omitempty"`
	Since   string   `json:"since,omitempty"`
}

var (
	liveEnvNameRE = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)
	// liveIdentRE bounds virtual key ids, tag values and source names.
	liveIdentRE  = regexp.MustCompile(`^[A-Za-z0-9._:@/-]{1,128}$`)
	liveHeaderRE = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
)

// liveReservedEnv are variables a token_env may never name: a pull would send
// their value to the configured gateway as a credential.
func liveReservedEnv(name string) bool {
	switch name {
	case "HEIMDALL_DATABASE_URL", "HEIMDALL_MASTER_KEY", "HEIMDALL_SHARED_RUNNER_SECRET", "PATH", "HOME":
		return true
	}
	for _, p := range []string{"AWS_", "S3_", "GOOGLE_", "AZURE_"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// liveDecode reads raw into v; empty params are the zero value. Unknown
// fields are refused, so a typo is reported instead of ignored.
func liveDecode(raw json.RawMessage, v any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: %v", jobs.ErrInvalidParams, err)
	}
	return nil
}

func liveInvalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", jobs.ErrInvalidParams, fmt.Sprintf(format, args...))
}

func (p liveInitParams) check() error {
	if p.URL != "" {
		u, err := url.Parse(p.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
			return liveInvalid("url must be an http(s) URL of the gateway, without credentials")
		}
	}
	if p.TokenEnv != "" && (!liveEnvNameRE.MatchString(p.TokenEnv) || liveReservedEnv(p.TokenEnv)) {
		return liveInvalid("token_env must be an environment variable name like BIFROST_ADMIN_TOKEN (server variables are not allowed)")
	}
	byKey := p.BaselineKey != "" || p.CandidateKey != ""
	byTag := p.TagHeader != "" || p.BaselineTag != "" || p.CandidateTag != ""
	if byKey && byTag {
		return liveInvalid("pick one way to tell the versions apart: virtual keys or a tag header, not both")
	}
	if byKey {
		if !liveIdentRE.MatchString(p.BaselineKey) || !liveIdentRE.MatchString(p.CandidateKey) {
			return liveInvalid("set both baseline_key and candidate_key (virtual key ids)")
		}
		if p.BaselineKey == p.CandidateKey {
			return liveInvalid("baseline_key and candidate_key must differ")
		}
	}
	if byTag {
		if !liveHeaderRE.MatchString(p.TagHeader) || !liveIdentRE.MatchString(p.BaselineTag) || !liveIdentRE.MatchString(p.CandidateTag) {
			return liveInvalid("set tag_header (e.g. x-bf-lh-agent-version) with both baseline_tag and candidate_tag")
		}
		if p.BaselineTag == p.CandidateTag {
			return liveInvalid("baseline_tag and candidate_tag must differ")
		}
	}
	return nil
}

func (p liveInitParams) options() livescore.InitOptions {
	return livescore.InitOptions{URL: p.URL, TokenEnv: p.TokenEnv, BaselineKey: p.BaselineKey, CandidateKey: p.CandidateKey,
		TagHeader: p.TagHeader, BaselineTag: p.BaselineTag, CandidateTag: p.CandidateTag}
}

func liveConfigPath(env jobEnv) string {
	return filepath.Join(env.App.cfg.Path(livescore.Dir), livescore.ConfigFile)
}

func validateLiveInit(_ context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
	var p liveInitParams
	if err := liveDecode(raw, &p); err != nil {
		return nil, "", err
	}
	if err := p.check(); err != nil {
		return nil, "", err
	}
	text, err := livescore.Template(p.options())
	if err != nil {
		return nil, "", liveInvalid("%v", err)
	}
	if p.BaselineKey != "" || p.TagHeader != "" {
		if _, err := livescore.ParseConfig([]byte(text), "template"); err != nil {
			return nil, "", liveInvalid("the config from these settings does not validate: %v", err)
		}
	}
	if !p.Force {
		if _, err := os.Stat(liveConfigPath(env)); err == nil {
			return nil, "", fmt.Errorf("%w: livescore/config.yaml exists; tick overwrite to replace it", jobs.ErrConflict)
		}
	}
	return p, "livescore/config.yaml", nil
}

// liveInitResult is the result of livescore.init.
type liveInitResult struct {
	Path       string `json:"path"`
	Configured bool   `json:"configured"`
	Next       string `json:"next"`
}

func runLiveInit(_ context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, _ func(any)) (any, error) {
	var p liveInitParams
	if err := liveDecode(j.Params, &p); err != nil {
		return nil, err
	}
	if err := p.check(); err != nil {
		return nil, err
	}
	text, err := livescore.Template(p.options())
	if err != nil {
		return nil, err
	}
	path := liveConfigPath(env)
	if _, err := os.Stat(path); err == nil && !p.Force {
		return nil, fmt.Errorf("livescore/config.yaml exists; tick overwrite to replace it")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	res := liveInitResult{Path: livescore.Dir + "/" + livescore.ConfigFile, Configured: p.BaselineKey != "" || p.TagHeader != ""}
	if res.Configured {
		res.Next = "Set the gateway read key on this server, then press Pull now."
		if env.Hosted {
			res.Next = "Add the gateway read key as an organization secret in Settings > Secrets, then press Pull now."
		}
	} else {
		res.Next = "replace the EDIT ME virtual keys in livescore/config.yaml (Files page), then pull"
	}
	log.Printf("wrote %s", res.Path)
	log.Printf("next: %s", res.Next)
	return res, nil
}

func validateLivePull(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
	var p livePullParams
	if err := liveDecode(raw, &p); err != nil {
		return nil, "", err
	}
	if _, err := livescore.LoadConfig(liveConfigPath(env)); err != nil {
		return nil, "", liveInvalid("%v", err)
	}
	if env.Hosted {
		if err := validateLivePullHosted(ctx, env, p); err != nil {
			return nil, "", err
		}
	}
	return p, "livescore", nil
}

func runLivePull(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, _ func(any)) (any, error) {
	var p livePullParams
	if err := liveDecode(j.Params, &p); err != nil {
		return nil, err
	}
	if env.Hosted {
		return runLivePullHosted(ctx, env, p, log)
	}
	s, err := scorerFor(env.App)
	if err != nil {
		return nil, err
	}
	if !p.Judge && unattendedScorer(s) {
		log.Warnf("the live judge is off for this pull (paid calls); tick Judge to use it")
	}
	res, err := s.Pull(ctx)
	if res != nil {
		printLivePull(log, res)
	}
	if err != nil {
		return res, fmt.Errorf("livescore pull: %w", err)
	}
	return res, nil
}

func (p liveCompareParams) check() error {
	if p.Simulate < 0 || p.Simulate > 2000 {
		return liveInvalid("simulate must be 0 to 2000 streams")
	}
	if p.Sessions < 0 || p.Sessions > 5000 {
		return liveInvalid("sessions must be 0 to 5000")
	}
	if p.Effect < 0 || p.Effect > 1 {
		return liveInvalid("effect must be 0 to 1")
	}
	if p.Simulate > 0 && p.Restart {
		return liveInvalid("restart and simulate do not go together")
	}
	return nil
}

func validateLiveCompare(_ context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
	var p liveCompareParams
	if err := liveDecode(raw, &p); err != nil {
		return nil, "", err
	}
	if err := p.check(); err != nil {
		return nil, "", err
	}
	if p.Simulate == 0 {
		if _, err := livescore.LoadConfig(liveConfigPath(env)); err != nil {
			return nil, "", liveInvalid("%v", err)
		}
	}
	return p, "livescore", nil
}

func runLiveCompare(_ context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, _ func(any)) (any, error) {
	var p liveCompareParams
	if err := liveDecode(j.Params, &p); err != nil {
		return nil, err
	}
	if err := p.check(); err != nil {
		return nil, err
	}
	if p.Simulate > 0 {
		sessions := p.Sessions
		if sessions == 0 {
			sessions = 600
		}
		r := livescore.Simulate(livescore.SimConfig{Streams: p.Simulate, Sessions: sessions, Effect: p.Effect, Seed: uint64(time.Now().UnixNano())})
		log.Printf("%d synthetic streams, %d sessions per version, effect %.3f: %d streams decided (rate %.4f), %d decided worse",
			r.Streams, r.Sessions, r.Effect, r.AnyDecision, r.Rate, r.WorseDecisions)
		return r, nil
	}
	s, err := scorerFor(env.App)
	if err != nil {
		return nil, err
	}
	var c *livescore.Comparison
	if p.Restart {
		c, err = s.RestartCompare()
	} else {
		c, err = s.Compare()
	}
	if err != nil {
		return nil, err
	}
	if c == nil {
		if s.Config.Compare == nil {
			return nil, errors.New("no compare section in livescore/config.yaml: add one (Files page) to compare two versions")
		}
		log.Printf("no comparison yet: pull first")
		return map[string]string{"result": "none", "next": "pull first"}, nil
	}
	printLiveCompare(log, c)
	return c, nil
}

func validateInboxPull(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
	var p inboxPullParams
	if err := liveDecode(raw, &p); err != nil {
		return nil, "", err
	}
	if len(p.Sources) > 32 {
		return nil, "", liveInvalid("at most 32 sources")
	}
	for _, s := range p.Sources {
		if !liveIdentRE.MatchString(s) {
			return nil, "", liveInvalid("source %q is not a source name", s)
		}
	}
	if p.Since != "" {
		if _, err := parseSince(p.Since, time.Now()); err != nil {
			return nil, "", liveInvalid("since: %v (use 24h, 7d or an RFC 3339 time)", err)
		}
	}
	cfg, err := traceinbox.LoadConfig(filepath.Join(env.App.cfg.Path(traceinbox.Dir), traceinbox.SourcesFile))
	if err != nil {
		return nil, "", liveInvalid("%v", err)
	}
	pullable := 0
	for _, s := range cfg.Sources {
		if s.Kind != traceinbox.KindWebhook && (!env.Hosted || hostedPullable(s.Kind)) {
			pullable++
		}
	}
	if pullable == 0 {
		return nil, "", liveInvalid("no source to pull: add a bifrost, langfuse or langsmith source with the Add a pull source form (inbox/sources.yaml)")
	}
	if env.Hosted {
		if err := validateInboxPullHosted(ctx, env, p, cfg); err != nil {
			return nil, "", err
		}
	}
	return p, "inbox", nil
}

func runInboxPull(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, _ func(any)) (any, error) {
	var p inboxPullParams
	if err := liveDecode(j.Params, &p); err != nil {
		return nil, err
	}
	if env.Hosted {
		return runInboxPullHosted(ctx, env, p, log)
	}
	ib, err := inboxFor(env.App, false)
	if err != nil {
		return nil, err
	}
	opts := traceinbox.PullOptions{Sources: p.Sources}
	if p.Since != "" {
		t, err := parseSince(p.Since, time.Now())
		if err != nil {
			return nil, err
		}
		opts.Since = t
	}
	results, err := ib.Pull(ctx, opts)
	printPull(log, results)
	if err != nil {
		return results, fmt.Errorf("inbox pull: %w", err)
	}
	return results, nil
}
