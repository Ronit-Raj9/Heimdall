package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tenancy"
)

// TestHostedDeleteComparisonAndReadAudit: an admin deletes a comparison
// (folder, index), a viewer may not; reading a trace leaves an audit row
// (lane X16).
func TestHostedDeleteComparisonAndReadAudit(t *testing.T) {
	f := newHostedFixture(t)
	ctx := context.Background()
	if r := f.do("GET", "/api/bench/runs/"+f.aRuns[0]+"/trace", "alice", nil, nil); r.status != 200 {
		t.Fatalf("trace: %d %s", r.status, trim(r.body))
	}
	page, err := f.d.ListAudit(ctx, db.AuditFilter{OrgID: f.acme.ID, Action: "runs.trace.read"})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].ActorID != f.users["alice"].ID || page.Entries[0].TargetID != f.aRuns[0] {
		t.Fatalf("trace read audit %+v %v", page.Entries, err)
	}

	p, _ := f.env.layout.Paths(f.acme.ID)
	if r := f.do("DELETE", "/api/bench/experiments/"+f.aExp, "vera", nil, nil); r.status != 403 {
		t.Fatalf("viewer delete: %d %s", r.status, trim(r.body))
	}
	if r := f.do("DELETE", "/api/bench/experiments/"+f.aExp, "alice", nil, nil); r.status != 200 || !strings.Contains(r.body, `"kind":"experiment"`) {
		t.Fatalf("delete: %d %s", r.status, trim(r.body))
	}
	if _, err := os.Stat(p.StoreRoot + "/" + f.aExp); !os.IsNotExist(err) {
		t.Fatalf("folder kept: %v", err)
	}
	if r := f.do("GET", "/api/bench/experiments/"+f.aExp, "alice", nil, nil); r.status != 404 {
		t.Fatalf("read after delete: %d %s", r.status, trim(r.body))
	}
	if r := f.do("DELETE", "/api/bench/experiments/"+f.aExp, "alice", nil, nil); r.status != 404 {
		t.Fatalf("second delete: %d %s", r.status, trim(r.body))
	}
	if page, _ := f.d.ListAudit(ctx, db.AuditFilter{OrgID: f.acme.ID, Action: "experiments.delete"}); len(page.Entries) != 1 {
		t.Fatalf("delete audit %+v", page.Entries)
	}
	// Org B is untouched.
	if r := f.do("GET", "/api/bench/experiments/"+f.bExp, "bob", nil, nil); r.status != 200 {
		t.Fatalf("other org: %d", r.status)
	}
}

// TestHostedRetentionSweep: the setting round-trips through the API and
// the sweeper deletes only comparisons older than it.
func TestHostedRetentionSweep(t *testing.T) {
	f := newHostedFixture(t)
	ctx := context.Background()
	if r := f.do("PUT", "/api/bench/data/retention", "alice", nil, map[string]int{"run_days": 99999}); r.status != 400 {
		t.Fatalf("out of range: %d %s", r.status, trim(r.body))
	}
	if r := f.do("PUT", "/api/bench/data/retention", "vera", nil, map[string]int{"run_days": 30}); r.status != 403 {
		t.Fatalf("viewer set: %d", r.status)
	}
	if r := f.do("PUT", "/api/bench/data/retention", "alice", nil, map[string]int{"run_days": 30}); r.status != 200 {
		t.Fatalf("set: %d %s", r.status, trim(r.body))
	}
	var got handlers.RetentionInfo
	r := f.do("GET", "/api/bench/data/retention", "vera", nil, nil)
	if err := json.Unmarshal([]byte(r.body), &got); err != nil || got.RunDays != 30 || got.MaxDays != handlers.RetentionMaxDays {
		t.Fatalf("get %+v %v %s", got, err, r.body)
	}
	var target retentionTarget
	for _, tg := range hostedRetentionTargets(f.env) {
		if tg.orgID == f.acme.ID {
			target = tg
		}
	}
	if target.orgID == "" {
		t.Fatal("acme not swept")
	}
	// Today nothing is old enough.
	if del, err := sweepRetention(ctx, target, time.Now(), f.env.base.logger); err != nil || len(del) != 0 {
		t.Fatalf("early sweep %v %v", del, err)
	}
	del, err := sweepRetention(ctx, target, time.Now().AddDate(30, 0, 0), f.env.base.logger)
	if err != nil || len(del) != 1 || del[0] != f.aExp {
		t.Fatalf("sweep %v %v", del, err)
	}
	if r := f.do("GET", "/api/bench/experiments/"+f.aExp, "alice", nil, nil); r.status != 404 {
		t.Fatalf("swept comparison still served: %d", r.status)
	}
	info, _ := handlers.ReadRetention(target.paths.ProjectRoot)
	if info.LastDeleted != 1 || info.LastSweepAt == nil {
		t.Fatalf("sweep status %+v", info)
	}
	if page, _ := f.d.ListAudit(ctx, db.AuditFilter{OrgID: f.acme.ID, Action: "data.retention.delete"}); len(page.Entries) != 1 {
		t.Fatalf("retention audit %+v", page.Entries)
	}
}

// TestHostedEvaluationDelete: a finished evaluation goes with its
// comparisons; a running one is refused.
func TestHostedEvaluationDelete(t *testing.T) {
	f := newHostedFixture(t)
	p, _ := f.env.layout.Paths(f.acme.ID)
	ev := &schemas.Evaluation{ID: "ev_test", Org: f.acme.ID, Status: schemas.EvaluationDone, CreatedAt: time.Now().UTC(),
		Children: []schemas.EvaluationChild{{Benchmark: "coding-core", ExperimentID: f.aExp, Status: string(schemas.ExperimentDone)}}}
	saveEval := func() {
		b, _ := json.Marshal(ev)
		if err := os.MkdirAll(p.ProjectRoot+"/.heimdall/evaluations", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p.ProjectRoot+"/.heimdall/evaluations/ev_test.json", b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	saveEval()
	if r := f.do("DELETE", "/api/bench/evaluations/ev_test", "tokA", nil, nil); r.status != 403 {
		t.Fatalf("member delete: %d %s", r.status, trim(r.body))
	}
	r := f.do("DELETE", "/api/bench/evaluations/ev_test", "alice", nil, nil)
	if r.status != 200 || !strings.Contains(r.body, f.aExp) {
		t.Fatalf("delete: %d %s", r.status, trim(r.body))
	}
	if r := f.do("GET", "/api/bench/evaluations/ev_test", "alice", nil, nil); r.status != 404 {
		t.Fatalf("evaluation kept: %d", r.status)
	}
	if _, err := os.Stat(p.StoreRoot + "/" + f.aExp); !os.IsNotExist(err) {
		t.Fatalf("child comparison kept: %v", err)
	}
}

// TestHostedOrgDeletion: owner only, typed slug, access gone at once, the
// worker removes folders and rows, the audit chain stays and verifies.
func TestHostedOrgDeletion(t *testing.T) {
	f := newHostedFixture(t)
	ctx := context.Background()
	p, _ := f.env.layout.Paths(f.acme.ID)
	if r := f.do("POST", "/api/bench/orgs/acme/delete", "tokA", nil, map[string]string{"confirm": "acme"}); r.status != 403 {
		t.Fatalf("token delete: %d %s", r.status, trim(r.body))
	}
	if r := f.do("POST", "/api/bench/orgs/acme/delete", "vera", nil, map[string]string{"confirm": "acme"}); r.status != 403 {
		t.Fatalf("viewer delete: %d %s", r.status, trim(r.body))
	}
	if r := f.do("POST", "/api/bench/orgs/acme/delete", "alice", nil, map[string]string{"confirm": "ACME"}); r.status != 400 {
		t.Fatalf("wrong confirm: %d %s", r.status, trim(r.body))
	}
	r := f.do("POST", "/api/bench/orgs/acme/delete", "alice", nil, map[string]string{"confirm": "acme"})
	if r.status != 202 || !strings.Contains(r.body, `"status":"scheduled"`) {
		t.Fatalf("delete: %d %s", r.status, trim(r.body))
	}
	// Membership is gone: alice's session no longer reaches the org.
	f.as["alice"].Role = ""
	if r := f.do("GET", "/api/bench/orgs/acme", "alice", nil, nil); r.status < 400 {
		t.Fatalf("org still readable: %d %s", r.status, trim(r.body))
	}
	pending, err := f.d.PendingOrgDeletions(ctx)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending %+v %v", pending, err)
	}
	runOrgDeletion(ctx, f.env, pending[0])
	od, err := f.d.OrgDeletionOf(ctx, f.acme.ID)
	if err != nil || od.Status != db.OrgDeletionDone {
		t.Fatalf("receipt %+v %v", od, err)
	}
	for _, dir := range []string{p.StoreRoot, p.ProjectRoot} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("%s kept: %v", dir, err)
		}
	}
	if _, err := f.d.OrgByID(ctx, f.acme.ID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("org row kept: %v", err)
	}
	rep, err := f.d.VerifyAuditChain(ctx, f.acme.ID)
	if err != nil || !rep.OK {
		t.Fatalf("audit after delete %+v %v", rep, err)
	}
	for _, action := range []string{"org.delete.requested", "org.delete.completed"} {
		if page, _ := f.d.ListAudit(ctx, db.AuditFilter{OrgID: f.acme.ID, Action: action}); len(page.Entries) != 1 {
			t.Fatalf("%s rows %+v", action, page.Entries)
		}
	}
	// Org B still works.
	if r := f.do("GET", "/api/bench/experiments/"+f.bExp, "bob", nil, nil); r.status != 200 {
		t.Fatalf("other org: %d", r.status)
	}
}

// TestHostedAuditExportVerify: both export formats verify offline; one
// edited byte breaks the chain.
func TestHostedAuditExportVerify(t *testing.T) {
	f := newHostedFixture(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := f.d.AppendAudit(ctx, db.AuditEntry{OrgID: f.beta.ID, ActorType: db.ActorUser, ActorID: "usr_x", Action: "x.y",
			Details: json.RawMessage(`{"i":` + string(rune('0'+i)) + `}`)}); err != nil {
			t.Fatal(err)
		}
	}
	r := f.do("GET", "/api/bench/orgs/beta/audit/verify", "bob", nil, nil)
	var api handlers.AuditVerifyReport
	if err := json.Unmarshal([]byte(r.body), &api); r.status != 200 || err != nil || !api.OK || api.Chained < 3 {
		t.Fatalf("api verify %d %+v %v", r.status, api, err)
	}
	if r := f.do("GET", "/api/bench/orgs/beta/audit/export?format=xml", "bob", nil, nil); r.status != 400 {
		t.Fatalf("bad format: %d", r.status)
	}
	if r := f.do("GET", "/api/bench/orgs/beta/audit/export", "alice", nil, nil); r.status < 400 {
		t.Fatalf("other org's export: %d", r.status)
	}
	for _, format := range []string{"jsonl", "csv"} {
		r := f.do("GET", "/api/bench/orgs/beta/audit/export?format="+format, "bob", nil, nil)
		if r.status != 200 {
			t.Fatalf("%s export: %d %s", format, r.status, trim(r.body))
		}
		rep, err := verifyAuditExport(strings.NewReader(r.body), format == "csv")
		if err != nil || !rep.OK || rep.Chained < 3 {
			t.Fatalf("%s offline verify %+v %v", format, rep, err)
		}
		bad := strings.Replace(r.body, "usr_x", "usr_y", 1)
		if rep, err := verifyAuditExport(strings.NewReader(bad), format == "csv"); err != nil || rep.OK {
			t.Fatalf("%s tampered export verified: %+v %v", format, rep, err)
		}
	}
	// The export itself is a logged read.
	if page, _ := f.d.ListAudit(ctx, db.AuditFilter{OrgID: f.beta.ID, Action: "orgs.audit.export.read"}); len(page.Entries) != 2 {
		t.Fatalf("export reads %+v", page.Entries)
	}
	var out bytes.Buffer
	printAuditReport(&out, db.AuditChainReport{OK: false, BrokenAt: 4, Reason: "x", Legacy: 1})
	if !strings.Contains(out.String(), "BROKEN at entry #4") || !strings.Contains(out.String(), "predate") {
		t.Fatalf("report text %q", out.String())
	}
}

func TestParseRateLimits(t *testing.T) {
	cases := []struct {
		in      string
		want    tenancy.RateLimitConfig
		wantErr bool
	}{
		{"", tenancy.DefaultRateLimits, false},
		{"off", tenancy.RateLimitConfig{}, false},
		{"10,2,100,0", tenancy.RateLimitConfig{PerCredential: auth.RateLimit{Burst: 10, Every: 500 * time.Millisecond}}, false},
		{"1,2,3", tenancy.RateLimitConfig{}, true},
		{"a,b,c,d", tenancy.RateLimitConfig{}, true},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := parseRateLimits(c.in)
			if (err != nil) != c.wantErr || (!c.wantErr && got != c.want) {
				t.Fatalf("got %+v %v", got, err)
			}
		})
	}
}

func TestEraseExperimentDataRefusesPaths(t *testing.T) {
	root := t.TempDir()
	for _, id := range []string{"../x", "", "A"} {
		if err := eraseExperimentData(context.Background(), "local", root, id); !errors.Is(err, schemas.ErrNotFound) {
			t.Fatalf("%q: %v", id, err)
		}
	}
}
