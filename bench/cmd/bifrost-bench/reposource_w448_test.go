package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/issuebench"
	"github.com/maximhq/bifrost/bench/issuebench/emit"
	"github.com/maximhq/bifrost/bench/issuesync"
	"github.com/maximhq/bifrost/bench/keystore"
)

func privateBenchmarks(t *testing.T, root string) map[string]string {
	t.Helper()
	list, err := issuesync.PrivateSources(root)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, p := range list {
		out[p.Benchmark] = p.Repo
	}
	return out
}

// TestIssuebenchWorkRecordsSource: the hosted issue work job records a
// private repository's benchmark as private and a repository proven public
// as public, before the worker gets the job.
func TestIssuebenchWorkRecordsSource(t *testing.T) {
	bench := emit.SuiteID("acme/widgets")
	exec := &ibwExec{result: json.RawMessage(`{"mode":"build","cost_usd":0.1,"seconds":1,"validation":{}}`)}
	env, _, _, _ := ibwFixture(t, exec, false, true)
	if _, log, _, err := ibwRun(context.Background(), env, ibwParams(t, env, issuebench.WorkModeBuild)); err != nil {
		t.Fatalf("run: %v\n%s", err, log)
	}
	if got := privateBenchmarks(t, env.ProjectRoot); got[bench] != "acme/widgets" {
		t.Fatalf("private build not recorded: %v", got)
	}

	exec = &ibwExec{result: json.RawMessage(`{"mode":"validate","cost_usd":0,"validation":{}}`)}
	env, _, _, _ = ibwFixture(t, exec, true, true)
	if _, log, _, err := ibwRun(context.Background(), env, ibwParams(t, env, issuebench.WorkModeValidate)); err != nil {
		t.Fatalf("run: %v\n%s", err, log)
	}
	if got := privateBenchmarks(t, env.ProjectRoot); len(got) != 0 || !issuesync.KnownPublic(env.ProjectRoot, bench) {
		t.Fatalf("public repository: private %v, known public %v", got, issuesync.KnownPublic(env.ProjectRoot, bench))
	}
}

// TestIssuebenchEmitRecordsSource: the emitter of every issue pipeline (the
// manual From issues flow and the weekly sync, local and hosted) records the
// benchmark private unless a worker proved the repository public, and a
// public record stays public.
func TestIssuebenchEmitRecordsSource(t *testing.T) {
	root := t.TempDir()
	s := &ibStages{root: root}
	_, err := ibEmitter{s}.Emit(context.Background(), "acme/secret", []issuebench.Validation{{Candidate: issuebench.Candidate{Repo: "acme/secret", Issue: 1}}})
	if err == nil {
		t.Fatal("emit without an environment worked")
	}
	if got := privateBenchmarks(t, root); got[emit.SuiteID("acme/secret")] != "acme/secret" {
		t.Fatalf("not recorded before the emit: %v", got)
	}
	if err := issuesync.RecordSource(root, emit.SuiteID("acme/open"), "acme/open", true, time.Now()); err != nil {
		t.Fatal(err)
	}
	_, _ = ibEmitter{s}.Emit(context.Background(), "acme/open", []issuebench.Validation{{Candidate: issuebench.Candidate{Repo: "acme/open", Issue: 1}}})
	if !issuesync.KnownPublic(root, emit.SuiteID("acme/open")) {
		t.Fatal("a public repository turned private")
	}
}

// TestSuiteOpsRecordSource: "Task from a commit" and "Find tasks" record a
// GitHub repository cloned with the organization's token as private, one
// cloned without a token as public, and nothing for a local folder.
func TestSuiteOpsRecordSource(t *testing.T) {
	root := t.TempDir()
	ks := keystore.NewMemory()
	if _, err := ks.Create(context.Background(), "org1", keystore.KindSecret, keystore.Put{Provider: suiteOpsGitHubProvider, Label: "gh", Value: "ghp_x"}, "u"); err != nil {
		t.Fatal(err)
	}
	env := jobEnv{OrgID: "org1", ProjectRoot: root}
	env.Services.Keys = ks
	if err := (suiteOpsRepo{GitHub: "acme/secret"}).recordSource(context.Background(), env, "mine"); err != nil {
		t.Fatal(err)
	}
	if got := privateBenchmarks(t, root); got["mine"] != "acme/secret" {
		t.Fatalf("token clone not private: %v", got)
	}
	env.Services.Keys = nil
	if err := (suiteOpsRepo{GitHub: "acme/open"}).recordSource(context.Background(), env, "open"); err != nil {
		t.Fatal(err)
	}
	if !issuesync.KnownPublic(root, "open") {
		t.Fatal("a clone without a token is public")
	}
	if err := (suiteOpsRepo{Repo: "."}).recordSource(context.Background(), env, "local"); err != nil {
		t.Fatal(err)
	}
	if err := recordRepoSource("", "b", "acme/x", false); err == nil {
		t.Fatal("no project folder must refuse")
	}
}
