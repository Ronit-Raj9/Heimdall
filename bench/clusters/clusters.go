// Package clusters groups failed runs by cause across experiments
// (docs/failure-clusters.md), so a team reads ten clusters instead of three
// hundred runs. Each non-passing run gets a signature: a stable hash of
// layered, deterministic signals (outcome, failed hidden tests or other
// shortfall, fired gates, a normalized error fingerprint, the repeated call
// triage found, the provider status, the last tool call, the probe
// condition). Runs that share a signature form a cluster. No model and no
// embedding is involved: the same runs always give the same clusters.
//
// The package is pure compute over verified sealed runs. It reads them
// through Source, which the store's verified index backs (SealedRuns checks
// every run against its seal), and writes nothing. Per-experiment triage
// (docs/triage.md) stays as it is; clusters reuse its pattern_loop metric.
package clusters

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Evidence files a cluster reads beside the sealed record. Source.RunEvidence
// must serve exactly these.
const (
	FileEvents = "events.jsonl"
	FileLedger = "proxy.ledger.jsonl"
	FileTools  = "tools.jsonl"
	FileStderr = "agent.stderr.txt"
)

// Synthetic filter values.
const (
	SyntheticExclude = "exclude"
	SyntheticInclude = "include"
	SyntheticOnly    = "only"
)

// Exclusion reasons.
const (
	ReasonSynthetic   = "synthetic"
	ReasonSealFailed  = "seal does not verify"
	ReasonProblem     = "evidence problem"
	ReasonUnreadable  = "experiment unreadable"
	sealProblemPrefix = "seal does not verify"
)

// ErrBadQuery marks a query that cannot be answered as asked. The HTTP
// handler maps it to 400 and the CLI to a usage error.
var ErrBadQuery = errors.New("bad clusters query")

// ErrNotFound marks a signature that is not in the filtered set.
var ErrNotFound = fmt.Errorf("cluster: %w", schemas.ErrNotFound)

// Source is the verified read side of the run folder.
type Source interface {
	Experiments() []string
	Manifest(expID string) (*schemas.Manifest, bool)
	// VerifiedRuns returns the counted sealed runs of an experiment, each
	// checked against its seal; a run that does not verify carries the
	// reason in Problems (store.FSStore.SealedRuns with the original grade).
	VerifiedRuns(ctx context.Context, expID string) ([]schemas.SealedRun, error)
	// RunEvidence returns one of the evidence files (FileEvents, FileLedger,
	// FileTools, FileStderr) of a run. A missing file is an error wrapping
	// fs.ErrNotExist or schemas.ErrNotFound.
	RunEvidence(runID, name string) ([]byte, error)
}

// Query selects the runs to cluster. Zero fields match everything; Min 0
// lists every cluster; Synthetic "" is exclude.
type Query struct {
	Suite       string    // suite id or id@version
	Agent       string    // agent config id, or a config hash prefix of 6 or more characters
	Experiments []string  // experiment ids
	Since       string    // RFC3339 time, a date (2026-11-01), or an age (24h, 7d)
	Min         int       // smallest cluster listed
	Synthetic   string    // exclude | include | only
	Now         time.Time // reference for an age; zero is time.Now
}

// Filters echoes the query a report answered.
type Filters struct {
	Suite       string   `json:"suite"`
	Agent       string   `json:"agent"`
	Experiments []string `json:"experiments"`
	Since       string   `json:"since"`
	Min         int      `json:"min"`
	Synthetic   string   `json:"synthetic"`
}

// Report is the cluster list.
type Report struct {
	GeneratedAt time.Time   `json:"generated_at"`
	Filters     Filters     `json:"filters"`
	Runs        int         `json:"runs"`     // verified runs in scope, passing included
	Failures    int         `json:"failures"` // non-passing runs among them
	Experiments []string    `json:"experiments"`
	Clusters    []Cluster   `json:"clusters"`
	Hidden      int         `json:"hidden"` // clusters below Min, not listed
	Excluded    []Exclusion `json:"excluded"`
	all         []Cluster   // every cluster, below Min included
}

// Cluster is the runs that share one signature.
type Cluster struct {
	Signature    string    `json:"signature"`
	Title        string    `json:"title"`
	Outcome      string    `json:"outcome"`
	Layers       []Layer   `json:"layers"`
	Count        int       `json:"count"`
	Share        float64   `json:"share"`
	Tasks        []string  `json:"tasks"`
	Agents       []string  `json:"agents"`
	ConfigHashes []string  `json:"config_hashes"`
	Conditions   []string  `json:"conditions"`
	Experiments  []string  `json:"experiments"`
	FirstSeen    time.Time `json:"first_seen"`
	LastSeen     time.Time `json:"last_seen"`
	Examples     []string  `json:"examples"`
	// ExampleRuns are the Examples with their benchmark and task, so a
	// page can name the task instead of the run id (fix wave X-25).
	ExampleRuns       []ExampleRun  `json:"example_runs,omitempty"`
	Scope             string        `json:"scope"` // single_run | agent_and_task | agent | task | systemic
	AfterConfigChange *ConfigChange `json:"after_config_change,omitempty"`
	Related           []Related     `json:"related,omitempty"`
	ErrorText         string        `json:"error_text,omitempty"`
	runs              []RunRef
}

// RunRef is one run of a cluster, for the detail view.
type RunRef struct {
	RunID           string    `json:"run_id"`
	PairID          string    `json:"pair_id,omitempty"` // for the run page link (/runs?pair=)
	ExpID           string    `json:"exp_id"`
	Suite           string    `json:"suite"`
	Task            string    `json:"task"`
	Arm             string    `json:"arm"`
	Condition       string    `json:"condition"`
	Agent           string    `json:"agent"`
	ConfigHash      string    `json:"config_hash"`
	Outcome         string    `json:"outcome"`
	At              time.Time `json:"at"`
	LastTool        string    `json:"last_tool,omitempty"`
	LastToolErrored *bool     `json:"last_tool_errored,omitempty"`
	ErrorText       string    `json:"error_text,omitempty"`
}

// Related points at a cluster with the same failed set and a different
// signature: a merge a reader may make, never made automatically.
type Related struct {
	Signature string `json:"signature"`
	Title     string `json:"title"`
	Count     int    `json:"count"`
	Reason    string `json:"reason"`
}

// ConfigChange says a cluster appeared only after an agent config changed.
type ConfigChange struct {
	Agent    string    `json:"agent"`
	FromHash string    `json:"from_hash"`
	ToHash   string    `json:"to_hash"`
	At       time.Time `json:"at"`
}

// Exclusion counts runs left out, by reason.
type Exclusion struct {
	Reason string `json:"reason"`
	Runs   int    `json:"runs"`
}

// Detail is one cluster with its runs.
type Detail struct {
	GeneratedAt time.Time `json:"generated_at"`
	Filters     Filters   `json:"filters"`
	Failures    int       `json:"failures"`
	Cluster     Cluster   `json:"cluster"`
	Runs        []RunRef  `json:"runs"`
}

// Build clusters the non-passing runs the query selects. Only counted,
// sealed runs whose seal verifies enter; a run with any evidence problem is
// left out and counted in Excluded. Clusters are sorted by count, then by
// signature, and Min hides the small ones (counted in Hidden).
func Build(ctx context.Context, src Source, q Query) (*Report, error) {
	q, since, err := normalize(q)
	if err != nil {
		return nil, err
	}
	sc, err := collect(ctx, src, q, since)
	if err != nil {
		return nil, err
	}
	all := group(sc)
	if sc.experiments == nil {
		sc.experiments = []string{}
	}
	rep := &Report{GeneratedAt: q.Now.UTC(), Filters: filtersOf(q), Runs: sc.runs, Failures: len(sc.failed),
		Experiments: sc.experiments, Excluded: sc.exclusions(), all: all, Clusters: []Cluster{}}
	for _, c := range all {
		if c.Count < q.Min {
			rep.Hidden++
			continue
		}
		rep.Clusters = append(rep.Clusters, c)
	}
	return rep, nil
}

// Find returns one cluster of a report with its runs, whatever Min hid, or
// ErrNotFound.
func Find(rep *Report, signature string) (*Detail, error) {
	for _, c := range rep.all {
		if c.Signature == signature {
			return &Detail{GeneratedAt: rep.GeneratedAt, Filters: rep.Filters, Failures: rep.Failures, Cluster: c, Runs: slices.Clone(c.runs)}, nil
		}
	}
	return nil, fmt.Errorf("%s: %w", signature, ErrNotFound)
}

// scored is one non-passing run with its features and signature.
type scored struct {
	ref    RunRef
	f      Features
	layers []Layer
	sig    string
	expID  string
	hash   string
	agent  string
}

// scope is what collect read: every verified run in scope (for config
// change detection), the non-passing ones, and the exclusions.
type scope struct {
	runs        int
	failed      []scored
	seen        []RunRef // every verified run in scope, passing included
	experiments []string
	excluded    map[string]int
}

// collect reads every experiment the query selects and scores its
// non-passing verified runs.
func collect(ctx context.Context, src Source, q Query, since time.Time) (*scope, error) {
	sc := &scope{excluded: map[string]int{}}
	ids := slices.Clone(src.Experiments())
	slices.Sort(ids)
	for _, id := range ids {
		if len(q.Experiments) > 0 && !slices.Contains(q.Experiments, id) {
			continue
		}
		m, ok := src.Manifest(id)
		if !ok || m == nil || !suiteMatches(q.Suite, m) {
			continue
		}
		runs, err := src.VerifiedRuns(ctx, id)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		if err != nil {
			sc.excluded[ReasonUnreadable]++
			continue
		}
		read := false
		for _, r := range runs {
			ok, err := sc.add(src, q, since, m, r)
			if err != nil {
				return nil, err
			}
			read = read || ok
		}
		if read {
			sc.experiments = append(sc.experiments, id)
		}
	}
	return sc, nil
}

// add scores one verified run when the query selects it, and reports
// whether it entered.
func (sc *scope) add(src Source, q Query, since time.Time, m *schemas.Manifest, r schemas.SealedRun) (bool, error) {
	ref := refOf(m, r)
	if !agentMatches(q.Agent, ref) || (!since.IsZero() && ref.At.Before(since)) {
		return false, nil
	}
	if len(r.Problems) > 0 {
		if strings.HasPrefix(r.Problems[0], sealProblemPrefix) {
			sc.excluded[ReasonSealFailed]++
		} else {
			sc.excluded[ReasonProblem]++
		}
		return false, nil
	}
	in := Input{Run: r}
	ledger, err := runEvidence(src, r, FileLedger)
	if err != nil {
		return false, err
	}
	in.Ledger = parseJSONL[schemas.LedgerEntry](ledger)
	synthetic := Synthetic(m, r, in.Ledger)
	if (q.Synthetic == SyntheticExclude && synthetic) || (q.Synthetic == SyntheticOnly && !synthetic) {
		sc.excluded[ReasonSynthetic]++
		return false, nil
	}
	sc.runs++
	sc.seen = append(sc.seen, ref)
	if r.Run.Outcome == schemas.OutcomePass {
		return true, nil
	}
	if err := readInput(src, &in); err != nil {
		return false, err
	}
	f := Extract(in)
	layers := Layers(f)
	if f.LastTool != nil {
		ref.LastTool, ref.LastToolErrored = f.LastTool.Name, f.LastTool.Errored
	}
	if f.Error != nil {
		ref.ErrorText = f.Error.Text
	}
	sc.failed = append(sc.failed, scored{ref: ref, f: f, layers: layers, sig: Signature(layers), expID: m.ExpID, hash: ref.ConfigHash, agent: ref.Agent})
	return true, nil
}

// exclusions lists the excluded counts sorted by reason.
func (sc *scope) exclusions() []Exclusion {
	out := make([]Exclusion, 0, len(sc.excluded))
	for reason, n := range sc.excluded {
		out = append(out, Exclusion{Reason: reason, Runs: n})
	}
	slices.SortFunc(out, func(x, y Exclusion) int { return cmp.Compare(x.Reason, y.Reason) })
	return out
}

// readInput reads the events, tool ledger and stderr of a run into in.
func readInput(src Source, in *Input) error {
	events, err := runEvidence(src, in.Run, FileEvents)
	if err != nil {
		return err
	}
	in.Events = parseJSONL[schemas.Event](events)
	sortEvents(in.Events)
	tools, err := runEvidence(src, in.Run, FileTools)
	if err != nil {
		return err
	}
	in.Tools = parseJSONL[schemas.ToolCall](tools)
	slices.SortStableFunc(in.Tools, func(x, y schemas.ToolCall) int { return cmp.Compare(x.Seq, y.Seq) })
	stderr, err := runEvidence(src, in.Run, FileStderr)
	if err != nil {
		return err
	}
	in.Stderr = string(stderr)
	return nil
}

// readEvidence reads one evidence file; a missing one is nil with no error.
func readEvidence(src Source, runID, name string) ([]byte, error) {
	data, err := src.RunEvidence(runID, name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, schemas.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s of run %s: %w", name, runID, err)
	}
	return data, nil
}

// refOf builds a run's reference from its sealed record and manifest.
func refOf(m *schemas.Manifest, r schemas.SealedRun) RunRef {
	ref := RunRef{RunID: r.Run.RunID, PairID: cmp.Or(r.Slot.PairID, r.Run.PairID), ExpID: cmp.Or(r.Slot.ExpID, r.Run.ExpID, m.ExpID), Suite: cmp.Or(r.Slot.Suite, m.SuiteID),
		Task: r.Slot.Task, Arm: r.Slot.Arm, Condition: r.Slot.Condition, Agent: r.Run.Agent.ConfigID,
		ConfigHash: r.Run.Agent.ConfigHash, Outcome: string(r.Run.Outcome), At: r.Run.Timing.Started}
	if arm := armOf(m, r.Slot.Arm); arm != nil {
		ref.ConfigHash = cmp.Or(arm.ConfigHash, ref.ConfigHash)
		ref.Agent = cmp.Or(ref.Agent, arm.Config.ID)
	}
	if ref.Agent == "" {
		ref.Agent = r.Slot.Arm
	}
	if ref.Outcome == "" {
		ref.Outcome = string(schemas.OutcomeUnknown)
	}
	return ref
}

// normalize fills the query defaults and parses Since. Its error wraps
// ErrBadQuery.
func normalize(q Query) (Query, time.Time, error) {
	q.Suite, q.Agent, q.Since = strings.TrimSpace(q.Suite), strings.TrimSpace(q.Agent), strings.TrimSpace(q.Since)
	q.Synthetic = cmp.Or(strings.TrimSpace(q.Synthetic), SyntheticExclude)
	if q.Now.IsZero() {
		q.Now = time.Now()
	}
	var exps []string
	for _, e := range q.Experiments {
		for _, part := range strings.Split(e, ",") {
			if part = strings.TrimSpace(part); part != "" && !slices.Contains(exps, part) {
				exps = append(exps, part)
			}
		}
	}
	slices.Sort(exps)
	q.Experiments = exps
	switch {
	case q.Synthetic != SyntheticExclude && q.Synthetic != SyntheticInclude && q.Synthetic != SyntheticOnly:
		return q, time.Time{}, fmt.Errorf("%w: synthetic %q is not exclude, include or only", ErrBadQuery, q.Synthetic)
	case q.Min < 0:
		return q, time.Time{}, fmt.Errorf("%w: min %d is negative", ErrBadQuery, q.Min)
	case q.Agent != "" && strings.ContainsAny(q.Agent, " /"):
		return q, time.Time{}, fmt.Errorf("%w: agent %q is not a config id or hash", ErrBadQuery, q.Agent)
	}
	since, err := parseSince(q.Since, q.Now)
	if err != nil {
		return q, time.Time{}, err
	}
	return q, since, nil
}

// parseSince reads an RFC3339 time, a date, or an age in hours (24h) or
// days (7d) before now. "" is the zero time.
func parseSince(s string, now time.Time) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	if n, err := strconv.Atoi(strings.TrimSuffix(s, "d")); err == nil && strings.HasSuffix(s, "d") && n >= 0 {
		return now.Add(-time.Duration(n) * 24 * time.Hour), nil
	}
	if d, err := time.ParseDuration(s); err == nil && d >= 0 {
		return now.Add(-d), nil
	}
	return time.Time{}, fmt.Errorf("%w: since %q is not an RFC3339 time, a date (2006-01-02) or an age (24h, 7d)", ErrBadQuery, s)
}

// suiteMatches reports whether a manifest's suite matches the filter: the
// suite id, or id@version. A manifest without the resolved suite fields
// falls back to the experiment's own "id@version".
func suiteMatches(filter string, m *schemas.Manifest) bool {
	if filter == "" {
		return true
	}
	suiteID, suiteVersion := m.SuiteID, m.SuiteVersion
	if suiteID == "" {
		suiteID, suiteVersion, _ = strings.Cut(m.Experiment.Suite, "@")
	}
	if id, version, found := strings.Cut(filter, "@"); found {
		return id == suiteID && version == suiteVersion
	}
	return filter == suiteID
}

// agentMatches reports whether a run's agent matches the filter: its config
// id, or a config hash prefix of 6 or more characters.
func agentMatches(filter string, ref RunRef) bool {
	if filter == "" {
		return true
	}
	if filter == ref.Agent {
		return true
	}
	return len(filter) >= 6 && strings.HasPrefix(ref.ConfigHash, filter)
}

// filtersOf echoes a normalized query.
func filtersOf(q Query) Filters {
	exps := q.Experiments
	if exps == nil {
		exps = []string{}
	}
	return Filters{Suite: q.Suite, Agent: q.Agent, Experiments: exps, Since: q.Since, Min: q.Min, Synthetic: q.Synthetic}
}
