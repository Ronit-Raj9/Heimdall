package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/investigator"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/schemas"
)

// w442Index is a read index with one experiment of three runs.
type w442Index struct {
	runs  []handlers.RunView
	files map[string][]byte
}

func (x *w442Index) Experiments() []string { return []string{"e1"} }
func (x *w442Index) Manifest(expID string) (*schemas.Manifest, bool) {
	return &schemas.Manifest{}, expID == "e1"
}
func (x *w442Index) Runs(expID string) []handlers.RunView {
	if expID != "e1" {
		return nil
	}
	return x.runs
}
func (x *w442Index) Run(runID string) (*handlers.RunView, bool) {
	for i := range x.runs {
		if x.runs[i].Run.RunID == runID {
			return &x.runs[i], true
		}
	}
	return nil, false
}
func (x *w442Index) Pair(string) []handlers.RunView { return nil }
func (x *w442Index) RunFile(runID, name string) ([]byte, error) {
	if b, ok := x.files[runID+"/"+name]; ok {
		return b, nil
	}
	return nil, os.ErrNotExist
}

func w442Fixture(t *testing.T) jobEnv {
	t.Helper()
	at := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	mk := func(id, task string, outcome schemas.Outcome, h int) handlers.RunView {
		return handlers.RunView{Slot: schemas.Slot{Suite: "s", Task: task, Arm: "a"}, Sealed: true,
			Run: schemas.Run{RunID: id, ExpID: "e1", Outcome: outcome, Timing: schemas.RunTiming{Started: at.Add(time.Duration(h) * time.Hour)}}}
	}
	step := func(seq int, text string) string {
		b, _ := json.Marshal(schemas.StepRecord{Seq: seq, Kind: "tool_request", Summary: text})
		return string(b)
	}
	idx := &w442Index{
		runs: []handlers.RunView{mk("r1", "t1", schemas.OutcomeFail, 1), mk("r2", "t2", schemas.OutcomePass, 2), mk("r3", "t3", schemas.OutcomeTimeout, 3)},
		files: map[string][]byte{
			"r1/steps.jsonl": []byte(step(1, "pytest exited with status 1 on test_login") + "\nnot json\n"),
			"r3/steps.jsonl": []byte(step(1, "the agent waited for the server forever and token=abcdef123456")),
		},
	}
	return jobEnv{OrgID: "org1", ProjectRoot: t.TempDir(), Services: handlers.Services{OrgID: "org1", Index: idx}}
}

func TestInvestigateJobValidate(t *testing.T) {
	env := w442Fixture(t)
	cases := []struct{ params, want string }{
		{`{"exp_id":"e1","provider":"openai","model":"m","budget_usd":1}`, "Confirm the cost first."},
		{`{"exp_id":"e1","provider":"openai","model":"m","yes":true}`, "Set a spending cap above $0 first."},
		{`{"exp_id":"e1","provider":"openai","model":"m","budget_usd":1,"yes":true,"max_runs":41}`, "Pick at most 40 runs."},
		{`{"exp_id":"e1","provider":"openai","model":"m","budget_usd":1,"yes":true,"runs":["` + strings.Repeat(`r","`, 40) + `r"]}`, "Pick at most 40 runs."},
		{`{"exp_id":"e1","budget_usd":1,"yes":true}`, "Pick a model first."},
		{`{"provider":"openai","model":"m","budget_usd":1,"yes":true}`, "Pick an evaluation first."},
		{`{"exp_id":"nope","provider":"openai","model":"m","budget_usd":1,"yes":true}`, "No evaluation named nope was found."},
	}
	for _, c := range cases {
		_, _, err := validateFindingsInvestigate(context.Background(), env, json.RawMessage(c.params))
		if err == nil || !errors.Is(err, jobs.ErrInvalidParams) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", c.params, err, c.want)
		}
	}
	p, target, err := validateFindingsInvestigate(context.Background(), env, json.RawMessage(`{"exp_id":"e1","provider":"openai","model":"m","budget_usd":1,"yes":true}`))
	if err != nil || target != "e1" {
		t.Fatalf("valid params refused: %v", err)
	}
	if ip := p.(investigateParams); ip.MaxRuns != 20 || ip.Question != investigator.DefaultQuestion {
		t.Fatalf("defaults not set: %+v", ip)
	}
}

func TestInvestigateJobWritesReport(t *testing.T) {
	env := w442Fixture(t)
	fake := &investigator.FakeModel{Replies: []string{
		`{"items":[]}`, // r3, the newest failed run, is read first
		`{"items":[{"title":"Login test","hypothesis":"The login test failed.","kind":"agent_mistake","quotes":[{"seq":1,"text":"pytest exited with status 1"}]}]}`,
		`{"groups":[]}`,
	}, CostUSD: 0.01, Cap: 1}
	old := openCaller
	revoked := 0
	openCaller = func(ctx context.Context, env jobEnv, j *jobs.Job, p modelParams) (investigator.Caller, func(), error) {
		return fake, func() { revoked++ }, nil
	}
	t.Cleanup(func() { openCaller = old })
	j := &jobs.Job{ID: "job_1", Params: json.RawMessage(`{"exp_id":"e1","provider":"openai","model":"m","budget_usd":1,"yes":true}`)}
	res, err := runFindingsInvestigate(context.Background(), env, j, &testJobLog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if revoked != 1 {
		t.Fatalf("done called %d times, want 1", revoked)
	}
	raw, err := os.ReadFile(filepath.Join(env.ProjectRoot, "investigations", "job_1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rep investigator.Report
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatal(err)
	}
	// The failed runs, newest first; the passing run is not read.
	if rep.ID != "job_1" || rep.ExpID != "e1" || strings.Join(rep.Runs, ",") != "r3,r1" || len(rep.Items) != 1 {
		t.Fatalf("report %+v", rep)
	}
	if res == nil {
		t.Fatal("no job result")
	}
	for _, s := range fake.Seen {
		for _, m := range s.Messages {
			if strings.Contains(m.Content, "abcdef123456") {
				t.Fatalf("a secret reached the prompt: %s", m.Content)
			}
		}
	}
}

func TestInvestigateJobWithoutModelCalls(t *testing.T) {
	env := w442Fixture(t)
	old := openCaller
	openCaller = nil
	t.Cleanup(func() { openCaller = old })
	j := &jobs.Job{ID: "job_2", Params: json.RawMessage(`{"exp_id":"e1","provider":"openai","model":"m","budget_usd":1,"yes":true}`)}
	if _, err := runFindingsInvestigate(context.Background(), env, j, &testJobLog{}, nil); err == nil || err.Error() != "this server makes no model calls" {
		t.Fatalf("got %v", err)
	}
}
