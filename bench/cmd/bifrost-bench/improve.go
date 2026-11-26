package main

// Improvement runs (bench/improve, docs/improvement-runs.md): the CLI
// command, the engine-backed improve.Executor and the serve source.
//
//	bifrost-bench improve create -f improvements/x.yaml [-run]
//	bifrost-bench improve run <id>
//	bifrost-bench improve list | status <id> [-json]
//	bifrost-bench improve approve <id> -round N -reason "..."
//	bifrost-bench improve stop <id>
//	bifrost-bench improve gate -suite s@v -tasks a,b -incumbent agents/x.yaml@v3 -candidate agents/x.yaml@v4

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"

	"github.com/maximhq/bifrost/bench/agentversions"
	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/improve"
	"github.com/maximhq/bifrost/bench/schedule"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
	"gopkg.in/yaml.v3"
)

func init() {
	register("improve", "improvement runs: the agent improves its own prompt, notes and skills over rounds, each commit gated on held-out tasks: improve create|run|list|status|approve|stop|gate", runImprove)
}

const improveUsage = `usage:
  bifrost-bench improve create -f <spec.yaml> [-run]
  bifrost-bench improve run <id>
  bifrost-bench improve list [-json]
  bifrost-bench improve status <id> [-json]
  bifrost-bench improve approve <id> -round <n> -reason "<why>"
  bifrost-bench improve stop <id>
  bifrost-bench improve gate -suite <id@version> -tasks a,b,... -incumbent <agent ref> -candidate <agent ref> [-trials n] [-alpha a]
  every subcommand takes -config <bench.yaml>`

// improverBundleFile is the artifact the improver task leaves: {"files":
// {"<container path>": "<text>"}}, the full new bundle.
const improverBundleFile = "bundle.json"

// improverEditsFile is the artifact the default improver task leaves: the
// edits of A3.3.
const improverEditsFile = "edits.json"

// Paths inside the improver's container: the current bundle and the dev
// evidence, as files of its agent config.
const (
	improverDir          = "/heimdall/improve"
	improverEvidencePath = improverDir + "/evidence.json"
	improverBundlePath   = improverDir + "/bundle.json"
	improverLimitsPath   = improverDir + "/limits.json"
)

func runImprove(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, improveUsage)
		return exitUsage
	}
	fs := flag.NewFlagSet("improve "+args[0], flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	config := fs.String("config", defaultConfig, "bench config")
	asJSON := fs.Bool("json", false, "print JSON")
	file := fs.String("f", "", "improvement spec file")
	andRun := fs.Bool("run", false, "run right after creating")
	round := fs.Int("round", 0, "round to approve")
	reason := fs.String("reason", "", "why the tripwire is overridden")
	suiteRef := fs.String("suite", "", "benchmark id@version")
	tasks := fs.String("tasks", "", "comma-separated task ids")
	inc := fs.String("incumbent", "", "incumbent agent ref")
	cand := fs.String("candidate", "", "candidate agent ref")
	trials := fs.Int("trials", 1, "trials per task")
	alpha := fs.Float64("alpha", improve.DefaultAlpha, "level of the commit test")
	pos, err := parseInterspersed(fs, args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, improveUsage)
		return exitUsage
	}
	sub := args[0]
	needID := sub == "run" || sub == "status" || sub == "approve" || sub == "stop"
	if needID != (len(pos) == 1) || sub == "create" && *file == "" {
		fmt.Fprintln(os.Stderr, improveUsage)
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	gateway := sub == "create" && *andRun || sub == "run" || sub == "approve" || sub == "gate"
	a, err := newApp(ctx, *config, appOptions{gateway: gateway})
	if err != nil {
		return fail("%v", err)
	}
	r := newImproveRunner(a)
	out := os.Stdout
	switch sub {
	case "create":
		b, err := os.ReadFile(*file)
		if err != nil {
			return fail("%v", err)
		}
		spec, err := improve.ParseSpec(b)
		if err != nil {
			return fail("%v", err)
		}
		rec, err := r.Create(ctx, spec, improveActor())
		if err != nil {
			return fail("%v", err)
		}
		fmt.Fprintf(out, "created %s: %d dev, %d audit, %d sealed tasks\n", rec.ID, len(rec.Splits.Dev), len(rec.Splits.Audit), len(rec.Splits.Sealed))
		if !*andRun {
			return exitOK
		}
		return improveDrive(ctx, r, rec.ID, out, *asJSON)
	case "run":
		return improveDrive(ctx, r, pos[0], out, *asJSON)
	case "list":
		recs, err := r.List()
		if err != nil {
			return fail("%v", err)
		}
		l := improve.List{Improvements: []improve.Summary{}}
		for _, rec := range recs {
			l.Improvements = append(l.Improvements, improve.Summarize(rec, false))
		}
		if *asJSON {
			return printJSONOut(out, l)
		}
		for _, s := range l.Improvements {
			fmt.Fprintf(out, "%-40s %-18s %d/%d rounds  %d commits  %s\n", s.ID, s.Status, s.RoundsDone, s.Rounds, s.Commits, s.Reason)
		}
		return exitOK
	case "status":
		rec, err := r.Get(pos[0])
		if err != nil {
			return fail("%v", err)
		}
		return printImprovement(out, r, rec, *asJSON)
	case "approve":
		rec, err := r.Approve(pos[0], *round, improveActor(), *reason)
		if err != nil {
			return fail("%v", err)
		}
		fmt.Fprintf(out, "%s round %d approved; continuing\n", rec.ID, *round)
		return improveDrive(ctx, r, rec.ID, out, *asJSON)
	case "stop":
		rec, err := r.Stop(pos[0], improveActor())
		if err != nil {
			return fail("%v", err)
		}
		fmt.Fprintf(out, "%s stopped\n", rec.ID)
		return exitOK
	case "gate":
		return improveGateCmd(ctx, a, out, *suiteRef, *tasks, *inc, *cand, *trials, *alpha, *asJSON)
	}
	fmt.Fprintf(os.Stderr, "unknown improve subcommand %q\n%s\n", sub, improveUsage)
	return exitUsage
}

func improveDrive(ctx context.Context, r *improve.Runner, id string, out io.Writer, asJSON bool) int {
	rec, err := r.Run(ctx, id)
	if rec != nil {
		if code := printImprovement(out, r, rec, asJSON); code != exitOK {
			return code
		}
	}
	if err != nil {
		return fail("%v", err)
	}
	if rec.Status == improve.StatusAwaiting {
		return 2
	}
	return exitOK
}

func printImprovement(out io.Writer, r *improve.Runner, rec *improve.Record, asJSON bool) int {
	d := r.View(rec)
	if asJSON {
		return printJSONOut(out, d)
	}
	fmt.Fprintf(out, "%s (%s): %s\n  %s\n", d.ID, d.Status, d.Name, d.Reason)
	for _, p := range d.Curve {
		fmt.Fprintf(out, "  round %d  %-9s dev %s  audit %s  sealed %s\n", p.Round, p.Event, improvePct(p.Dev), improvePct(p.Audit), improvePct(p.Sealed))
	}
	for _, rd := range d.Rounds {
		fmt.Fprintf(out, "  round %d %s: %s\n", rd.N, improveOr(rd.Decision, rd.Stage), rd.Why)
		if len(rd.Tripwires) > 0 {
			fmt.Fprintf(out, "    tripwires: %s\n", strings.Join(rd.Tripwires, ", "))
		}
	}
	if d.Band != nil {
		fmt.Fprintf(out, "  control band %.0f%% to %.0f%% (version 0, %d trials)\n", 100*d.Band.Low, 100*d.Band.High, d.Band.Trials)
	}
	fmt.Fprintf(out, "  spend %.4f USD; lineage chain ok: %v\n", d.Cost.Total, d.Lineage.ChainOK)
	return exitOK
}

func improvePct(p *float64) string {
	if p == nil {
		return "  -"
	}
	return fmt.Sprintf("%3.0f%%", 100**p)
}

func improveOr(a, b string) string {
	if a == "" {
		return b
	}
	return a
}

func printJSONOut(out io.Writer, v any) int {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fail("%v", err)
	}
	return exitOK
}

// improveActor names the shell user in an improvement run's record.
func improveActor() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "local"
}

// improveGateCmd is the commit gate on its own: any two versions, paired on
// the given tasks, an anytime-valid e-value test.
func improveGateCmd(ctx context.Context, a *app, out io.Writer, suiteRef, tasks, inc, cand string, trials int, alpha float64, asJSON bool) int {
	if suiteRef == "" || tasks == "" || inc == "" || cand == "" {
		fmt.Fprintln(os.Stderr, improveUsage)
		return exitUsage
	}
	x := improveExecutor{a: a}
	ed := improve.Editable{}
	vi, err := x.Load(ctx, inc, ed)
	if err != nil {
		return fail("%v", err)
	}
	vc, err := x.Load(ctx, cand, ed)
	if err != nil {
		return fail("%v", err)
	}
	ids := strings.Split(tasks, ",")
	spec := &improve.Spec{Name: "gate", Suite: suiteRef, Seed: 1}
	res, err := x.Score(ctx, improve.ScoreRequest{ImprovementID: "gate", Spec: spec, Phase: improve.PhaseAudit, Arms: []improve.Version{vi, vc}, Tasks: ids, Trials: trials})
	if err != nil {
		return fail("%v", err)
	}
	ev := improve.GateTest(res.Runs, ids, alpha, improve.DefaultMinPairs, 0)
	if asJSON {
		return printJSONOut(out, map[string]any{"exp_id": res.ExpID, "e_value": ev})
	}
	verdict := "keep the incumbent"
	if ev.Better {
		verdict = "commit the candidate"
	}
	fmt.Fprintf(out, "%s: e-value %.2f (threshold %.0f) after %d pairs: %s\n", res.ExpID, ev.MaxEUp, ev.Threshold, ev.Pairs, verdict)
	if ev.Better {
		return exitOK
	}
	return 1
}

// improveOnEvent builds the runner's event hook (notifications); nil sends
// nothing. Set by the file that sends improvement events.
var improveOnEvent func(a *app) func(kind string, rec *improve.Record)

// newImproveRunner wires the runner over the runs root.
func newImproveRunner(a *app) *improve.Runner {
	r := &improve.Runner{Root: filepath.Join(a.store.Root(), improve.DefaultDir), Exec: improveExecutor{a: a}, Logger: a.logger,
		Objects: improve.Objects{Root: filepath.Join(a.store.Root(), improve.DefaultDir, "objects")}}
	if improveOnEvent != nil {
		r.OnEvent = improveOnEvent(a)
	}
	return r
}

// improveExecutor implements improve.Executor with the agent version store,
// the planner, the scheduler and the run store.
type improveExecutor struct {
	a *app
	// hosted runs every experiment through the org's hosted launcher (own
	// keys, daily caps, the org's default worker, runs on runners only); nil
	// runs them in this process (local serve and the CLI).
	hosted *launcher
}

// Load implements improve.Executor: a pinned ref reads its snapshot; a
// plain agent file is snapshotted first, so version 0 is frozen.
func (x improveExecutor) Load(_ context.Context, ref string, ed improve.Editable) (improve.Version, error) {
	doc, id, n, err := x.readAgent(ref)
	if err != nil {
		return improve.Version{}, err
	}
	if n > 0 {
		return improve.NewVersion(ref, n, doc, ed)
	}
	return x.snapshot(id, doc, ed)
}

// readAgent reads an agent reference's document without storing anything:
// a pinned ref returns its version number, a plain file (or a library
// agent not yet stored) returns 0.
func (x improveExecutor) readAgent(ref string) ([]byte, string, int, error) {
	root := x.a.cfg.Root
	if _, id, n, ok := schemas.ParseAgentVersionRef(ref); ok {
		doc, _, err := agentversions.Read(root, id, n)
		if errors.Is(err, agentversions.ErrNotFound) && n == 1 {
			if p := agentversions.LibraryFile(suite.LibraryDir(), id); p != "" {
				if doc, err = os.ReadFile(p); err == nil {
					return doc, id, 0, nil
				}
			}
		}
		if err != nil {
			return nil, "", 0, err
		}
		return doc, id, n, nil
	}
	id, _, err := agentRef(ref)
	if err != nil {
		return nil, "", 0, err
	}
	doc, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ref)))
	if errors.Is(err, fs.ErrNotExist) {
		if p := agentversions.LibraryFile(suite.LibraryDir(), id); p != "" {
			doc, err = os.ReadFile(p)
		}
	}
	if err != nil {
		return nil, "", 0, fmt.Errorf("read %s: %w", ref, err)
	}
	return doc, id, 0, nil
}

func (x improveExecutor) snapshot(id string, doc []byte, ed improve.Editable) (improve.Version, error) {
	v, _, err := agentversions.Snapshot(x.a.cfg.Root, id, doc, "improvement", "version 0 of an improvement run")
	if err != nil {
		return improve.Version{}, err
	}
	b, _, err := agentversions.Read(x.a.cfg.Root, id, v.Version)
	if err != nil {
		return improve.Version{}, err
	}
	return improve.NewVersion(versionRef(id, v.Version), v.Version, b, ed)
}

func versionRef(id string, n int) string { return fmt.Sprintf("agents/%s.yaml@v%d", id, n) }

// Freeze implements improve.Executor.
func (x improveExecutor) Freeze(_ context.Context, parent improve.Version, doc []byte, lin improve.Lineage) (improve.Version, error) {
	_, id, _, ok := schemas.ParseAgentVersionRef(parent.Ref)
	if !ok {
		return improve.Version{}, fmt.Errorf("parent %s is not a pinned version", parent.Ref)
	}
	v, _, err := agentversions.Derive(x.a.cfg.Root, id, doc, schemas.AgentVersion{Parent: parent.Number, Author: schemas.AuthorAgent,
		Improvement: lin.ImprovementID, Round: lin.Round, ProducedByRun: lin.ProducedByRun, BundleHash: lin.BundleHash,
		CreatedBy: "improvement " + lin.ImprovementID, Note: lin.Note})
	if err != nil {
		return improve.Version{}, err
	}
	b, _, err := agentversions.Read(x.a.cfg.Root, id, v.Version)
	if err != nil {
		return improve.Version{}, err
	}
	nv, err := improve.NewVersion(versionRef(id, v.Version), v.Version, b, lin.Editable)
	if err != nil {
		return nv, err
	}
	if nv.BundleHash != lin.BundleHash {
		return improve.Version{}, fmt.Errorf("frozen bundle %s does not match the applied bundle %s", nv.BundleHash, lin.BundleHash)
	}
	obj, err := improve.BundleObjectOf(b, nv.Bundle)
	if err != nil {
		return improve.Version{}, err
	}
	if nv.BundleObj, err = x.objects().Put(improve.ObjBundle, obj); err != nil {
		return improve.Version{}, fmt.Errorf("store the bundle of %s: %w", nv.Ref, err)
	}
	return nv, nil
}

// objects is the improvement runs' content-addressed store.
func (x improveExecutor) objects() improve.Objects {
	return improve.Objects{Root: filepath.Join(x.a.store.Root(), improve.DefaultDir, "objects")}
}

// Tasks implements improve.Executor.
func (x improveExecutor) Tasks(ctx context.Context, spec *improve.Spec) ([]improve.TaskInfo, error) {
	s, err := engine.LoadSuite(ctx, x.a.reg, spec.Suite)
	if err != nil {
		return nil, err
	}
	out := []improve.TaskInfo{}
	for _, t := range s.Tasks {
		if len(spec.Tasks) > 0 && !slices.Contains(spec.Tasks, t.ID) && !slices.Contains(spec.Splits.Honeypots, t.ID) {
			continue
		}
		out = append(out, improve.TaskInfo{ID: t.ID, Impossible: t.Impossible})
	}
	return out, nil
}

// Protect implements improve.Executor: task ids, the hidden tests and
// expected answers, required test names and the suite canary.
func (x improveExecutor) Protect(ctx context.Context, spec *improve.Spec, tasks []string) (improve.Protected, error) {
	s, err := engine.LoadSuite(ctx, x.a.reg, spec.Suite)
	if err != nil {
		return improve.Protected{}, err
	}
	p := improve.Protected{TaskIDs: slices.Clone(tasks)}
	if s.Canary != "" {
		p.Canaries = append(p.Canaries, s.Canary)
	}
	budget := protectMaxBytes
	for _, t := range s.Tasks {
		if !slices.Contains(tasks, t.ID) {
			continue
		}
		p.Secrets = append(p.Secrets, t.Verifier.Required...)
		for _, rel := range []string{t.Verifier.Tests, t.Verifier.Answer} {
			if rel == "" || t.Dir == "" {
				continue
			}
			budget = readSecrets(filepath.Join(t.Dir, filepath.FromSlash(rel)), budget, &p.Secrets)
		}
	}
	return p, nil
}

// protectMaxBytes caps the protected text read per improvement.
const protectMaxBytes = 8 << 20

// readSecrets appends the text files under path (a file or a folder) until
// budget bytes are read; it returns the budget left.
func readSecrets(path string, budget int, out *[]string) int {
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || budget <= 0 {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil || bytes.IndexByte(b, 0) >= 0 {
			return nil
		}
		if len(b) > budget {
			b = b[:budget]
		}
		budget -= len(b)
		*out = append(*out, string(b))
		return nil
	})
	return budget
}

// Score implements improve.Executor: one experiment, arms A (and B) on the
// tasks, read back from the sealed runs.
func (x improveExecutor) Score(ctx context.Context, req improve.ScoreRequest) (*improve.ScoreResult, error) {
	if len(req.Arms) == 0 || len(req.Arms) > improve.MaxScoreArms {
		return nil, fmt.Errorf("a scoring experiment holds 1 to %d versions, not %d", improve.MaxScoreArms, len(req.Arms))
	}
	exp := improveExperiment(req.ImprovementID, req.Phase, req.Round, req.Spec.Suite, req.Spec.Seed, req.Tasks, req.Trials)
	for i, v := range req.Arms {
		exp.Arms[improveArm(i)] = schemas.ArmSpec{Label: v.Ref, Agent: v.Ref}
	}
	exp.Base = req.Arms[0].Ref
	expID, cost, err := x.execute(ctx, exp)
	if err != nil {
		return nil, err
	}
	res := &improve.ScoreResult{ExpID: expID, CostUSD: cost, Runs: []improve.RunScore{}, Channels: make([][]string, len(req.Arms))}
	runs, err := x.a.store.SealedRuns(ctx, expID, store.GradeSourceOriginal)
	if err != nil {
		return nil, fmt.Errorf("load sealed runs of %s: %w", expID, err)
	}
	for _, sr := range runs {
		rs, ok := improveRunScore(sr, len(req.Arms))
		if !ok {
			continue
		}
		arm := rs.Arm
		res.Runs = append(res.Runs, rs)
		res.Channels[arm] = mergeChannels(res.Channels[arm], runChannels(sr))
	}
	return res, nil
}

// improveRunScore reads one counted run of an experiment with arms
// letters A up to the n-th; ok is false for a run not counted or of
// another arm.
func improveRunScore(sr schemas.SealedRun, n int) (improve.RunScore, bool) {
	arm := improveArmIndex(sr.Slot.Arm)
	if !sr.Slot.Counted || arm < 0 || arm >= n {
		return improve.RunScore{}, false
	}
	rs := improve.RunScore{Arm: arm, Task: sr.Slot.Task, Trial: sr.Slot.Trial, Outcome: string(sr.Run.Outcome), RunID: sr.Run.RunID}
	switch sr.Run.Outcome {
	case schemas.OutcomeTimeout, schemas.OutcomeBudget, schemas.OutcomeCrash, schemas.OutcomeRefusal:
		rs.Stop = string(sr.Run.Outcome)
	}
	if f, ok := sr.Run.Y.Float(); ok && len(sr.Problems) == 0 {
		rs.Success, rs.Known = f, true
	}
	if v, ok := sr.Metrics["cost_usd"]; ok {
		if f, ok := v.Float(); ok {
			rs.CostUSD = f
		}
	}
	for _, g := range sr.Gates {
		if g.Fired && g.Effect == schemas.GateVeto {
			rs.Vetoes = append(rs.Vetoes, g.Gate)
		}
	}
	return rs, true
}

// Runs implements improve.Executor: the counted runs of a scoring
// experiment with their sealed folders, arms as indexes (A is 0).
func (x improveExecutor) Runs(ctx context.Context, expID string) ([]improve.RunRef, error) {
	runs, err := x.a.store.SealedRuns(ctx, expID, store.GradeSourceOriginal)
	if err != nil {
		return nil, fmt.Errorf("load sealed runs of %s: %w", expID, err)
	}
	out := []improve.RunRef{}
	for _, sr := range runs {
		arm := improveArmIndex(sr.Slot.Arm)
		if !sr.Slot.Counted || arm < 0 {
			continue
		}
		out = append(out, improve.RunRef{ExpID: expID, RunID: sr.Run.RunID, Task: sr.Slot.Task, Trial: sr.Slot.Trial, Arm: arm, Dir: sr.Dir})
	}
	return out, nil
}

// improveArmIndex is the index of arm letter A to F, or -1.
func improveArmIndex(letter string) int {
	if len(letter) != 1 || letter[0] < 'A' || int(letter[0]-'A') >= improve.MaxScoreArms {
		return -1
	}
	return int(letter[0] - 'A')
}

// runChannels are the evidence channels a run emitted: event types from
// events.jsonl, plus gateway when the run has gateway rows.
func runChannels(sr schemas.SealedRun) []string {
	out := []string{}
	if sr.Gateway != nil && sr.Gateway.Requests > 0 {
		out = append(out, "gateway")
	}
	f, err := os.Open(filepath.Join(sr.Dir, "events.jsonl"))
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var e struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Type != "" && !slices.Contains(out, "event:"+e.Type) {
			out = append(out, "event:"+e.Type)
		}
	}
	return out
}

func mergeChannels(a, b []string) []string {
	for _, c := range b {
		if !slices.Contains(a, c) {
			a = append(a, c)
		}
	}
	slices.Sort(a)
	return a
}

// Improve implements improve.Executor: the improver is a normal sealed run
// on the improver task. Its agent is the shipped reflector (on the
// incumbent's model, or the spec's teacher) or, with improver.agent self,
// the incumbent. The current bundle, the dev evidence and the limits go
// into that agent's files_raw under /heimdall/improve, so text with "{{"
// never breaks a run, and its run key is capped at the round budget. The
// container mounts no verifier, audit or sealed task and no run store. The
// task's artifact edits.json (or bundle.json with reasons) is the output.
func (x improveExecutor) Improve(ctx context.Context, req improve.ImproveRequest) (*improve.ImproveResult, error) {
	suiteRef, task, err := improverTask(req.Spec)
	if err != nil {
		return nil, err
	}
	base := req.Incumbent.Doc
	if req.Spec.Improver.Agent != "self" {
		if base, err = x.reflectorDoc(); err != nil {
			return nil, err
		}
	}
	doc, err := improverDoc(req, base, x.playbook(req.Incumbent))
	if err != nil {
		return nil, err
	}
	rel := filepath.ToSlash(filepath.Join(".heimdall", "improve", req.ImprovementID, fmt.Sprintf("improver-r%02d%s.yaml", req.Round, map[bool]string{true: "-null"}[req.Null])))
	if err := os.MkdirAll(filepath.Dir(filepath.Join(x.a.cfg.Root, rel)), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(x.a.cfg.Root, filepath.FromSlash(rel)), doc, 0o644); err != nil {
		return nil, err
	}
	exp := improverExperiment(req, suiteRef, task, rel)
	expID, cost, err := x.execute(ctx, exp)
	res := &improve.ImproveResult{ExpID: expID, CostUSD: cost}
	if err != nil {
		return res, err
	}
	runs, err := x.a.store.SealedRuns(ctx, expID, store.GradeSourceOriginal)
	if err != nil || len(runs) == 0 {
		return res, fmt.Errorf("improver run of %s: no sealed run (%v)", expID, err)
	}
	sr := runs[0]
	res.RunID = sr.Run.RunID
	for _, g := range sr.Gates {
		if g.Fired && (g.Gate == "egress" || g.Gate == "oversight_disabled") {
			res.Flags = append(res.Flags, g.Gate)
		}
	}
	if err := readImproverOutput(filepath.Join(sr.Dir, utils.ArtifactsDir), req.Incumbent.Bundle, res); err != nil {
		return res, err
	}
	return res, nil
}

// reflectorAgentID is the shipped improver agent (agents/heimdall-reflector.yaml).
const reflectorAgentID = "heimdall-reflector"

// reflectorDoc reads the reflector agent like any library agent: the
// project's agents folder first, then the built-in library.
func (x improveExecutor) reflectorDoc() ([]byte, error) {
	doc, _, _, err := x.readAgent("agents/" + reflectorAgentID + ".yaml")
	if err != nil {
		return nil, fmt.Errorf("the improver agent %s is not installed: %w", reflectorAgentID, err)
	}
	return doc, nil
}

// playbook is a version's playbook object, or an empty one.
func (x improveExecutor) playbook(v improve.Version) improve.PlaybookObject {
	pb := improve.PlaybookObject{Items: []improve.PlaybookItem{}}
	if v.PlaybookObj != "" && x.a.store != nil {
		_ = x.objects().Get(v.PlaybookObj, improve.ObjPlaybook, &pb)
	}
	return pb
}

// improverExperiment is the one-run experiment of an improver phase. Its
// fairness rule is equal_budget, so the run's gateway key carries the
// improver's USD cap (budgets.usd of the improver document); the planner
// refuses an improver agent that sets its own gateway spending limit.
func improverExperiment(req improve.ImproveRequest, suiteRef, task, rel string) *schemas.Experiment {
	exp := improveExperiment(req.ImprovementID, "improve", req.Round, suiteRef, req.Spec.Seed, []string{task}, 1)
	exp.Base = rel
	exp.Arms[improveArm(0)] = schemas.ArmSpec{Label: "improver"}
	exp.Budget = schemas.BudgetSpec{TotalUSD: req.BudgetUSD}
	exp.Fairness = schemas.FairnessEqualBudget
	return exp
}

// errOwnSpendLimit refuses an agent whose own gateway spending limit would
// replace the improver's cap on the run key (A1.5).
var errOwnSpendLimit = errors.New("This agent sets its own gateway spending limit; remove gateway.routing.limits.budget_usd so the improver's cap can be enforced.")

// agentSetsSpendLimit reports whether an agent document sets
// gateway.routing.limits.budget_usd.
func agentSetsSpendLimit(doc []byte) bool {
	var cfg schemas.AgentConfig
	if yaml.Unmarshal(doc, &cfg) != nil {
		return false
	}
	r := cfg.Gateway.Routing
	return r != nil && r.Limits != nil && r.Limits.BudgetUSD > 0
}

// CheckImprover implements improve.Executor (A1.5, A1.6): it refuses a
// spec before any experiment exists when the agent sets its own gateway
// spending limit, when the improver task does not exist, or when the task
// leaves neither edits.json nor bundle.json. The shipped improver suite is
// copied into the project first when this build embeds it.
func (x improveExecutor) CheckImprover(ctx context.Context, spec *improve.Spec) error {
	doc, _, _, err := x.readAgent(spec.Agent)
	if err != nil {
		return err
	}
	if agentSetsSpendLimit(doc) {
		return errOwnSpendLimit
	}
	suiteRef, task, err := improverTask(spec)
	if err != nil {
		return err
	}
	// The shipped suite matters only to a spec that uses it; a custom
	// improver task in another benchmark is checked on its own below.
	if id, _, _ := strings.Cut(suiteRef, "@"); id == path.Base(improverSuiteDir) {
		if err := ensureImproverSuite(x.a.cfg.Root); err != nil {
			return err
		}
	}
	ref := spec.Improver.Task
	missing := fmt.Errorf("The improver task %s does not exist.", ref)
	s, err := engine.LoadSuite(ctx, x.a.reg, suiteRef)
	if err != nil {
		return missing
	}
	for _, t := range s.Tasks {
		if t.ID != task {
			continue
		}
		out := t.Output
		if out == nil {
			out = s.Output
		}
		if out != nil {
			for _, art := range out.Artifacts {
				if b := path.Base(art.Path); b == improverEditsFile || b == improverBundleFile {
					return nil
				}
			}
		}
		return fmt.Errorf("The improver task %s does not leave edits.json or bundle.json, so its output cannot be read.", ref)
	}
	return missing
}

// improverTask is the improver task of a spec: "<task>" in the spec's
// suite or "<suite@version>:<task>".
func improverTask(spec *improve.Spec) (string, string, error) {
	t := strings.TrimSpace(spec.Improver.Task)
	if t == "" {
		return "", "", errors.New("improver.task is required: name a task whose output is the agent bundle (artifact bundle.json), as <task> or <benchmark@version>:<task>")
	}
	if s, task, ok := strings.Cut(t, ":"); ok {
		return s, task, nil
	}
	return spec.Suite, t, nil
}

// Lines added to the improver's standing instructions for a restart slot
// and for the null improver (A3.2).
const (
	improverRestartLine = "This is a restart: you may also rewrite a whole file with rewrite_file, starting from version 0."
	improverNullLine    = "There is no evidence this time; propose the changes you think are best."
)

// improverDoc is the improver agent's config (base: the reflector, or the
// incumbent for improver.agent self) with the improver inputs added as
// files_raw under /heimdall/improve, the model and provider of the
// incumbent (or the teacher) on the reflector, and budgets set to the
// round's cap.
func improverDoc(req improve.ImproveRequest, base []byte, pb improve.PlaybookObject) ([]byte, error) {
	var tree map[string]any
	if err := yaml.Unmarshal(base, &tree); err != nil {
		return nil, err
	}
	if tree == nil {
		tree = map[string]any{}
	}
	if req.Spec.Improver.Agent != "self" {
		var inc schemas.AgentConfig
		if err := yaml.Unmarshal(req.Incumbent.Doc, &inc); err != nil {
			return nil, fmt.Errorf("read the incumbent's model: %w", err)
		}
		model, provider := inc.Model, inc.Gateway.Provider
		if req.Model != "" {
			model, provider = req.Model, req.Provider
		}
		if model != "" {
			tree["model"] = model
		}
		if provider != "" {
			gw, _ := tree["gateway"].(map[string]any)
			if gw == nil {
				gw = map[string]any{}
			}
			gw["provider"] = provider
			tree["gateway"] = gw
		}
	}
	raw, _ := tree["files_raw"].(map[string]any)
	if raw == nil {
		raw = map[string]any{}
	}
	files, _ := tree["files"].(map[string]any)
	put := func(rel string, v []byte) error {
		p := improverDir + "/" + rel
		if _, ok := files[p]; ok {
			return fmt.Errorf("the improver agent already defines %s", p)
		}
		raw[p] = string(v)
		return nil
	}
	lim := req.Limits
	if lim == (improve.Limits{}) {
		lim = improve.DefaultLimits
	}
	limb, err := json.MarshalIndent(lim, "", "  ")
	if err != nil {
		return nil, err
	}
	obj, err := improve.BundleObjectOf(req.Incumbent.Doc, req.Incumbent.Bundle)
	if err != nil {
		return nil, err
	}
	if pb.Items == nil {
		pb.Items = []improve.PlaybookItem{}
	}
	bb, err := json.MarshalIndent(map[string]any{"files": obj.Files, "raw": obj.Raw, "items": pb.Items, "limits": lim}, "", "  ")
	if err != nil {
		return nil, err
	}
	var evb []byte
	switch {
	case req.Null:
		evb, err = json.MarshalIndent(map[string]any{"split": "dev", "round": req.Round, "clusters": []any{}, "items": []any{}}, "", "  ")
	case req.EvidenceDoc != nil && len(req.EvidenceDoc.EvidenceJSON) > 0:
		evb = req.EvidenceDoc.EvidenceJSON
	default:
		ev := req.Evidence
		if ev == nil {
			ev = []improve.EvidenceItem{}
		}
		evb, err = json.MarshalIndent(map[string]any{"split": "dev", "round": req.Round, "results": ev, "editable": req.Spec.Editable}, "", "  ")
	}
	if err != nil {
		return nil, err
	}
	for rel, v := range map[string][]byte{"bundle.json": bb, "evidence.json": evb, "limits.json": limb} {
		if err := put(rel, v); err != nil {
			return nil, err
		}
	}
	if req.EvidenceDoc != nil && !req.Null {
		for rel, v := range req.EvidenceDoc.Files {
			clean := path.Clean(rel)
			if strings.HasPrefix(clean, "/") || clean == "." || strings.HasPrefix(clean, "..") || slices.Contains([]string{"bundle.json", "evidence.json", "limits.json"}, clean) {
				return nil, fmt.Errorf("evidence file %q is not a plain relative path", rel)
			}
			if err := put(clean, v); err != nil {
				return nil, err
			}
		}
	}
	tree["files_raw"] = raw
	var lines []string
	if req.Kind == "restart" {
		lines = append(lines, improverRestartLine)
	}
	if req.Null {
		lines = append(lines, improverNullLine)
	}
	if len(lines) > 0 {
		cur, _ := tree["instructions"].(string)
		if cur = strings.TrimRight(cur, "\n"); cur != "" {
			lines = append([]string{cur}, lines...)
		}
		tree["instructions"] = strings.Join(lines, "\n\n")
	}
	budgets, _ := tree["budgets"].(map[string]any)
	if budgets == nil {
		budgets = map[string]any{}
	}
	if req.BudgetUSD > 0 {
		budgets["usd"] = req.BudgetUSD
	}
	if req.WallS > 0 {
		budgets["wall_s"] = req.WallS
	}
	tree["budgets"] = budgets
	return yaml.Marshal(tree)
}

// readImproverOutput reads the improver's output into res: edits.json (the
// default improver) leaves the bundle as the incumbent's for the edit
// applier; bundle.json is the full new bundle, every changed path with a
// reason.
func readImproverOutput(dir string, incumbent improve.Bundle, res *improve.ImproveResult) error {
	if p := findArtifact(dir, improverEditsFile); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		var e improve.EditsFile
		if err := json.Unmarshal(b, &e); err != nil {
			return fmt.Errorf("the improver's %s is not valid: %w", improverEditsFile, err)
		}
		res.Edits = &e
		res.Bundle = incumbent.Clone()
		if res.Bundle == nil {
			res.Bundle = improve.Bundle{}
		}
		return nil
	}
	files, reasons, summary, err := readBundleArtifact(dir)
	if err != nil {
		return err
	}
	for _, k := range slices.Sorted(maps.Keys(files)) {
		if old, ok := incumbent[k]; (!ok || old != files[k]) && strings.TrimSpace(reasons[k]) == "" {
			return fmt.Errorf("the improver gave no reason for %s", k)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(incumbent)) {
		if _, ok := files[k]; !ok && strings.TrimSpace(reasons[k]) == "" {
			return fmt.Errorf("the improver gave no reason for %s", k)
		}
	}
	res.Bundle, res.Reasons, res.Summary = files, reasons, summary
	return nil
}

// findArtifact is the first file named name among the run's exported
// artifacts, or "".
func findArtifact(dir, name string) string {
	var found string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() && d.Name() == name && found == "" {
			found = p
		}
		return nil
	})
	return found
}

// readBundleArtifact reads bundle.json among the run's exported
// artifacts: the full bundle, a reason per changed path and a summary.
func readBundleArtifact(dir string) (improve.Bundle, map[string]string, string, error) {
	found := findArtifact(dir, improverBundleFile)
	if found == "" {
		return nil, nil, "", fmt.Errorf("the improver left no %s or %s artifact", improverEditsFile, improverBundleFile)
	}
	b, err := os.ReadFile(found)
	if err != nil {
		return nil, nil, "", err
	}
	var out struct {
		Files   map[string]string `json:"files"`
		Reasons map[string]string `json:"reasons"`
		Summary string            `json:"summary"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, nil, "", fmt.Errorf("the improver's %s is not a bundle: %w", improverBundleFile, err)
	}
	if out.Files == nil {
		out.Files = map[string]string{}
	}
	return out.Files, out.Reasons, out.Summary, nil
}

func improveArm(i int) string { return string(rune('A' + i)) }

func improveExperiment(impID, phase string, round int, suiteRef string, seed int64, tasks []string, trials int) *schemas.Experiment {
	e := &schemas.Experiment{
		Name:         fmt.Sprintf("%s-%s-r%d", strings.TrimPrefix(impID, "imp_"), phase, round),
		Suite:        suiteRef,
		Arms:         map[string]schemas.ArmSpec{},
		Verdict:      schemas.VerdictSpec{Policy: "paired_signflip", Alpha: 0.05, AssumedSD: 0.30},
		Seed:         max(1, seed),
		ManifestMode: "agent_exposure",
		Conditions:   []schemas.ConditionSpec{{ID: "clean", Tasks: schemas.TaskSelector{IDs: slices.Clone(tasks)}, Trials: max(1, trials)}},
	}
	e.Primary = schemas.Endpoint{Condition: "clean", Metric: "verified_success"}
	return e
}

// execute plans, freezes and runs one experiment and returns its id and
// spend, as the canary does.
func (x improveExecutor) execute(ctx context.Context, exp *schemas.Experiment) (string, float64, error) {
	if x.hosted != nil {
		return x.hosted.runImprovementExperiment(ctx, exp)
	}
	a := x.a
	m, err := a.newPlanner().PlanCanary(ctx, exp)
	if err != nil {
		return "", 0, fmt.Errorf("plan %s: %w", exp.Name, err)
	}
	if err := a.store.CreateExperiment(ctx, m); err != nil {
		return "", 0, fmt.Errorf("freeze %s: %w", m.ExpID, err)
	}
	defer refreshResultsIndex(context.WithoutCancel(ctx), a, m.ExpID)
	res := &schedule.Result{ExpID: m.ExpID}
	sum, runErr := scheduleRunner{a: a}.execute(ctx, m)
	if sum != nil {
		res.CostUSD = sum.SpentUSD
	}
	scheduleRunner{a: a}.tally(ctx, res)
	if runErr != nil {
		return m.ExpID, res.CostUSD, fmt.Errorf("run %s: %w", m.ExpID, runErr)
	}
	return m.ExpID, res.CostUSD, nil
}

// improveSource serves improvements to the API (handlers.ImproveSource).
type improveSource struct {
	r       *improve.Runner
	offline bool
	mu      *sync.Mutex
}

// serveImprovements builds the API source; offline servers read only.
func serveImprovements(a *app, offline bool) improveSource {
	return improveSource{r: newImproveRunner(a), offline: offline, mu: &sync.Mutex{}}
}

// serveHostedImprovements is the source of a hosted org: improvement runs
// may start (org admins, tenancy/policy_improve.go), and each of their
// experiments goes through the org's launcher like any other org run.
func serveHostedImprovements(a *app, l *launcher) improveSource {
	r := newImproveRunner(a)
	r.Exec = improveExecutor{a: a, hosted: l}
	return improveSource{r: r, mu: &sync.Mutex{}}
}

// runImprovementExperiment plans, checks and runs one experiment of a
// hosted improvement run and waits for it: the org's default worker routes
// it, the daily caps and the own key rule (keyBudgetCheck) refuse it as
// they refuse any start, and its slots run on runners only (remoteOnly).
func (l *launcher) runImprovementExperiment(ctx context.Context, exp *schemas.Experiment) (string, float64, error) {
	if exp.Runner == nil {
		pool, err := handlers.EvaluationPool(ctx, l.org, "")
		if err != nil {
			return "", 0, fmt.Errorf("pick the worker for %s: %w", exp.Name, err)
		}
		if pool != "" {
			exp.Runner = &schemas.RunnerSpec{Pool: pool}
		}
	}
	m, err := l.a.newPlanner().PlanCanary(ctx, exp)
	if err != nil {
		return "", 0, fmt.Errorf("plan %s: %w", exp.Name, err)
	}
	if l.caps != nil {
		if err := l.caps(ctx); err != nil {
			return "", 0, err
		}
	}
	if err := l.keyBudgetCheck(ctx, m); err != nil {
		return "", 0, err
	}
	if err := l.a.store.CreateExperiment(ctx, m); err != nil {
		return "", 0, fmt.Errorf("freeze %s: %w", m.ExpID, err)
	}
	defer refreshResultsIndex(context.WithoutCancel(ctx), l.a, m.ExpID)
	run := l.liveRunFor(m.ExpID, "")
	defer run.finish()
	_, runErr := l.execute(ctx, m, run, nil, nil)
	res := &schedule.Result{ExpID: m.ExpID}
	scheduleRunner{a: l.a}.tally(ctx, res)
	if runErr != nil {
		return m.ExpID, res.CostUSD, fmt.Errorf("run %s: %w", m.ExpID, runErr)
	}
	return m.ExpID, res.CostUSD, nil
}

func (s improveSource) List(context.Context) (*improve.List, error) {
	recs, err := s.r.List()
	if err != nil {
		return nil, err
	}
	l := &improve.List{Improvements: []improve.Summary{}}
	for _, rec := range recs {
		l.Improvements = append(l.Improvements, improve.Summarize(rec, s.r.Running(rec.ID)))
	}
	return l, nil
}

func (s improveSource) Get(_ context.Context, id string) (*improve.Detail, error) {
	rec, err := s.r.Get(id)
	if err != nil {
		return nil, err
	}
	return s.r.View(rec), nil
}

func (s improveSource) Lineage(_ context.Context, id string) (*improve.LineageView, error) {
	lv, err := s.r.Lineage(id)
	return &lv, err
}

func (s improveSource) Create(ctx context.Context, spec *improve.Spec, actor string) (*improve.Summary, error) {
	if s.offline {
		return nil, improve.ErrOffline
	}
	rec, err := s.r.Create(ctx, spec, actor)
	if err != nil {
		return nil, err
	}
	s.start(rec.ID)
	sum := improve.Summarize(rec, true)
	return &sum, nil
}

func (s improveSource) Approve(_ context.Context, id string, round int, actor, reason string) (*improve.Summary, error) {
	if s.offline {
		return nil, improve.ErrOffline
	}
	rec, err := s.r.Approve(id, round, actor, reason)
	if err != nil {
		return nil, err
	}
	s.start(rec.ID)
	sum := improve.Summarize(rec, true)
	return &sum, nil
}

func (s improveSource) Stop(_ context.Context, id, actor string) (*improve.Summary, error) {
	rec, err := s.r.Stop(id, actor)
	if err != nil {
		return nil, err
	}
	sum := improve.Summarize(rec, false)
	return &sum, nil
}

// start drives an improvement in the background.
func (s improveSource) start(id string) {
	go func() {
		if _, err := s.r.Run(context.Background(), id); err != nil && s.r.Logger != nil {
			s.r.Logger.Warn("improvement run from the API ended with an error", "improvement", id, "error", err.Error())
		}
	}()
}
