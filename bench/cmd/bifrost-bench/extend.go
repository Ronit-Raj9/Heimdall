package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/verdict"
)

// W7 buy more evidence (docs/adaptive-trials.md): `extend` writes the
// follow-up experiment of an inconclusive one; `extend status` analyses the
// follow-up together with its parent under the parent's pre-declared
// combination rule, or labels the pooled analysis exploratory.

func init() {
	register("extend", "plan a follow-up of an inconclusive experiment, or analyse one with its parent: extend -exp <id> -tasks N | -trials K; extend status -exp <follow-up id>", runExtend)
}

const extendUsage = `usage: heimdall extend [-config deploy/bench.yaml] -exp <id> (-tasks N | -trials K) [-out experiments/<name>.yaml] [-force]
       heimdall extend status [-config deploy/bench.yaml] -exp <follow-up id> [-json]`

func runExtend(args []string) int {
	if len(args) > 0 && args[0] == "status" {
		return runExtendStatus(args[1:])
	}
	fs := flag.NewFlagSet("extend", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	expID := fs.String("exp", "", "experiment to extend")
	tasks := fs.Int("tasks", 0, "run N tasks the experiment did not run")
	trials := fs.Int("trials", 0, "run K more trials of the experiment's tasks")
	out := fs.String("out", "", "where to write the follow-up experiment (default experiments/<name>-ext.yaml)")
	force := fs.Bool("force", false, "extend a verdict that is not INCONCLUSIVE")
	if _, err := parseArgs(fs, args); err != nil || *expID == "" || (*tasks > 0) == (*trials > 0) {
		fmt.Fprintln(os.Stderr, extendUsage)
		return exitUsage
	}
	ctx := context.Background()
	a, err := newApp(ctx, *config, appOptions{})
	if err != nil {
		return fail("%v", err)
	}
	m, err := a.store.LoadManifest(ctx, *expID)
	if err != nil {
		return fail("load %s: %v", *expID, err)
	}
	an, _, err := a.analyze(ctx, *expID, "")
	if err != nil {
		return fail("analyze %s: %v", *expID, err)
	}
	if an.Verdict.Decision != schemas.DecisionInconclusive && !*force {
		return fail("%s is %s, not INCONCLUSIVE: there is nothing to buy more evidence for (pass -force to extend anyway)", *expID, an.Verdict.Decision)
	}
	s, err := engine.LoadSuite(ctx, a.reg, m.Experiment.Suite)
	if err != nil {
		return fail("%v", err)
	}
	kind, n := engine.ExtendTasks, *tasks
	if *trials > 0 {
		kind, n = engine.ExtendTrials, *trials
	}
	e, notes, err := engine.ExtendExperiment(m, s, kind, n)
	if err != nil {
		return fail("%v", err)
	}
	raw, err := experimentYAML(e)
	if err != nil {
		return fail("%v", err)
	}
	path := *out
	if path == "" {
		path = filepath.Join(a.cfg.Path("experiments"), e.Name+".yaml")
	}
	if _, err := os.Stat(path); err == nil {
		return fail("%s exists; pass -out <new file>", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fail("%v", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return fail("write %s: %v", path, err)
	}
	fmt.Printf("wrote %s: follow-up of %s (%s, %d)\n", path, *expID, kind, n)
	if me := an.Verdict.MoreEvidence; me != nil {
		cost := "unknown"
		if me.CostUSD != nil {
			cost = fmt.Sprintf("about %.2f USD", *me.CostUSD)
		}
		fmt.Printf("sizing: to detect %.1f pp the verdict needs about %d more tasks, %s (%s)\n", me.TargetPP, me.Tasks, cost, me.CostBasis)
	}
	fmt.Printf("combination rule: %s\n", e.Extends.Rule)
	for _, note := range notes {
		fmt.Printf("note: %s\n", note)
	}
	fmt.Printf("next: heimdall run %s, then heimdall extend status -exp <its id>\n", path)
	return exitOK
}

// experimentYAML writes the experiment through its JSON form with every
// empty field left out, so the strict decoder and the schema read it back.
func experimentYAML(e *schemas.Experiment) ([]byte, error) {
	b, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	pruneEmpty(doc)
	return yaml.Marshal(doc)
}

// pruneEmpty drops nil, empty and zero values from a JSON object, in place,
// and turns whole numbers back into integers so a seed prints as 20261119,
// not 2.0261119e+07.
func pruneEmpty(v map[string]any) {
	for k, x := range v {
		switch t := x.(type) {
		case nil:
			delete(v, k)
		case string:
			if t == "" {
				delete(v, k)
			}
		case float64:
			switch {
			case t == 0:
				delete(v, k)
			case t == math.Trunc(t) && math.Abs(t) < 1<<53:
				v[k] = int64(t)
			}
		case bool:
			if !t {
				delete(v, k)
			}
		case []any:
			if len(t) == 0 {
				delete(v, k)
			}
			for _, item := range t {
				if m, ok := item.(map[string]any); ok {
					pruneEmpty(m)
				}
			}
		case map[string]any:
			pruneEmpty(t)
			if len(t) == 0 {
				delete(v, k)
			}
		}
	}
}

func runExtendStatus(args []string) int {
	fs := flag.NewFlagSet("extend status", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	expID := fs.String("exp", "", "the follow-up experiment")
	asJSON := fs.Bool("json", false, "print the combined analysis as JSON")
	if _, err := parseArgs(fs, args); err != nil || *expID == "" {
		fmt.Fprintln(os.Stderr, extendUsage)
		return exitUsage
	}
	ctx := context.Background()
	a, err := newApp(ctx, *config, appOptions{})
	if err != nil {
		return fail("%v", err)
	}
	child, err := a.store.LoadManifest(ctx, *expID)
	if err != nil {
		return fail("load %s: %v", *expID, err)
	}
	ref := child.Experiment.Extends
	if ref == nil {
		return fail("%s extends no experiment", *expID)
	}
	parent, err := a.store.LoadManifest(ctx, ref.ExpID)
	if err != nil {
		return fail("load %s: %v", ref.ExpID, err)
	}
	if ref.Kind == engine.ExtendTrials {
		if _, err := suite.CompareTiers(parent, child, false); err != nil {
			return fail("%v", err)
		}
	}
	pa, _, err := a.analyze(ctx, parent.ExpID, "")
	if err != nil {
		return fail("analyze %s: %v", parent.ExpID, err)
	}
	ca, _, err := a.analyze(ctx, child.ExpID, "")
	if err != nil {
		return fail("analyze %s: %v", child.ExpID, err)
	}
	pr, err := a.store.SealedRuns(ctx, parent.ExpID, schemas.GradeSourceOriginal)
	if err != nil {
		return fail("%v", err)
	}
	cr, err := a.store.SealedRuns(ctx, child.ExpID, schemas.GradeSourceOriginal)
	if err != nil {
		return fail("%v", err)
	}
	res, err := verdict.CombineExtension(parent, pr, pa.Verdict.Decision, child, cr, ca.Verdict.Decision)
	if err != nil {
		return fail("%v", err)
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			return fail("%v", err)
		}
	} else {
		printExtension(os.Stdout, res)
	}
	switch res.Decision {
	case schemas.DecisionRegressed:
		return 1
	case schemas.DecisionInconclusive:
		return 2
	case schemas.DecisionInvalid:
		return 3
	}
	return exitOK
}

func printExtension(w io.Writer, r *schemas.ExtensionResult) {
	fmt.Fprintf(w, "%s extends %s (%s): rule %s\n", r.Child, r.Parent, r.Kind, r.Rule)
	for i, st := range r.Stages {
		fmt.Fprintf(w, "  stage %d %s: %s, %d tasks, d_bar %+.1f pp, one-sided p improve %.4f..%.4f, regress %.4f..%.4f\n",
			i+1, st.ExpID, st.Decision, st.Tasks, 100*st.DBar, st.PImproveLow, st.PImproveHi, st.PRegressLow, st.PRegressHi)
	}
	fmt.Fprintf(w, "  pooled d_bar %+.1f pp over %d tasks (descriptive)\n", 100*r.PooledDBar, r.PooledTasks)
	if !r.Exploratory {
		fmt.Fprintf(w, "  combined z improve %.3f..%.3f, regress %.3f..%.3f, critical %.3f (alpha %.4f, weight of stage 1 %.2f)\n",
			r.ZImproveLow, r.ZImproveHi, r.ZRegressLow, r.ZRegressHi, r.ZCritical, r.Alpha, r.WeightFirst)
	}
	fmt.Fprintf(w, "decision: %s", r.Decision)
	if r.Exploratory {
		fmt.Fprint(w, " (exploratory: no decision)")
	}
	fmt.Fprintln(w)
	for _, reason := range r.Reasons {
		fmt.Fprintf(w, "  %s\n", reason)
	}
}
