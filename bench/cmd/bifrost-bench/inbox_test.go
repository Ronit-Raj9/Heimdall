package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/resultsindex"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schedule"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/traceinbox"
)

// inboxProject is a project with a tool-use suite, a webhook source and a
// scheduled pull, and a chat trace file with a planted key.
func inboxProject(t *testing.T) (config, root, trace string) {
	t.Helper()
	root = t.TempDir()
	write := func(name, body string) {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("deploy/bench.yaml", "runs_root: runs\nbenchmarks_root: benchmarks\nserver:\n  listen: 127.0.0.1:1\n  mode: live\n")
	write("inbox/sources.yaml", "schedule: \"*/30 * * * *\"\nsources:\n  - name: hook\n    kind: webhook\n    token_env: INBOX_TEST_TOKEN\n    suite: tools\n")
	files, err := scaffold.Suite(scaffold.SuiteOptions{ID: "tools", Template: scaffold.TemplateToolUse})
	if err != nil {
		t.Fatal(err)
	}
	if err := scaffold.WriteFiles(filepath.Join(root, "benchmarks", "tools"), files, false); err != nil {
		t.Fatal(err)
	}
	conv := `{"id":"conv-1","status":"failed","messages":[
 {"role":"user","content":"Refund order A-1001, key sk-live1234567890abcdefXYZ"},
 {"role":"assistant","content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"issue_refund","arguments":"{\"order\":\"A-1001\"}"}}]},
 {"role":"tool","tool_call_id":"c1","content":"Error: timeout after 30s"},
 {"role":"assistant","content":"Done."}]}`
	write("trace.json", conv)
	return filepath.Join(root, "deploy", "bench.yaml"), root, filepath.Join(root, "trace.json")
}

func TestInboxCLI(t *testing.T) {
	config, root, trace := inboxProject(t)
	ctx := context.Background()
	var out bytes.Buffer
	if code := inboxIngest(ctx, []string{"-config", config, "-file", trace, "-reason", "thumbs down"}, &out); code != exitOK {
		t.Fatalf("ingest = %d %s", code, out.String())
	}
	if !strings.Contains(out.String(), "new item inc-") {
		t.Fatalf("ingest output = %s", out.String())
	}
	out.Reset()
	if code := inboxList(ctx, []string{"-config", config, "-format", "json"}, &out); code != exitOK {
		t.Fatalf("list = %d", code)
	}
	var items []schemas.InboxItem
	if err := json.Unmarshal(out.Bytes(), &items); err != nil || len(items) != 1 || items[0].SuggestedSuite != "tools" {
		t.Fatalf("list = %s (%v)", out.String(), err)
	}
	id := items[0].ID
	out.Reset()
	if code := inboxShow(ctx, []string{"-config", config, id}, &out); code != exitOK || !strings.Contains(out.String(), "signature f_") ||
		strings.Contains(out.String(), "sk-live") {
		t.Fatalf("show = %d %s", code, out.String())
	}
	out.Reset()
	if code := inboxAccept(ctx, []string{"-config", config, id, "-suite", "tools", "-id", "refund-timeout"}, &out); code != exitOK ||
		!strings.Contains(out.String(), "accepted "+id+" as task tools/refund-timeout") || !strings.Contains(out.String(), "task admit tools/refund-timeout") {
		t.Fatalf("accept = %d %s", code, out.String())
	}
	out.Reset()
	if code := inboxTasks(ctx, []string{"-config", config, "-ids"}, &out); code != exitOK || out.String() != "- refund-timeout\n" {
		t.Fatalf("tasks -ids = %d %q", code, out.String())
	}
	out.Reset()
	if code := inboxReject([]string{"-config", config, id, "-reason", "x"}, &out); code == exitOK {
		t.Error("an accepted item was rejected")
	}
	if code := inboxReject([]string{"-config", config, id}, &out); code != exitUsage {
		t.Errorf("reject without a reason = %d", code)
	}
	if _, err := os.Stat(filepath.Join(root, "inbox", "items.jsonl")); err != nil {
		t.Error(err)
	}
}

func TestInboxScheduleJob(t *testing.T) {
	config, _, _ := inboxProject(t)
	a, err := newApp(context.Background(), config, appOptions{})
	if err != nil {
		t.Fatal(err)
	}
	file := &schedule.File{}
	addInboxSchedule(a, file)
	s, ok := file.Find(schedule.JobInboxPull)
	if !ok || s.Job != schedule.JobInboxPull || s.Cron != "*/30 * * * *" {
		t.Fatalf("schedules = %+v", file.Schedules)
	}
	if err := file.Validate(a.cfg.Root, loadExperiment); err != nil {
		t.Fatalf("validate: %v", err)
	}
	addInboxSchedule(a, file)
	if len(file.Schedules) != 1 {
		t.Errorf("the job was added twice: %+v", file.Schedules)
	}
	// The webhook is the only source, so the pull has nothing to read.
	if _, err := (scheduleRunner{a: a}).Run(context.Background(), s); err == nil || !strings.Contains(err.Error(), "no source to pull") {
		t.Errorf("run = %v", err)
	}
	bad := schedule.Schedule{Name: "x", Job: "inbox-pull", Cron: "* * * * *", Experiment: "e.yaml"}
	if err := bad.Validate(a.cfg.Root, nil); err == nil {
		t.Error("a job with an experiment passed")
	}
	if err := (schedule.Schedule{Name: "x", Job: "nope", Cron: "* * * * *"}).Validate(a.cfg.Root, nil); err == nil {
		t.Error("an unknown job passed")
	}
}

func TestLatestRelease(t *testing.T) {
	one, zero := 1.0, 0.0
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	rows := []resultsindex.RunRow{
		{Experiment: "e_old", ExperimentCreated: t0, Suite: "tools@1.0.0", Task: "refund", Success: &one},
		{Experiment: "e_new", ExperimentCreated: t0.Add(time.Hour), Suite: "tools@1.0.0", Task: "refund", Success: &one},
		{Experiment: "e_new", ExperimentCreated: t0.Add(time.Hour), Suite: "tools@1.0.0", Task: "refund", Success: &zero},
		{Experiment: "e_other", ExperimentCreated: t0.Add(2 * time.Hour), Suite: "else@1.0.0", Task: "refund", Success: &one},
	}
	rs := latestRelease(rows, "tools")
	if rs.Status != traceinbox.ReleaseFails || rs.Experiment != "e_new" || rs.Passed != 1 || rs.Runs != 2 {
		t.Errorf("release = %+v", rs)
	}
	if latestRelease(nil, "tools").Status != traceinbox.ReleaseNotRun {
		t.Error("no rows is not not_run")
	}
}

func TestIncidentReportSection(t *testing.T) {
	config, _, trace := inboxProject(t)
	ctx := context.Background()
	var out bytes.Buffer
	inboxIngest(ctx, []string{"-config", config, "-file", trace}, &out)
	ib, a, err := openInbox(ctx, config, false)
	if err != nil {
		t.Fatal(err)
	}
	items, _ := ib.List(ctx, "", "")
	if _, err := ib.Accept(ctx, items[0].ID, traceinbox.AcceptOptions{Suite: "tools", TaskID: "refund-timeout"}); err != nil {
		t.Fatal(err)
	}
	runs := []schemas.SealedRun{{Slot: schemas.Slot{Suite: "tools@1.0.0", Task: "refund-timeout", Arm: "B"}, Grade: &schemas.Grade{Passed: false}}}
	report := a.incidentSection(runs, "# Verdict\n\n## Primary\n\ntext\n")
	if !strings.Contains(report, "## Production incidents") || !strings.Contains(report, "Regression of incident "+items[0].ID) {
		t.Errorf("report = %s", report)
	}
}
