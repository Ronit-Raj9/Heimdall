package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/investigator"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/replayrec"
	"github.com/maximhq/bifrost/bench/shadowreplay"
	"github.com/maximhq/bifrost/bench/toolproxy"
	"github.com/maximhq/bifrost/bench/traceinbox"
)

// Lane W4-47: shadow replay. replay.run reads recorded sessions from the
// org's Bifrost logs, replays them against another model with every tool
// answered from the recording (no tool is ever run), and writes
// replays/<job id>.json. The org's opt-in is checked before anything else.

const jobReplayRun = "replay.run"

const (
	replayDefaultSessions = 20
	replayDefaultBudget   = 2.0
	replayMaxWindow       = 14 * 24 * time.Hour
	replayMaxSteps        = 60
)

func init() {
	registerJobKind(jobKindSpec{Kind: jobReplayRun, Local: true, Hosted: true, Opts: []jobs.KindOption{jobs.WithLimit(2, 1)},
		Validate: validateReplayRun, Run: runReplayRun})
}

// replayParams are the params of replay.run.
type replayParams struct {
	Source     string       `json:"source"` // inbox source name of kind bifrost
	Since      string       `json:"since"`  // RFC 3339 or "24h" | "7d"; window at most 14 days
	Until      string       `json:"until,omitempty"`
	Sessions   int          `json:"sessions"`        // default 20, at most settings.max_sessions_per_run
	SampleRate float64      `json:"sample_rate"`     // default 1
	OnlyFailed bool         `json:"only_failed"`     // sessions with an errored row
	Mode       string       `json:"mode"`            // "fixed" (default) | "trajectory"
	MaxSteps   int          `json:"max_steps"`       // trajectory: default 30, at most 60
	Candidate  modelParams  `json:"candidate"`       // the new model; budget_usd default 2, at most settings.max_usd_per_run
	Judge      *modelParams `json:"judge,omitempty"` // optional: a judge for text answers; shares the same cap
}

// replayLogClient is the log reader of one job; tests replace it.
var replayLogClient = func(baseURL, token string, hosted bool) *replayrec.Client {
	c := &replayrec.Client{BaseURL: baseURL, Token: token}
	if hosted {
		c.HTTP = handlers.HostedPullClient()
	}
	return c
}

func checkReplayModel(p *modelParams, what string, maxUSD float64, defaultBudget float64) error {
	if strings.TrimSpace(p.Provider) == "" || strings.TrimSpace(p.Model) == "" {
		return liveInvalid("Pick a model first.")
	}
	if !p.Yes {
		return liveInvalid("Confirm the cost first.")
	}
	if p.BudgetUSD == 0 && defaultBudget > 0 {
		p.BudgetUSD = defaultBudget
	}
	if p.BudgetUSD <= 0 {
		return liveInvalid("Set a spending cap above 0 for the %s.", what)
	}
	if p.BudgetUSD > maxUSD {
		return liveInvalid("The spending cap for the %s is above this organization's limit of $%g per replay.", what, maxUSD)
	}
	return nil
}

// replayWindow reads since and until.
func replayWindow(p replayParams, now time.Time) (time.Time, time.Time, error) {
	until := now
	if p.Until != "" {
		t, err := time.Parse(time.RFC3339, p.Until)
		if err != nil {
			return time.Time{}, time.Time{}, liveInvalid("until must be a date and time like 2026-11-20T10:00:00Z")
		}
		until = t
	}
	var since time.Time
	switch s := strings.TrimSpace(p.Since); {
	case s == "":
		since = until.Add(-7 * 24 * time.Hour)
	case strings.HasSuffix(s, "d"):
		var n int
		if _, err := fmt.Sscanf(s, "%dd", &n); err != nil || n <= 0 {
			return time.Time{}, time.Time{}, liveInvalid("since must be like 24h, 7d or a date and time")
		}
		since = until.Add(-time.Duration(n) * 24 * time.Hour)
	case strings.HasSuffix(s, "h"):
		d, err := time.ParseDuration(s)
		if err != nil || d <= 0 {
			return time.Time{}, time.Time{}, liveInvalid("since must be like 24h, 7d or a date and time")
		}
		since = until.Add(-d)
	default:
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return time.Time{}, time.Time{}, liveInvalid("since must be like 24h, 7d or a date and time")
		}
		since = t
	}
	if !since.Before(until) {
		return time.Time{}, time.Time{}, liveInvalid("the time window is empty")
	}
	if until.Sub(since) > replayMaxWindow {
		return time.Time{}, time.Time{}, liveInvalid("the time window is at most 14 days")
	}
	return since, until, nil
}

// replayCheck normalizes and checks the params. The opt-in comes first.
func replayCheck(env jobEnv, p *replayParams) error {
	if err := shadowreplay.CheckEnabled(env.ProjectRoot); err != nil {
		return liveInvalid("%s", shadowreplay.OffText)
	}
	set := shadowreplay.LoadSettings(env.ProjectRoot)
	if p.Mode == "" {
		p.Mode = shadowreplay.ModeFixed
	}
	if p.Mode != shadowreplay.ModeFixed && p.Mode != shadowreplay.ModeTrajectory {
		return liveInvalid("mode must be fixed or trajectory")
	}
	if p.Sessions == 0 {
		p.Sessions = min(replayDefaultSessions, set.MaxSessionsPerRun)
	}
	if p.Sessions < 0 || p.Sessions > set.MaxSessionsPerRun {
		return liveInvalid("sessions must be between 1 and %d", set.MaxSessionsPerRun)
	}
	if p.SampleRate == 0 {
		p.SampleRate = 1
	}
	if p.SampleRate < 0 || p.SampleRate > 1 {
		return liveInvalid("sample_rate must be more than 0 and at most 1")
	}
	if p.Mode == shadowreplay.ModeTrajectory {
		if p.MaxSteps == 0 {
			p.MaxSteps = 30
		}
		if p.MaxSteps < 0 || p.MaxSteps > replayMaxSteps {
			return liveInvalid("max_steps must be between 1 and %d", replayMaxSteps)
		}
	}
	if _, _, err := replayWindow(*p, time.Now()); err != nil {
		return err
	}
	if err := checkReplayModel(&p.Candidate, "new model", set.MaxUSDPerRun, replayDefaultBudget); err != nil {
		return err
	}
	if p.Judge != nil {
		if err := checkReplayModel(p.Judge, "judge", set.MaxUSDPerRun, 0); err != nil {
			return err
		}
	}
	if _, err := replaySource(env, p.Source); err != nil {
		return err
	}
	return nil
}

// replaySource finds the Bifrost source the params name.
func replaySource(env jobEnv, name string) (*traceinbox.Source, error) {
	if strings.TrimSpace(name) == "" {
		return nil, liveInvalid("Pick the Bifrost source to read sessions from.")
	}
	cfg, err := traceinbox.LoadConfig(filepath.Join(env.ProjectRoot, traceinbox.Dir, traceinbox.SourcesFile))
	if err != nil {
		return nil, liveInvalid("%v", err)
	}
	for i := range cfg.Sources {
		s := cfg.Sources[i]
		if s.Name != name {
			continue
		}
		if s.Kind != traceinbox.KindBifrost {
			return nil, liveInvalid("source %s is not a Bifrost source", name)
		}
		if s.URL == "" && env.App != nil {
			s.URL = env.App.cfg.Gateway.URL
		}
		if s.TokenEnv == "" && env.App != nil {
			s.TokenEnv = env.App.cfg.Gateway.AdminTokenEnv
		}
		if s.URL == "" {
			return nil, liveInvalid("source %s has no gateway address", name)
		}
		if env.Hosted {
			if err := handlers.CheckPullURL(s.URL); err != nil {
				return nil, liveInvalid("%v", err)
			}
		}
		return &s, nil
	}
	return nil, liveInvalid("there is no inbox source named %s", name)
}

func validateReplayRun(_ context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
	var p replayParams
	if err := liveDecode(raw, &p); err != nil {
		return nil, "", err
	}
	if err := replayCheck(env, &p); err != nil {
		return nil, "", err
	}
	return p, p.Candidate.Provider + "/" + p.Candidate.Model, nil
}

// sharedCap makes the candidate and the judge share one spending cap.
type sharedCap struct {
	mu     sync.Mutex
	inner  investigator.Caller
	others []investigator.Caller
	cap    float64
}

func (s *sharedCap) total() float64 {
	t := s.inner.Spent()
	for _, o := range s.others {
		if o != nil && o != s.inner {
			t += o.Spent()
		}
	}
	return t
}

func (s *sharedCap) Call(ctx context.Context, req investigator.Request) (investigator.Reply, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.total() >= s.cap {
		return investigator.Reply{}, investigator.ErrBudget
	}
	return s.inner.Call(ctx, req)
}

func (s *sharedCap) Spent() float64  { return s.inner.Spent() }
func (s *sharedCap) Budget() float64 { return s.cap }

func runReplayRun(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
	// The opt-in first: nothing is read or sent while it is off.
	if err := shadowreplay.CheckEnabled(env.ProjectRoot); err != nil {
		return nil, err
	}
	var p replayParams
	if err := liveDecode(j.Params, &p); err != nil {
		return nil, err
	}
	if err := replayCheck(env, &p); err != nil {
		return nil, err
	}
	if openCaller == nil {
		return nil, errors.New("this server makes no model calls")
	}
	src, err := replaySource(env, p.Source)
	if err != nil {
		return nil, err
	}
	token := ""
	if src.TokenEnv != "" {
		if env.Hosted {
			m, err := toolproxy.OrgSecretEnv(ctx, env.Services.Keys, env.OrgID, []string{src.TokenEnv})
			if err != nil {
				return nil, fmt.Errorf("read the secret %s: %w", src.TokenEnv, err)
			}
			token = m[src.TokenEnv]
		} else {
			token = os.Getenv(src.TokenEnv)
		}
		if token == "" {
			return nil, fmt.Errorf("the source %s needs the secret %s, which is not set", src.Name, src.TokenEnv)
		}
	}
	since, until, err := replayWindow(p, time.Now())
	if err != nil {
		return nil, err
	}
	client := replayLogClient(src.URL, token, env.Hosted)
	refs, err := client.Sessions(ctx, replayrec.Query{Since: since, Until: until, VirtualKeys: src.VirtualKeys, Max: replayrec.MaxSessions, SampleRate: p.SampleRate})
	if err != nil {
		return nil, err
	}
	var recs []*replayrec.Recording
	for _, ref := range refs {
		if len(recs) >= p.Sessions {
			break
		}
		if p.OnlyFailed && !ref.Failed {
			continue
		}
		rec, err := client.Session(ctx, src.Name, ref.SessionID, since, until)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			log.Warnf("skipped a session: %v", err)
			continue
		}
		if err := shadowreplay.SaveRecording(env.ProjectRoot, rec); err != nil {
			log.Warnf("skipped a session: %v", err)
			continue
		}
		recs = append(recs, rec)
	}
	log.Printf("read %d recorded sessions from %s", len(recs), src.Name)
	if progress != nil {
		progress(map[string]any{"sessions": len(recs)})
	}
	cand, done, err := openCaller(ctx, env, j, p.Candidate)
	if err != nil {
		return nil, err
	}
	defer done()
	var judge investigator.Caller
	if p.Judge != nil {
		jc, jdone, err := openCaller(ctx, env, j, *p.Judge)
		if err != nil {
			return nil, err
		}
		defer jdone()
		judge = jc
	}
	capUSD := p.Candidate.BudgetUSD
	c := &sharedCap{inner: cand, others: []investigator.Caller{judge}, cap: capUSD}
	var jc investigator.Caller
	if judge != nil {
		jc = &sharedCap{inner: judge, others: []investigator.Caller{cand}, cap: capUSD}
	}
	opt := shadowreplay.Options{Candidate: p.Candidate.Provider + "/" + p.Candidate.Model, MaxSteps: p.MaxSteps}
	rep := shadowreplay.Run(ctx, j.ID, p.Mode, recs, c, jc, opt)
	if err := shadowreplay.SaveReport(env.ProjectRoot, rep); err != nil {
		return nil, err
	}
	log.Printf("replayed %d sessions against %s, spent $%.4f", len(rep.Sessions), opt.Candidate, rep.SpentUSD)
	if rep.Stopped != "" {
		log.Warnf("the replay stopped early: %s", rep.Stopped)
	}
	return map[string]any{"replay": rep.ID, "sessions": len(rep.Sessions), "counts": rep.Counts, "spent_usd": rep.SpentUSD, "stopped": rep.Stopped}, nil
}
