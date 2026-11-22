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

	"github.com/maximhq/bifrost/bench/db/dbtest"
	"github.com/maximhq/bifrost/bench/evalstore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/reference"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/sharelinks"
	"github.com/maximhq/bifrost/bench/tenancy"
)

// refFixture imports the recorded sealed runs into a reference
// organization on disk (no database) and returns its target.
func refFixture(t *testing.T) (refTarget, []string) {
	t.Helper()
	ctx := context.Background()
	storage := t.TempDir()
	layout := tenancy.Layout{Hosted: true, StorageRoot: storage}
	p, err := layout.Paths("org_reference")
	if err != nil {
		t.Fatal(err)
	}
	if err := tenancy.EnsureProject(p, ""); err != nil {
		t.Fatal(err)
	}
	rep, err := tenancy.ImportRuns(ctx, recordedRoot, p.StoreRoot, nil)
	if err != nil || rep.Imported != 2 {
		t.Fatalf("import %+v %v", rep, err)
	}
	var ids []string
	for _, e := range rep.Experiments {
		ids = append(ids, e.ExpID)
	}
	now := time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)
	return refTarget{
		orgID: "org_reference", paths: p, storageRoot: storage, logger: schemas.NoopLogger{},
		library: func(string) bool { return true }, now: func() time.Time { return now },
	}, ids
}

func TestReferenceImportPublishUnpublish(t *testing.T) {
	ctx := context.Background()
	tgt, ids := refFixture(t)
	wrapped, err := tgt.wrapImported(ctx, ids, "")
	if err != nil || len(wrapped) != 2 {
		t.Fatalf("wrapped %+v %v", wrapped, err)
	}
	for _, w := range wrapped {
		if w.Status != "created" {
			t.Fatalf("wrap %+v", w)
		}
	}
	// A second import keeps what is there.
	again, err := tgt.wrapImported(ctx, ids, "")
	if err != nil || len(again) != 0 {
		t.Fatalf("second wrap made %+v %v", again, err)
	}

	pilot := reference.WrapID("e_pilot_mixed_nc93")
	res, err := tgt.publish(ctx, pilot, "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 2 || !strings.HasPrefix(res.ReportPath, "/s/") {
		t.Fatalf("published %+v", res)
	}
	seals, err := os.ReadFile(filepath.Join(tgt.paths.StoreRoot, "e_pilot_mixed_nc93", "seals.log"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(seals)), "\n")
	head := strings.Fields(lines[len(lines)-1])[1]
	for _, e := range res.Entries {
		if e.Runs == 0 || e.Tasks == 0 || e.SealHash != head || e.SealedRuns != len(lines) || e.RanAt.IsZero() || e.Benchmark != "coding-core" || e.Model != "gpt-5.4-mini" {
			t.Fatalf("entry %+v", e)
		}
		if e.Score < e.CILow || e.Score > e.CIHigh {
			t.Fatalf("score outside its interval: %+v", e)
		}
	}
	// The board is what the public route serves.
	b, err := reference.Open(tgt.storageRoot).Load()
	if err != nil || len(b.Entries) != 2 {
		t.Fatalf("board %+v %v", b, err)
	}
	// The report card link opens for anyone and never expires.
	links := sharelinks.Open(tgt.paths.ProjectRoot)
	tok := strings.TrimPrefix(res.ReportPath, "/s/")
	sh, err := links.Verify("org_reference", tok)
	if err != nil || sh.Kind != sharelinks.KindEvaluation || sh.Target != pilot || sh.ExpiresAt != nil {
		t.Fatalf("share %+v %v", sh, err)
	}
	// Publishing again keeps the same link.
	res2, err := tgt.publish(ctx, pilot, "test")
	if err != nil || res2.ReportPath != res.ReportPath {
		t.Fatalf("republish %q vs %q: %v", res2.ReportPath, res.ReportPath, err)
	}

	// Practice runs (the scripted model) are never a reference score.
	if _, err := tgt.publish(ctx, reference.WrapID("e_smoke_sdoj"), "test"); err == nil {
		t.Fatal("scripted practice runs published")
	}

	removed, err := tgt.unpublish(pilot, "test")
	if err != nil || len(removed) != 2 {
		t.Fatalf("unpublish %v %v", removed, err)
	}
	if _, err := links.Verify("org_reference", tok); !errors.Is(err, sharelinks.ErrRevoked) {
		t.Fatalf("link still open after unpublish: %v", err)
	}
	if b, _ := reference.Open(tgt.storageRoot).Load(); len(b.Entries) != 0 {
		t.Fatalf("board after unpublish %+v", b.Entries)
	}
}

func TestReferencePublishRefusesTamperedRuns(t *testing.T) {
	ctx := context.Background()
	tgt, ids := refFixture(t)
	if _, err := tgt.wrapImported(ctx, ids, ""); err != nil {
		t.Fatal(err)
	}
	var target string
	_ = filepath.WalkDir(filepath.Join(tgt.paths.StoreRoot, "e_pilot_mixed_nc93"), func(p string, d os.DirEntry, err error) error {
		if err == nil && target == "" && !d.IsDir() && d.Name() == "metrics.json" {
			target = p
		}
		return nil
	})
	if target == "" {
		t.Fatal("no metrics.json")
	}
	_ = os.Chmod(filepath.Dir(target), 0o755)
	_ = os.Chmod(target, 0o644)
	if err := os.WriteFile(target, []byte(`{"success":{"value":1}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := tgt.publish(ctx, reference.WrapID("e_pilot_mixed_nc93"), "test")
	if err == nil || !strings.Contains(err.Error(), "does not verify") {
		t.Fatalf("tampered runs published: %v", err)
	}
	if b, _ := reference.Open(tgt.storageRoot).Load(); len(b.Entries) != 0 {
		t.Fatalf("board %+v", b.Entries)
	}
}

func TestReferencePublishNeedsLibrary(t *testing.T) {
	ctx := context.Background()
	tgt, ids := refFixture(t)
	if _, err := tgt.wrapImported(ctx, ids, ""); err != nil {
		t.Fatal(err)
	}
	pilot := reference.WrapID("e_pilot_mixed_nc93")
	tgt.library = nil
	if _, err := tgt.publish(ctx, pilot, "test"); err == nil {
		t.Fatal("published without the library")
	}
	tgt.library = func(string) bool { return false }
	if _, err := tgt.publish(ctx, pilot, "test"); err == nil || !strings.Contains(err.Error(), "library") {
		t.Fatalf("own benchmark published: %v", err)
	}
	if _, err := tgt.publish(ctx, "ev_missing", "test"); err == nil {
		t.Fatal("unknown evaluation published")
	}
}

func TestReferenceImportCopiesEvaluationFiles(t *testing.T) {
	ctx := context.Background()
	tgt, ids := refFixture(t)
	dir := t.TempDir()
	ev := schemas.Evaluation{
		ID: "ev_fleet_codex", Org: "local", Status: schemas.EvaluationDone, CreatedAt: time.Now().UTC(),
		Spec: schemas.EvaluationSpec{Mode: schemas.EvaluationModeOpponent, Benchmarks: []string{"coding-core"}, Arms: []schemas.EvaluationArm{
			{Label: "A", Agent: "codex", Model: "gpt-5.4-mini"}, {Label: "B", Agent: "custom-python", Model: "gpt-5.4-mini"},
		}},
		Children: []schemas.EvaluationChild{{Benchmark: "coding-core", ExperimentID: "e_pilot_mixed_nc93", Status: schemas.EvaluationDone}},
	}
	b, _ := json.Marshal(ev)
	if err := os.WriteFile(filepath.Join(dir, ev.ID+".json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	wrapped, err := tgt.wrapImported(ctx, ids, dir)
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]string{}
	for _, w := range wrapped {
		statuses[w.Evaluation] = w.Status
	}
	if statuses["ev_fleet_codex"] != "copied" || statuses[reference.WrapID("e_smoke_sdoj")] != "created" || statuses[reference.WrapID("e_pilot_mixed_nc93")] != "" {
		t.Fatalf("statuses %v", statuses)
	}
	got, err := evalstore.Open(tgt.paths.ProjectRoot).Load("ev_fleet_codex")
	if err != nil || got.Org != "org_reference" {
		t.Fatalf("copied %+v %v", got, err)
	}
	if _, err := tgt.publish(ctx, "ev_fleet_codex", "test"); err != nil {
		t.Fatal(err)
	}
}

func TestReferenceOrgCreatedOnce(t *testing.T) {
	ctx := context.Background()
	d := dbtest.OpenSQLite(t)
	cfg := &lib.Config{RunsRoot: t.TempDir()}
	if _, err := referenceOrg(ctx, cfg, d, false, ""); err == nil {
		t.Fatal("found a reference organization that does not exist")
	}
	a, err := referenceOrg(ctx, cfg, d, true, "")
	if err != nil || a.Slug != reference.OrgSlug || a.PublicRead {
		t.Fatalf("created %+v %v", a, err)
	}
	b, err := referenceOrg(ctx, cfg, d, true, "")
	if err != nil || b.ID != a.ID {
		t.Fatalf("second call %+v %v", b, err)
	}
	tgt, err := newRefTarget(cfg, b, nil)
	if err != nil || tgt.paths.StoreRoot == "" || tgt.storageRoot != hostedLayout(cfg).StorageRoot {
		t.Fatalf("target %+v %v", tgt, err)
	}
}

// The hosted server serves the published board to anyone, signed in or
// not, and never the board's share link ids.
func TestHostedReferenceBoardIsPublic(t *testing.T) {
	f := newHostedFixture(t)
	if r := f.do("GET", "/api/bench/reference/board", "", nil, nil); r.status != 200 || !strings.Contains(r.body, `"entries":[]`) {
		t.Fatalf("empty board: %d %s", r.status, r.body)
	}
	now := time.Now().UTC()
	if _, err := reference.Open(f.env.layout.StorageRoot).Publish("ev_ref_x", []reference.Entry{{ID: "ev_ref_x:tau2:A", EvaluationID: "ev_ref_x", Benchmark: "tau2", Agent: "codex", Model: "m", Runs: 4, SealHash: "sha256:abc"}}, "shr_0123456789abcdef0123", now); err != nil {
		t.Fatal(err)
	}
	r := f.do("GET", "/api/bench/reference/board?benchmark=tau2", "", nil, nil)
	if r.status != 200 || !strings.Contains(r.body, "sha256:abc") || strings.Contains(r.body, "shr_") {
		t.Fatalf("board: %d %s", r.status, r.body)
	}
	if r := f.do("GET", "/api/bench/reference/board", "alice", nil, nil); r.status != 200 {
		t.Fatalf("signed in: %d %s", r.status, r.body)
	}
}
