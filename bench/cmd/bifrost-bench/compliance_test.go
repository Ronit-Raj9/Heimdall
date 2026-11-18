package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// recordedConfig writes a bench config whose runs root is the read-only
// recorded bundle and returns its path. Nothing is written to the bundle as
// long as the experiment already has an analysis.
func recordedConfig(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../../deploy/bench.yaml")
	if err != nil {
		t.Fatal(err)
	}
	abs := func(p string) string {
		a, err := filepath.Abs(p)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	cfg := string(raw)
	cfg = strings.Replace(cfg, "runs_root: runs", "runs_root: "+abs(recordedRunsRoot), 1)
	cfg = strings.Replace(cfg, "benchmarks_root: benchmarks", "benchmarks_root: "+abs("../../benchmarks"), 1)
	cfg = strings.Replace(cfg, "pricing: deploy/pricing-snapshot.json", "pricing: "+abs("../../deploy/pricing-snapshot.json"), 1)
	dir := filepath.Join(t.TempDir(), "deploy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "bench.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestComplianceReportOnRecordedBundle(t *testing.T) {
	cfg := recordedConfig(t)
	out := t.TempDir()
	if code := runCompliance([]string{"report", "-config", cfg, "-exp", "e_pilot_mixed_nc93", "-o", out}); code != exitOK {
		t.Fatalf("exit %d", code)
	}
	b, err := os.ReadFile(filepath.Join(out, "compliance-e_pilot_mixed_nc93.json"))
	if err != nil {
		t.Fatal(err)
	}
	var p schemas.CompliancePack
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Experiments) != 1 {
		t.Fatalf("experiments %d", len(p.Experiments))
	}
	e := p.Experiments[0]
	if e.CountedRuns == 0 || e.RunsVerified != e.CountedRuns || e.ChainStatus != "verified" || e.AnalysisID == "" {
		t.Fatalf("experiment %+v", e)
	}
	if len(p.Tests) == 0 || p.Tests[0].ID != "condition:clean" {
		t.Fatalf("tests %+v", p.Tests)
	}
	for _, tt := range p.Tests {
		for _, ev := range tt.Evidence {
			if ev.SealHash == "" || !strings.HasPrefix(ev.Path, "e_pilot_mixed_nc93/slots/") {
				t.Fatalf("evidence row %+v", ev)
			}
		}
	}
	md, err := os.ReadFile(filepath.Join(out, "compliance-e_pilot_mixed_nc93.md"))
	if err != nil || !strings.Contains(string(md), "## What this pack does not certify") {
		t.Fatalf("markdown: %v", err)
	}
}

func TestComplianceUsage(t *testing.T) {
	cases := [][]string{
		nil,
		{"export"},
		{"report"},
		{"report", "-exp", "e_x", "-format", "pdf"},
		{"report", "-exp", "e_x,e_y", "-analysis", "a_1"},
	}
	for _, args := range cases {
		if code := runCompliance(args); code != exitUsage {
			t.Errorf("args %v: exit %d, want usage", args, code)
		}
	}
}

func TestPackExpsSplitsCommas(t *testing.T) {
	var l packExps
	for _, v := range []string{"e_a,e_b", " e_c ", ""} {
		if err := l.Set(v); err != nil {
			t.Fatal(err)
		}
	}
	if got := l.String(); got != "e_a,e_b,e_c" {
		t.Fatalf("got %q", got)
	}
}

func TestSealsTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "seals.log")
	if err := os.WriteFile(path, []byte("r_1 aaa\nr_2 bbb\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := sealsTail(path); got != "r_2 bbb" {
		t.Fatalf("tail %q", got)
	}
	if got := sealsTail(filepath.Join(dir, "missing")); got != "" {
		t.Fatalf("missing file tail %q", got)
	}
}
