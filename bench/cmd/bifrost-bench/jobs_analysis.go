package main

// Web parity lane L15 (Analysis): the analysis commands as background jobs,
// run in-process on the org's launcher. Every kind reads sealed run folders
// only and starts no container, so each one also runs on the hosted server.
// blame.run is tier 1 only: a tier-2 replay runs agents again and stays a
// CLI command (`bifrost-bench blame -replay`).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/maximhq/bifrost/bench/attribution"
	"github.com/maximhq/bifrost/bench/clusters"
	"github.com/maximhq/bifrost/bench/codescan"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/ops"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
)

// Job kinds of the analysis area.
const (
	kindCheckRun        = "check.run"
	kindCodeScanRun     = "codescan.run"
	kindAwarenessRun    = "awareness.run"
	kindBlameRun        = "blame.run"
	kindClustersRebuild = "clusters.rebuild"
)

// analysisParams are the params of the per-experiment analysis kinds.
type analysisParams struct {
	ExpID       string `json:"exp_id"`
	RunID       string `json:"run_id,omitempty"`       // codescan.run, blame.run: only this run
	GradeSource string `json:"grade_source,omitempty"` // check.run: original (default), reviewed or a regrade id
}

// analysisTarget serializes the jobs that write an experiment's analysis
// artifacts, apart from its experiment.run.
func analysisTarget(expID string) string { return "analysis:" + expID }

func init() {
	expKind := func(kind string, allowRun, allowGrade, writes bool, run func(ctx context.Context, l *launcher, p analysisParams, log jobs.Logger) (any, error)) {
		registerJobKind(jobKindSpec{
			Kind: kind, Local: true, Hosted: true,
			Validate: func(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
				l, err := envLauncher(env)
				if err != nil {
					return nil, "", err
				}
				p, err := decodeAnalysisParams(raw, allowRun, allowGrade)
				if err != nil {
					return nil, "", err
				}
				if err := validateExp(ctx, l, p.ExpID); err != nil {
					return nil, "", err
				}
				target := ""
				if writes {
					target = analysisTarget(p.ExpID)
				}
				return p, target, nil
			},
			Run: func(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, _ func(any)) (any, error) {
				l, err := envLauncher(env)
				if err != nil {
					return nil, err
				}
				var p analysisParams
				if err := j.DecodeParams(&p); err != nil {
					return nil, err
				}
				return run(ctx, l, p, log)
			},
		})
	}
	expKind(kindCheckRun, false, true, true, checkRunJob)
	expKind(kindCodeScanRun, true, false, false, codeScanRunJob)
	expKind(kindAwarenessRun, false, false, false, awarenessRunJob)
	expKind(kindBlameRun, true, false, true, blameRunJob)

	registerJobKind(jobKindSpec{
		Kind: kindClustersRebuild, Local: true, Hosted: true,
		Validate: func(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
			var p emptyParams
			if err := strictParams(orEmpty(raw), &p); err != nil {
				return nil, "", err
			}
			return p, "index", nil // beside index.rebuild, never at the same time
		},
		Run: func(ctx context.Context, env jobEnv, _ *jobs.Job, log jobs.Logger, _ func(any)) (any, error) {
			l, err := envLauncher(env)
			if err != nil {
				return nil, err
			}
			return clustersRebuildJob(ctx, l, log)
		},
	})
}

// envLauncher is the job's org launcher (jobEnv.Services.Launcher).
func envLauncher(env jobEnv) (*launcher, error) {
	l, ok := env.Services.Launcher.(*launcher)
	if !ok || l == nil || l.a == nil {
		return nil, errors.New("this server has no experiment store for the organization")
	}
	return l, nil
}

func orEmpty(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage("{}")
	}
	return raw
}

// decodeAnalysisParams decodes and checks the params; fields a kind does
// not take are refused.
func decodeAnalysisParams(raw json.RawMessage, allowRun, allowGrade bool) (analysisParams, error) {
	var p analysisParams
	if err := strictParams(orEmpty(raw), &p); err != nil {
		return p, err
	}
	if p.ExpID == "" {
		return p, invalid(errors.New("exp_id is required"))
	}
	if p.RunID != "" {
		if !allowRun {
			return p, invalid(errors.New("run_id is not a param of this kind"))
		}
		if err := ops.ValidID("run_id", p.RunID); err != nil {
			return p, invalid(err)
		}
	}
	if p.GradeSource != "" {
		if !allowGrade {
			return p, invalid(errors.New("grade_source is not a param of this kind"))
		}
		if err := ops.ValidID("grade_source", p.GradeSource); err != nil {
			return p, invalid(err)
		}
	}
	return p, nil
}

// checkRunResult is check.run's result.
type checkRunResult struct {
	ExpID       string `json:"exp_id"`
	AnalysisID  string `json:"analysis_id"`
	GradeSource string `json:"grade_source"`
	Decision    string `json:"decision"`
	Action      string `json:"action"`
	Policy      string `json:"policy,omitempty"` // the release policy's decision, when one applies
}

// checkRunJob re-analyzes a frozen experiment and saves a new analysis, the
// web form of `check -exp`. The verdict is the result; a regressed verdict
// is not a failed job.
func checkRunJob(ctx context.Context, l *launcher, p analysisParams, log jobs.Logger) (any, error) {
	log.Printf("analyzing %s (grade source %s)", p.ExpID, orDefault(p.GradeSource, schemas.GradeSourceOriginal))
	an, _, err := l.a.analyzeWith(context.WithoutCancel(ctx), p.ExpID, p.GradeSource, "")
	if err != nil {
		return nil, fmt.Errorf("analyze %s: %w", p.ExpID, err)
	}
	res := checkRunResult{ExpID: p.ExpID, AnalysisID: an.AnalysisID, GradeSource: an.GradeSource,
		Decision: string(an.Verdict.Decision), Action: string(an.Verdict.Action)}
	if an.Policy != nil {
		res.Policy = string(an.Policy.Decision)
	}
	log.Printf("verdict: %s, action: %s", res.Decision, res.Action)
	if res.Policy != "" {
		log.Printf("release policy %s: %s", an.Policy.PolicyName, res.Policy)
	}
	log.Printf("saved analysis %s", res.AnalysisID)
	return res, nil
}

// codeScanRunResult is codescan.run's result.
type codeScanRunResult struct {
	ExpID      string         `json:"exp_id"`
	Runs       int            `json:"runs"`        // runs looked at
	Scanned    int            `json:"scanned"`     // of those, runs with a diff that was scanned
	Skipped    int            `json:"skipped"`     // runs whose seal did not verify: nothing read
	Findings   int            `json:"findings"`    // counted findings over every scanned run
	BySeverity map[string]int `json:"by_severity"` // counted findings per severity
	Vetoed     []string       `json:"vetoed"`      // runs the experiment's veto level would veto
}

// codeScanRunJob scans the lines every sealed run's agent added, as GET
// /runs/{id}/codescan does for one run.
func codeScanRunJob(ctx context.Context, l *launcher, p analysisParams, log jobs.Logger) (any, error) {
	m, err := l.a.store.LoadManifest(ctx, p.ExpID)
	if err != nil {
		return nil, err
	}
	runs, err := l.a.store.SealedRuns(ctx, p.ExpID, "")
	if err != nil {
		return nil, fmt.Errorf("load sealed runs: %w", err)
	}
	res := codeScanRunResult{ExpID: p.ExpID, BySeverity: map[string]int{}, Vetoed: []string{}}
	for i := range runs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sr := &runs[i]
		if p.RunID != "" && sr.Run.RunID != p.RunID {
			continue
		}
		res.Runs++
		if len(sr.Problems) > 0 {
			res.Skipped++
			log.Printf("%s: skipped, evidence problem: %s", sr.Run.RunID, sr.Problems[0])
			continue
		}
		ev, err := codescan.LoadRun(sr.Dir)
		if err != nil {
			res.Skipped++
			log.Printf("%s: skipped: %v", sr.Run.RunID, err)
			continue
		}
		scan, sealed := codescan.ForRun(ev)
		rep := codescan.BuildReport(sr.Run.RunID, scan, sealed, m.Experiment.CodeScan)
		if !scan.Scanned {
			log.Printf("%s: not scanned: %s", sr.Run.RunID, scan.Reason)
			continue
		}
		res.Scanned++
		res.Findings += rep.Counted
		for _, f := range codescan.Counted(scan, m.Experiment.CodeScan) {
			res.BySeverity[f.Severity]++
		}
		if rep.Vetoed {
			res.Vetoed = append(res.Vetoed, sr.Run.RunID)
		}
		log.Printf("%s: %d counted findings on %d added lines in %d files", sr.Run.RunID, rep.Counted, scan.AddedLines, scan.Files)
	}
	if p.RunID != "" && res.Runs == 0 {
		return nil, fmt.Errorf("%w: run %s is not a sealed run of %s", jobs.ErrNotFound, p.RunID, p.ExpID)
	}
	log.Printf("%d runs, %d scanned, %d counted findings, %d would be vetoed", res.Runs, res.Scanned, res.Findings, len(res.Vetoed))
	return res, nil
}

// awarenessRunJob runs the eval awareness phrase scan (GET
// /experiments/{id}/awareness) and returns the report.
func awarenessRunJob(ctx context.Context, l *launcher, p analysisParams, log jobs.Logger) (any, error) {
	ix, err := launcherIndex(ctx, l)
	if err != nil {
		return nil, err
	}
	rep, err := handlers.BuildAwareness(ctx, ix, p.ExpID)
	if err != nil {
		return nil, fmt.Errorf("awareness %s: %w", p.ExpID, err)
	}
	arms := make([]string, 0, len(rep.PerArm))
	for arm := range rep.PerArm {
		arms = append(arms, arm)
	}
	sort.Strings(arms)
	for _, arm := range arms {
		a := rep.PerArm[arm]
		log.Printf("arm %s: %d runs, %d scanned, %d with signals, %d signals", arm, a.Runs, a.RunsScanned, a.RunsWithSignals, a.Signals)
	}
	log.Printf("HEURISTIC: %s", rep.Label)
	return rep, nil
}

// blameRunResult is blame.run's result.
type blameRunResult struct {
	ExpID   string `json:"exp_id"`
	Runs    int    `json:"runs"`    // blame.json written
	Failed  int    `json:"failed"`  // of those, failed runs
	Blamed  int    `json:"blamed"`  // of those, failures pinned on one agent
	Skipped int    `json:"skipped"` // runs whose seal did not verify
}

// blameRunJob computes the tier-1 blame of every sealed run and writes
// blame.json beside it; an earlier replay result is kept.
func blameRunJob(ctx context.Context, l *launcher, p analysisParams, log jobs.Logger) (any, error) {
	runs, err := l.a.store.SealedRuns(ctx, p.ExpID, "")
	if err != nil {
		return nil, fmt.Errorf("load sealed runs: %w", err)
	}
	root := l.a.store.Root()
	res := blameRunResult{ExpID: p.ExpID}
	for i := range runs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sr := &runs[i]
		if p.RunID != "" && sr.Run.RunID != p.RunID {
			continue
		}
		if len(sr.Problems) > 0 {
			res.Skipped++
			log.Printf("%s: skipped, evidence problem: %s", sr.Run.RunID, sr.Problems[0])
			continue
		}
		b, _, err := tier1Of(sr)
		if err != nil {
			return nil, fmt.Errorf("blame %s: %w", sr.Run.RunID, err)
		}
		keepReplay(root, sr, b)
		if err := attribution.Write(root, b); err != nil {
			return nil, fmt.Errorf("blame %s: %w", sr.Run.RunID, err)
		}
		res.Runs++
		if b.Failed {
			res.Failed++
			if b.AgentID != "" {
				res.Blamed++
			}
		}
		log.Printf("%s: %s, tier %s, agent %s, gate %s", sr.Run.RunID, b.Outcome, b.Tier, orDash(b.AgentID), orDash(b.Gate))
	}
	if p.RunID != "" && res.Runs+res.Skipped == 0 {
		return nil, fmt.Errorf("%w: run %s is not a sealed run of %s", jobs.ErrNotFound, p.RunID, p.ExpID)
	}
	log.Printf("blame written for %d runs: %d failed, %d pinned on one agent (tier 1; replays stay a CLI step)", res.Runs, res.Failed, res.Blamed)
	return res, nil
}

// clustersRebuildResult is clusters.rebuild's result.
type clustersRebuildResult struct {
	Runs        int `json:"runs"`
	Failures    int `json:"failures"`
	Clusters    int `json:"clusters"`
	Experiments int `json:"experiments"`
}

// clustersRebuildJob refreshes the run index, then groups the failures of
// every verified run by cause, as GET /clusters does.
func clustersRebuildJob(ctx context.Context, l *launcher, log jobs.Logger) (any, error) {
	if err := l.rebuild(ctx); err != nil {
		return nil, fmt.Errorf("index runs: %w", err)
	}
	ix, err := launcherIndex(ctx, l)
	if err != nil {
		return nil, err
	}
	rep, err := clusters.Build(ctx, ix, clusters.Query{Min: 1, Synthetic: clusters.SyntheticExclude})
	if err != nil {
		return nil, fmt.Errorf("clusters: %w", err)
	}
	for i, c := range rep.Clusters {
		if i == 20 {
			log.Printf("... and %d more", len(rep.Clusters)-i)
			break
		}
		log.Printf("%d  %s  %s", c.Count, c.Signature, c.Title)
	}
	res := clustersRebuildResult{Runs: rep.Runs, Failures: rep.Failures, Clusters: len(rep.Clusters), Experiments: len(rep.Experiments)}
	log.Printf("%d failures in %d verified runs across %d experiments: %d clusters", res.Failures, res.Runs, res.Experiments, res.Clusters)
	return res, nil
}

// launcherIndex is the launcher's run index, or a fresh one built from its
// store when the launcher keeps none.
func launcherIndex(ctx context.Context, l *launcher) (indexAdapter, error) {
	if l.ix != nil {
		return indexAdapter{ix: l.ix, root: l.a.store.Root()}, nil
	}
	ix := store.NewIndex(l.a.store)
	if err := ix.Rebuild(ctx); err != nil {
		return indexAdapter{}, fmt.Errorf("index runs: %w", err)
	}
	return indexAdapter{ix: ix, root: l.a.store.Root()}, nil
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
