package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/release"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
)

// TestReleaseExportCompliance releases a copy of the recorded pilot (with an
// override, its verdict is inconclusive) and exports it with -compliance:
// the pack lands beside -o and carries the release.
func TestReleaseExportCompliance(t *testing.T) {
	runs := t.TempDir()
	copyTree(t, filepath.Join(recordedRunsRoot, "e_pilot_mixed_nc93"), filepath.Join(runs, "e_pilot_mixed_nc93"))
	cfg := recordedConfig(t)
	raw, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(recordedRunsRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte(strings.Replace(string(raw), "runs_root: "+abs, "runs_root: "+runs, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	relRoot := t.TempDir()
	t.Setenv(release.RootEnv, relRoot)
	st, err := store.NewFSStore(runs, schemas.NoopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	l, err := release.New(release.Deps{Store: st, Root: relRoot})
	if err != nil {
		t.Fatal(err)
	}
	// The recorded analyses predate inputs digests and are never released
	// (audit qc2-01-03): analyse afresh first.
	a, err := newApp(context.Background(), cfg, appOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.analyze(context.Background(), "e_pilot_mixed_nc93", ""); err != nil {
		t.Fatal(err)
	}
	rec, err := l.Create(context.Background(), &schemas.ReleaseRequest{ExpID: "e_pilot_mixed_nc93", Approver: "test", Override: "test export"})
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "evidence.zip")
	if code := runReleaseExport([]string{rec.ReleaseID, "-config", cfg, "-o", out, "-compliance"}); code != exitOK {
		t.Fatalf("exit %d", code)
	}
	b, err := os.ReadFile(filepath.Join(filepath.Dir(out), "compliance-"+rec.ReleaseID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var p schemas.CompliancePack
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	if p.Release == nil || p.Release.ReleaseID != rec.ReleaseID || p.Release.Override != "test export" || p.Experiments[0].AnalysisID != rec.AnalysisID {
		t.Fatalf("release in pack %+v, experiment %+v", p.Release, p.Experiments[0])
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(out), "compliance-"+rec.ReleaseID+".md")); err != nil {
		t.Fatal(err)
	}
}

func TestExportComplianceOff(t *testing.T) {
	if code := exportCompliance(false, "no-such-config.yaml", nil, "rel_x", ""); code != exitOK {
		t.Fatalf("exit %d", code)
	}
}
