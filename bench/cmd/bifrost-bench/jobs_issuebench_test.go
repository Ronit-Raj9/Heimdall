package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/issuebench"
	"github.com/maximhq/bifrost/bench/issuebench/builder"
	"github.com/maximhq/bifrost/bench/jobs"
)

func issuebenchParams(t *testing.T, root string, mut func(map[string]any)) json.RawMessage {
	t.Helper()
	p := map[string]any{"repo": "acme/tiny", "version": "abc123", "repo_dir": "checkout", "provider": "openai", "model": "m1",
		"max_usd": 2.5, "consent": true, "candidates": []issuebench.Candidate{{Repo: "acme/tiny", PR: 2, Issue: 1, TestFiles: []string{"tests/test_a.py"}}}}
	if mut != nil {
		mut(p)
	}
	b, _ := json.Marshal(p)
	return b
}

func TestIssuebenchBuildValidate(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "checkout"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := jobEnv{ProjectRoot: root}
	if _, target, err := validateIssuebenchBuild(context.Background(), env, issuebenchParams(t, root, nil)); err != nil || target != "acme/tiny@abc123" {
		t.Fatalf("valid params: %v %q", err, target)
	}
	for name, mut := range map[string]func(map[string]any){
		"consent":    func(p map[string]any) { p["consent"] = false },
		"max_usd":    func(p map[string]any) { delete(p, "max_usd") },
		"repo_dir":   func(p map[string]any) { p["repo_dir"] = "../outside" },
		"absolute":   func(p map[string]any) { p["repo_dir"] = "/etc" },
		"candidates": func(p map[string]any) { delete(p, "candidates") },
		"version":    func(p map[string]any) { p["version"] = "a b" },
	} {
		if _, _, err := validateIssuebenchBuild(context.Background(), env, issuebenchParams(t, root, mut)); err == nil || !errors.Is(err, jobs.ErrInvalidParams) {
			t.Fatalf("%s: want invalid params, got %v", name, err)
		}
	}
}

func TestIssuebenchBuildRunClosesModel(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "checkout"), 0o755); err != nil {
		t.Fatal(err)
	}
	closed := false
	oldM, oldS := issuebenchModelFor, issuebenchSandboxFor
	t.Cleanup(func() { issuebenchModelFor, issuebenchSandboxFor = oldM, oldS })
	issuebenchModelFor = func(context.Context, jobEnv, issuebenchBuildParams, string) (builder.Model, func(), error) {
		return &builder.ScriptedModel{}, func() { closed = true }, nil
	}
	issuebenchSandboxFor = func(issuebenchBuildParams) builder.SandboxFactory { return nil }
	log := &testJobLog{}
	res, err := runIssuebenchBuild(context.Background(), jobEnv{ProjectRoot: root}, &jobs.Job{ID: "j_1", Params: issuebenchParams(t, root, nil)}, log, nil)
	if err != nil {
		t.Fatal(err)
	}
	br := res.(issuebench.BuildResult)
	// No language is detected in an empty checkout.
	if br.Stop != issuebench.StopUnsupported || !closed || !strings.Contains(log.String(), "build stopped: unsupported_language") {
		t.Fatalf("%+v closed=%v log=%s", br, closed, log.String())
	}
}
