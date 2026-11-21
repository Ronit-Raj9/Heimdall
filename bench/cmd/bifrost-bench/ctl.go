package main

// `heimdall ctl`: run control from the shell (audit/RESEARCH_R1.md item 9).
// Thin client over the REST routes of handlers/runcontrol.go with -server;
// without one, the experiment controls are written straight into the local
// store, where a running `run` or `serve` picks them up within a second.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/runctl"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
)

const ctlUsage = `usage: bifrost-bench ctl exp controls <exp_id> [-json]
       bifrost-bench ctl exp pause <exp_id> [-reason TEXT]
       bifrost-bench ctl exp resume <exp_id> [-reason TEXT]
       bifrost-bench ctl exp set <exp_id> [-concurrency N] [-total-usd USD] [-pause-models m1,m2|-] [-reason TEXT]
       bifrost-bench ctl exp cancel <exp_id>
       bifrost-bench ctl exp rerun-failures <exp_id> [-outcomes fail,timeout] [-reason TEXT]
       bifrost-bench ctl run cancel <exp_id> <run_id> [-reason TEXT]
       bifrost-bench ctl run requeue <exp_id> <run_id> [-reason TEXT]
       bifrost-bench ctl eval pause|resume|rerun-failures <eval_id> [-reason TEXT]
Pause drains: runs in flight finish, no new run starts until resume. A
cancelled run is never counted and uses no attempt; requeue runs it again
(only aborted_infra or infra_error runs; the old run is kept and marked
superseded). Every change is recorded with -reason in the experiment's
controls.jsonl. Every command takes -server URL -token TOKEN [-org ORG] (env
HEIMDALL_SERVER, HEIMDALL_TOKEN); without a server, exp controls|pause|resume|
set and run cancel|requeue act on the local store (-config deploy/bench.yaml),
and rerun-failures, exp cancel and eval need a server.`

func init() {
	register("ctl", "run control: ctl exp pause|resume|set|cancel|rerun-failures, ctl run cancel|requeue, ctl eval pause|resume|rerun-failures", runCtl)
}

func runCtl(args []string) int { return ctlMain(args, os.Stdout, os.Stderr) }

// ctlOpts holds one ctl command's flags.
type ctlOpts struct {
	conn        connOpts
	reason      string
	concurrency int
	totalUSD    float64
	models      string
	outcomes    string
	asJSON      bool
	set         map[string]bool
}

func ctlMain(args []string, out, errw io.Writer) int {
	if len(args) < 2 || args[0] == "-h" || args[0] == "help" {
		fmt.Fprintln(errw, ctlUsage)
		return exitUsage
	}
	area, verb := args[0], args[1]
	var pos []string
	rest := args[2:]
	for len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		pos, rest = append(pos, rest[0]), rest[1:]
	}
	o := &ctlOpts{}
	fs := flag.NewFlagSet("ctl "+area+" "+verb, flag.ContinueOnError)
	fs.SetOutput(errw)
	addConnFlags(fs, &o.conn)
	fs.StringVar(&o.reason, "reason", "", "why: recorded in the controls log")
	fs.IntVar(&o.concurrency, "concurrency", -1, "set: live concurrency (0 = the experiment's own)")
	fs.Float64Var(&o.totalUSD, "total-usd", -1, "set: live spend cap in USD (0 = the frozen cap)")
	fs.StringVar(&o.models, "pause-models", "", "set: models to hold, comma separated; - clears")
	fs.StringVar(&o.outcomes, "outcomes", "", "rerun-failures: outcomes to rerun, comma separated (default every outcome but pass)")
	fs.BoolVar(&o.asJSON, "json", false, "print JSON")
	if err := fs.Parse(rest); err != nil {
		fmt.Fprintln(errw, ctlUsage)
		return exitUsage
	}
	pos = append(pos, fs.Args()...)
	o.conn.finish(fs)
	o.set = map[string]bool{}
	fs.Visit(func(f *flag.Flag) { o.set[f.Name] = true })
	want := map[string]int{"exp": 1, "run": 2, "eval": 1}[area]
	if want == 0 || len(pos) != want {
		fmt.Fprintln(errw, ctlUsage)
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var res any
	var err error
	if strings.TrimSpace(o.conn.server) != "" {
		res, err = ctlRemote(ctx, o, area, verb, pos)
	} else {
		res, err = ctlLocal(ctx, o, area, verb, pos)
	}
	if errors.Is(err, errCtlUsage) {
		fmt.Fprintf(errw, "%v\n\n%s\n", err, ctlUsage)
		return exitUsage
	}
	if err != nil {
		fmt.Fprintln(errw, "ctl:", err)
		return 1
	}
	if o.asJSON {
		return writeJSON(out, res)
	}
	printCtl(out, res)
	return exitOK
}

var errCtlUsage = errors.New("usage")

// patch builds the set command's patch from its flags.
func (o *ctlOpts) patch() (runctl.Patch, error) {
	p := runctl.Patch{Reason: o.reason}
	if o.set["concurrency"] {
		c := o.concurrency
		p.Concurrency = &c
	}
	if o.set["total-usd"] {
		u := o.totalUSD
		p.TotalUSD = &u
	}
	if o.set["pause-models"] {
		ms := []string{}
		if strings.TrimSpace(o.models) != "-" {
			for _, m := range strings.Split(o.models, ",") {
				if m = strings.TrimSpace(m); m != "" {
					ms = append(ms, m)
				}
			}
		}
		p.PausedModels = &ms
	}
	if p.Empty() {
		return p, fmt.Errorf("%w: set needs -concurrency, -total-usd or -pause-models", errCtlUsage)
	}
	return p, nil
}

func (o *ctlOpts) outcomeList() []schemas.Outcome {
	var out []schemas.Outcome
	for _, s := range strings.Split(o.outcomes, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, schemas.Outcome(s))
		}
	}
	return out
}

// ctlRemote calls the server.
func ctlRemote(ctx context.Context, o *ctlOpts, area, verb string, pos []string) (any, error) {
	cl, err := newSyncClient(&syncOpts{server: o.conn.server, token: o.conn.token, org: o.conn.org})
	if err != nil {
		return nil, err
	}
	h := &httpEval{c: cl}
	reason := handlers.RunControlRequest{Reason: o.reason}
	exp := func(id string) string { return "/experiments/" + urlPathEscape(id) }
	switch area + " " + verb {
	case "exp controls":
		var rc handlers.RunControls
		return &rc, h.call(ctx, http.MethodGet, exp(pos[0])+"/controls", nil, &rc)
	case "exp pause":
		var rc handlers.RunControls
		return &rc, h.call(ctx, http.MethodPost, exp(pos[0])+"/pause", reason, &rc)
	case "exp resume":
		var st schemas.ExperimentStatus
		if err := h.call(ctx, http.MethodPost, exp(pos[0])+"/resume", reason, &st); err != nil {
			return nil, err
		}
		var rc handlers.RunControls
		return &rc, h.call(ctx, http.MethodGet, exp(pos[0])+"/controls", nil, &rc)
	case "exp set":
		p, err := o.patch()
		if err != nil {
			return nil, err
		}
		var rc handlers.RunControls
		return &rc, h.call(ctx, http.MethodPatch, exp(pos[0])+"/controls", p, &rc)
	case "exp cancel":
		var st schemas.ExperimentStatus
		return &st, h.call(ctx, http.MethodPost, exp(pos[0])+"/cancel", struct{}{}, &st)
	case "exp rerun-failures":
		var res handlers.RerunFailuresResult
		return &res, h.call(ctx, http.MethodPost, exp(pos[0])+"/rerun-failures", handlers.RerunFailuresRequest{Outcomes: o.outcomeList(), Reason: o.reason}, &res)
	case "run cancel", "run requeue":
		var rc handlers.RunControls
		return &rc, h.call(ctx, http.MethodPost, exp(pos[0])+"/runs/"+urlPathEscape(pos[1])+"/"+verb, reason, &rc)
	case "eval pause", "eval resume":
		var ev schemas.Evaluation
		return &ev, h.call(ctx, http.MethodPost, evalPath(pos[0])+"/"+verb, reason, &ev)
	case "eval rerun-failures":
		var res handlers.EvaluationRerunResult
		return &res, h.call(ctx, http.MethodPost, evalPath(pos[0])+"/rerun-failures", handlers.RerunFailuresRequest{Outcomes: o.outcomeList(), Reason: o.reason}, &res)
	}
	return nil, fmt.Errorf("%w: unknown command ctl %s %s", errCtlUsage, area, verb)
}

// localActor names the shell user in the controls log.
func localActor() string {
	if u := os.Getenv("USER"); u != "" {
		return "cli:" + u
	}
	return "cli"
}

// ctlLocal writes the controls into the local store.
func ctlLocal(ctx context.Context, o *ctlOpts, area, verb string, pos []string) (any, error) {
	cfg, err := loadConfig(projectConfig(o.conn.config, o.conn.configSet))
	if err != nil {
		return nil, err
	}
	root := cfg.Path(cfg.RunsRoot)
	st, err := store.NewFSStore(root, schemas.NoopLogger{})
	if err != nil {
		return nil, err
	}
	expID := pos[0]
	m, err := st.LoadManifest(ctx, expID)
	if err != nil {
		return nil, err
	}
	who := localActor()
	switch area + " " + verb {
	case "exp controls":
	case "exp pause":
		if _, err := runctl.Pause(root, expID, who, o.reason); err != nil {
			return nil, err
		}
	case "exp resume":
		if _, err := runctl.Resume(root, expID, who, o.reason); err != nil {
			return nil, err
		}
	case "exp set":
		p, err := o.patch()
		if err != nil {
			return nil, err
		}
		if err := p.Validate(engine.ExperimentModels(m)); err != nil {
			return nil, err
		}
		if _, err := runctl.Apply(root, expID, who, runctl.ActionSet, "", p.Reason, func(s *runctl.State) error { p.ApplyTo(s); return nil }); err != nil {
			return nil, err
		}
	case "run cancel", "run requeue":
		slotID, err := slotOfRun(root, expID, pos[1])
		if err != nil {
			return nil, err
		}
		recs, err := st.SlotRuns(ctx, expID, slotID)
		if err != nil {
			return nil, err
		}
		if verb == "cancel" {
			for _, rec := range recs {
				if rec.Run.RunID == pos[1] && rec.Sealed {
					return nil, fmt.Errorf("run %s already finished (%s): requeue it instead", pos[1], rec.Run.Outcome)
				}
			}
			_, err = engine.CancelRun(root, expID, who, slotID, pos[1], o.reason)
		} else {
			_, err = engine.Requeue(root, expID, who, slotID, o.reason, recs)
			if err == nil {
				fmt.Fprintf(os.Stderr, "requeued; a running `run` picks it up, else continue with: %s run -resume %s\n", progName, expID)
			}
		}
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("%w: ctl %s %s needs -server (a Heimdall server)", errCtlUsage, area, verb)
	}
	return localControls(root, m)
}

// localControls is GET /controls read from the local store.
func localControls(root string, m *schemas.Manifest) (*handlers.RunControls, error) {
	s, err := runctl.Read(root, m.ExpID)
	if err != nil {
		return nil, err
	}
	log, lerr := runctl.Log(root, m.ExpID)
	if lerr == nil {
		lerr = runctl.Verify(root, m.ExpID)
	}
	rc := &handlers.RunControls{ExpID: m.ExpID, State: s, Paused: s.Paused(), Models: engine.ExperimentModels(m),
		Concurrency: m.Experiment.Concurrency, TotalUSD: m.Experiment.Budget.TotalUSD,
		Policy: schemas.ErrorPolicyTable(m.Experiment.ErrorPolicy), Log: log, LogEntries: len(log), LogVerified: lerr == nil,
		RerunOf: m.Experiment.RerunOf, Exploratory: m.Experiment.Exploratory()}
	if lerr != nil {
		rc.LogProblem = lerr.Error()
	}
	return rc, nil
}

// slotOfRun finds the slot folder holding runID.
func slotOfRun(root, expID, runID string) (string, error) {
	if strings.ContainsAny(runID, `/\.`) || strings.ContainsAny(expID, `/\.`) {
		return "", fmt.Errorf("bad id")
	}
	hits, _ := filepath.Glob(filepath.Join(root, expID, "slots", "*", "runs", runID))
	if len(hits) != 1 {
		return "", fmt.Errorf("run %s of experiment %s: %w", runID, expID, schemas.ErrNotFound)
	}
	return filepath.Base(filepath.Dir(filepath.Dir(hits[0]))), nil
}

// printCtl prints a result for people.
func printCtl(w io.Writer, res any) {
	switch v := res.(type) {
	case *handlers.RunControls:
		st := v.State
		mode := "running"
		if v.Paused {
			mode = fmt.Sprintf("paused by %s (%s); %d runs still in flight", st.PausedBy, reasonText(st.PauseReason), v.InFlight)
		}
		fmt.Fprintf(w, "experiment %s: %s\n", v.ExpID, mode)
		if v.Exploratory {
			fmt.Fprintf(w, "exploratory rerun of %s: decides nothing\n", v.RerunOf)
		}
		conc := fmt.Sprintf("%d (frozen)", v.Concurrency)
		if st.Concurrency > 0 {
			conc = fmt.Sprintf("%d (live; frozen %d)", st.Concurrency, v.Concurrency)
		}
		capText := fmt.Sprintf("%.2f USD (frozen)", v.TotalUSD)
		if st.TotalUSD > 0 {
			capText = fmt.Sprintf("%.2f USD (live; frozen %.2f)", st.TotalUSD, v.TotalUSD)
		}
		fmt.Fprintf(w, "concurrency: %s\nspend cap: %s\n", conc, capText)
		if len(st.PausedModels) > 0 {
			fmt.Fprintf(w, "paused models: %s\n", strings.Join(st.PausedModels, ", "))
		}
		for slot, h := range st.CancelledSlots {
			fmt.Fprintf(w, "cancelled: slot %s run %s by %s (%s)\n", slot, h.RunID, h.By, reasonText(h.Reason))
		}
		for run, s := range st.Superseded {
			fmt.Fprintf(w, "superseded: run %s (%s) by %s (%s)\n", run, s.Outcome, s.By, reasonText(s.Reason))
		}
		tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "\nfailure class\tretry\tmax\tcounts\tgrade")
		for _, r := range v.Policy {
			max := "-"
			if r.MaxAttempts > 0 {
				max = fmt.Sprint(r.MaxAttempts)
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%v\t%s\n", r.Class, r.Retry, max, r.Counts, r.Grade)
		}
		tw.Flush()
		verified := "verified"
		if !v.LogVerified {
			verified = "DOES NOT VERIFY: " + v.LogProblem
		}
		fmt.Fprintf(w, "\ncontrol log: %d entries, %s\n", v.LogEntries, verified)
		for _, e := range v.Log {
			fmt.Fprintf(w, "  %s  %-11s %s %s (%s)\n", e.At.Format(time.RFC3339), e.Action, e.Actor, e.Target, reasonText(e.Reason))
		}
	case *handlers.RerunFailuresResult:
		fmt.Fprintf(w, "exploratory rerun %s of %s: %d tasks, %d runs planned; started %v\n", v.ExperimentID, v.ParentID, v.Tasks, v.Slots, v.Started)
		if v.StartError != "" {
			fmt.Fprintf(w, "start failed: %s\n", v.StartError)
		}
		if len(v.ConfigChanged) > 0 {
			fmt.Fprintf(w, "note: agent config changed since the parent for %s\n", strings.Join(v.ConfigChanged, ", "))
		}
	default:
		b, _ := json.MarshalIndent(res, "", "  ")
		fmt.Fprintln(w, string(b))
	}
}

func reasonText(s string) string {
	if s == "" {
		return "no reason given"
	}
	return s
}
