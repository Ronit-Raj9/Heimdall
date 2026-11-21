package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/schemas"
)

func TestRunnerJobKindsHosted(t *testing.T) {
	want := []string{"agent.build", "scan.mcp", "scan.skill", "monitor.certify", "gpu.check", "voice.check",
		"visual.capture", "visual.compare", "simulator.spread"}
	for _, k := range want {
		var spec *jobKindSpec
		jobKindsMu.Lock()
		for i := range jobKinds {
			if jobKinds[i].Kind == k {
				spec = &jobKinds[i]
			}
		}
		jobKindsMu.Unlock()
		if spec == nil || !spec.Local || !spec.Hosted {
			t.Fatalf("%s: want a local and a hosted variant, got %+v", k, spec)
		}
	}
}

func TestRunnerJobArgv(t *testing.T) {
	argv, err := agentBuildArgs(json.RawMessage(`{"agent":"agents/codex.yaml","suite":"coding-core"}`))
	if err != nil || strings.Join(argv, " ") != "agent build -config {{config}} -suite coding-core agents/codex.yaml" {
		t.Fatalf("agent.build argv %v %v", argv, err)
	}
	if _, err := agentBuildArgs(json.RawMessage(`{"agent":"../x.yaml"}`)); err == nil {
		t.Fatal("agent outside the project accepted")
	}
	if got := strings.Join(withConfigFlag([]string{"voice", "check", "-suite", "s", "-json"}), " "); got != "voice check -config {{config}} -suite s -json" {
		t.Fatalf("withConfigFlag: %s", got)
	}
	if got := strings.Join(withConfigFlag([]string{"visual", "capture", "-out", "x"}), " "); got != "visual capture -out x" {
		t.Fatalf("visual capture takes no -config: %s", got)
	}
	argv, _ = scanSkillArgs(json.RawMessage(`{"path":"uploads/skill","dynamic":true}`))
	if strings.Join(argv, " ") != "skill scan -config {{config}} -json -dynamic uploads/skill" {
		t.Fatalf("scan.skill dynamic argv %v", argv)
	}
}

func TestRunnerJobNoRunnerFailsFast(t *testing.T) {
	spec, ok := runnerJobHostedSpec("gpu.check")
	if !ok {
		t.Fatal("gpu.check hosted variant missing")
	}
	j := &jobs.Job{ID: "j1", Params: json.RawMessage(`{}`)}
	start := time.Now()
	_, err := spec.Run(context.Background(), jobEnv{Hosted: true, ProjectRoot: t.TempDir()}, j, discardLogger{}, nil)
	if err == nil || !strings.Contains(err.Error(), "No worker online") || !strings.Contains(err.Error(), "Settings > Workers") ||
		!strings.Contains(err.Error(), "Add a worker") {
		t.Fatalf("no runner: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("not fast")
	}
}

func TestRunnerJobSnapshotAndUnpack(t *testing.T) {
	root := t.TempDir()
	for p, body := range map[string]string{
		"agents/a.yaml": "id: a", "policies/p.yaml": "x", "uploads/skill/SKILL.md": "s",
		"monitors/m.yaml": "monitor", ".git/config": "secret", "runs/big": "no",
	} {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	snap, skipped, err := projectSnapshot(root, []string{"monitor", "certify", "-monitor", "monitors/m.yaml", "-config", cliConfig})
	if err != nil || len(skipped) != 0 {
		t.Fatalf("snapshot: %v %v", err, skipped)
	}
	zr, err := zip.NewReader(bytes.NewReader(snap), int64(len(snap)))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	for _, want := range []string{"agents/a.yaml", "policies/p.yaml", "uploads/skill/SKILL.md", "monitors/m.yaml"} {
		if !names[want] {
			t.Fatalf("snapshot misses %s: %v", want, names)
		}
	}
	if names[".git/config"] || names["runs/big"] {
		t.Fatalf("snapshot carries files it must not: %v", names)
	}
	dst := t.TempDir()
	if err := unpackJobFiles(snap, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "monitors", "m.yaml")); string(b) != "monitor" {
		t.Fatal("unpack lost a file")
	}
	// A zip entry that leaves the project is refused.
	var bad bytes.Buffer
	zw := zip.NewWriter(&bad)
	w, _ := zw.Create("../escape")
	w.Write([]byte("x"))
	zw.Close()
	if err := unpackJobFiles(bad.Bytes(), t.TempDir()); err == nil {
		t.Fatal("escaping entry accepted")
	}
}

func TestRunnerJobWriteBack(t *testing.T) {
	root := t.TempDir()
	env := jobEnv{ProjectRoot: root}
	if err := writeRunnerFile(context.Background(), env, "monitor.certify", "a-1",
		engine.JobResultFile{Path: "monitor-certs/m/cert.json", Data: []byte("{}")}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "monitor-certs", "m", "cert.json")); string(b) != "{}" {
		t.Fatal("certificate not written")
	}
	for _, p := range []string{"deploy/bench.yaml", ".git/x", "../x", "x"} {
		if err := writeRunnerFile(context.Background(), env, "k", "a-1", engine.JobResultFile{Path: p, Data: []byte("x")}); err == nil {
			t.Fatalf("%s accepted", p)
		}
	}
	// uploads/ goes through projectfs; without a store it says so.
	if err := writeRunnerFile(context.Background(), env, "k", "a-1", engine.JobResultFile{Path: "uploads/visual/c.png", Data: []byte("x")}); err == nil {
		t.Fatal("uploads written without projectfs")
	}
}

func TestRunnerJobCollect(t *testing.T) {
	dir := t.TempDir()
	start := time.Now().Add(-time.Second)
	p := filepath.Join(dir, "uploads", "visual", "c.png")
	os.MkdirAll(filepath.Dir(p), 0o700)
	os.WriteFile(p, []byte("png"), 0o600)
	got, err := collectJobFiles(dir, []string{"uploads/visual/c.png", "monitor-certs", "../etc"}, start)
	if err != nil || len(got) != 1 || got[0] != "uploads/visual/c.png" {
		t.Fatalf("collect %v %v", got, err)
	}
	var lines []string
	l := &lineLogger{log: func(s string) { lines = append(lines, s) }}
	l.Write([]byte("a\nb"))
	l.Write([]byte("c\n"))
	if strings.Join(lines, "|") != "a|bc" {
		t.Fatalf("line logger %q", lines)
	}
	_ = schemas.LeaseKindJob
}

func runnerJobHostedSpec(kind string) (jobKindSpec, bool) {
	jobKindsMu.Lock()
	defer jobKindsMu.Unlock()
	for _, k := range jobKinds {
		if k.Kind == kind && k.Hosted {
			return k, true
		}
	}
	return jobKindSpec{}, false
}

type discardLogger struct{}

func (discardLogger) Printf(string, ...any)       {}
func (discardLogger) Warnf(string, ...any)        {}
func (discardLogger) Errorf(string, ...any)       {}
func (discardLogger) Write(p []byte) (int, error) { return len(p), nil }
