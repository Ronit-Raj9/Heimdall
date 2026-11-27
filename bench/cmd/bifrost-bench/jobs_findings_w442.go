package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/maximhq/bifrost/bench/steprules"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/investigator"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Job findings.investigate (wave 4, W4-42): a budgeted model reads failed
// runs and writes hypotheses with checked quotes to
// investigations/<job id>.json under the org project root.
func init() {
	registerJobKind(jobKindSpec{Kind: kindFindingsInvestigate, Local: true, Hosted: true,
		Opts:     []jobs.KindOption{jobs.WithLimit(2, 1)},
		Validate: validateFindingsInvestigate, Run: runFindingsInvestigate})
}

const (
	kindFindingsInvestigate = "findings.investigate"
	investigateDefaultRuns  = 20
	investigateMaxRuns      = 40
	investigateMaxQuestion  = 500
	investigationsDir       = "investigations"
)

type investigateParams struct {
	ExpID    string   `json:"exp_id"`
	Runs     []string `json:"runs,omitempty"`     // default: the experiment's failed runs (outcome not pass), newest first
	MaxRuns  int      `json:"max_runs,omitempty"` // default 20, at most 40
	Question string   `json:"question,omitempty"` // optional, at most 500 chars; default "Why did these runs fail?"
	modelParams
}

// investigateResult is the job's result: where the report is and what it
// holds.
type investigateResult struct {
	Report   string  `json:"report"` // the report id (the job id)
	Runs     int     `json:"runs"`
	Skipped  int     `json:"skipped"`
	Items    int     `json:"items"`
	Dropped  int     `json:"dropped_unverified"`
	SpentUSD float64 `json:"spent_usd"`
	Stopped  string  `json:"stopped,omitempty"`
}

func invalidInvestigate(msg string) error {
	return fmt.Errorf("%w: %s", jobs.ErrInvalidParams, msg)
}

func parseInvestigateParams(raw json.RawMessage) (investigateParams, error) {
	var p investigateParams
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return p, invalidInvestigate("the job settings could not be read: " + err.Error())
	}
	p.ExpID = strings.TrimSpace(p.ExpID)
	p.Question = strings.TrimSpace(p.Question)
	switch {
	case p.ExpID == "":
		return p, invalidInvestigate("Pick an evaluation first.")
	case p.Provider == "" || p.Model == "":
		return p, invalidInvestigate("Pick a model first.")
	case !p.Yes:
		return p, invalidInvestigate("Confirm the cost first.")
	case !(p.BudgetUSD > 0):
		return p, invalidInvestigate("Set a spending cap above $0 first.")
	case p.MaxRuns < 0 || p.MaxRuns > investigateMaxRuns || len(p.Runs) > investigateMaxRuns:
		return p, invalidInvestigate("Pick at most 40 runs.")
	case utf8.RuneCountInString(p.Question) > investigateMaxQuestion:
		return p, invalidInvestigate("Keep the question to 500 characters or fewer.")
	}
	if p.MaxRuns == 0 {
		p.MaxRuns = investigateDefaultRuns
	}
	if p.Question == "" {
		p.Question = investigator.DefaultQuestion
	}
	return p, nil
}

func validateFindingsInvestigate(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
	p, err := parseInvestigateParams(raw)
	if err != nil {
		return nil, "", err
	}
	if idx := env.Services.Index; idx != nil {
		if _, ok := idx.Manifest(p.ExpID); !ok {
			return nil, "", invalidInvestigate(fmt.Sprintf("No evaluation named %s was found.", p.ExpID))
		}
	}
	return p, p.ExpID, nil
}

func runFindingsInvestigate(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
	p, err := parseInvestigateParams(j.Params)
	if err != nil {
		return nil, err
	}
	if env.Services.Index == nil {
		return nil, errors.New("this server cannot read runs")
	}
	root := env.ProjectRoot
	if root == "" {
		return nil, errors.New("no project folder on this server")
	}
	inputs := investigateInputs(env, p)
	if len(inputs) == 0 {
		return nil, errors.New("This evaluation has no failed runs to read.")
	}
	if openCaller == nil {
		return nil, errors.New("this server makes no model calls")
	}
	c, done, err := openCaller(ctx, env, j, p.modelParams)
	if err != nil {
		return nil, err
	}
	defer done()
	log.Printf("reading %d failed runs with a cap of $%.2f", len(inputs), c.Budget())
	rep, err := investigator.Investigate(ctx, c, p.Question, inputs)
	if rep == nil {
		return nil, err
	}
	rep.ID, rep.ExpID = j.ID, p.ExpID
	if rep.Model == "" {
		rep.Model = p.Provider + "/" + p.Model
	}
	if werr := writeInvestigation(root, rep); werr != nil {
		return nil, werr
	}
	res := &investigateResult{Report: rep.ID, Runs: len(rep.Runs), Skipped: len(rep.Skipped), Items: len(rep.Items), Dropped: rep.Dropped, SpentUSD: rep.SpentUSD, Stopped: rep.Stopped}
	if err != nil {
		return res, err
	}
	return res, nil
}

// investigateInputs picks the runs to read: the named ones, else the
// experiment's sealed failed runs (outcome set and not pass), newest first,
// at most MaxRuns. Steps come from each run's steps.jsonl; bad lines are
// skipped.
func investigateInputs(env jobEnv, p investigateParams) []investigator.RunInput {
	idx := env.Services.Index
	var views []handlers.RunView
	if len(p.Runs) > 0 {
		for _, id := range p.Runs {
			if v, ok := idx.Run(id); ok && v.Run.ExpID == p.ExpID {
				views = append(views, *v)
			}
		}
	} else {
		for _, v := range idx.Runs(p.ExpID) {
			if v.Superseded || v.Run.Outcome == "" || v.Run.Outcome == schemas.OutcomePass {
				continue
			}
			views = append(views, v)
		}
		sort.SliceStable(views, func(a, b int) bool { return views[a].Run.Timing.Started.After(views[b].Run.Timing.Started) })
	}
	if len(views) > p.MaxRuns {
		views = views[:p.MaxRuns]
	}
	out := make([]investigator.RunInput, 0, len(views))
	for _, v := range views {
		in := investigator.RunInput{RunID: v.Run.RunID, Task: v.Slot.Suite + "/" + v.Slot.Task, Arm: v.Slot.Arm, Outcome: string(v.Run.Outcome)}
		for _, f := range v.Run.Failures {
			in.Failed = append(in.Failed, strings.TrimSpace(f.Part+": "+f.Error))
		}
		runID := v.Run.RunID
		if raw, err := idx.RunFile(runID, schemas.StepsFile); err == nil {
			in.Steps = parseStepLines(raw)
		} else if steps, _, err := steprules.Load(func(name string) ([]byte, error) { return idx.RunFile(runID, name) }, 0); err == nil {
			// No steps.jsonl: steprules.Load reads the steps from the run's
			// evidence.json, as the Behaviour checks do.
			in.Steps = steps
		}
		// The agent's final message is the claim the run recorded (run.json);
		// Investigate redacts it before any prompt.
		if v.Run.Claim != nil {
			in.Final = v.Run.Claim.Text
		}
		out = append(out, in)
	}
	return out
}

func parseStepLines(raw []byte) []schemas.StepRecord {
	var out []schemas.StepRecord
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for sc.Scan() {
		var s schemas.StepRecord
		if json.Unmarshal(sc.Bytes(), &s) == nil {
			out = append(out, s)
		}
	}
	return out
}

// writeInvestigation writes the report atomically.
func writeInvestigation(root string, rep *investigator.Report) error {
	if rep.ID == "" || filepath.Base(rep.ID) != rep.ID || strings.HasPrefix(rep.ID, ".") {
		return fmt.Errorf("job id %q can not name a file", rep.ID)
	}
	dir := filepath.Join(root, investigationsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".inv-*.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, rep.ID+".json"))
}
