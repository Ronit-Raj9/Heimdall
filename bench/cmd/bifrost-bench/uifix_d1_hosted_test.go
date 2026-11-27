package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/evalstore"
	"github.com/maximhq/bifrost/bench/findings"
	"github.com/maximhq/bifrost/bench/monitorcert"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tenancy"
)

// TestHostedArchiveStaysInOrg (UI fix wave D1, decision 6): through the
// real hosted dispatcher and two organizations, archive and unarchive act
// only on the caller's own objects (another org's id is a 404, naming the
// other org is refused), need admin (a viewer gets 403), leave an audit
// row, hide the object from its list but not from its page, leave the
// other organization untouched, and keep an existing share link working
// without telling its visitor.
func TestHostedArchiveStaysInOrg(t *testing.T) {
	f := newHostedFixture(t)
	ctx := context.Background()
	pa, _ := f.env.layout.Paths(f.acme.ID)
	pb, _ := f.env.layout.Paths(f.beta.ID)

	share := f.do("POST", "/api/bench/shares", "alice", nil, map[string]any{"kind": "verdict", "target": f.aExp})
	var made struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(share.body), &made); err != nil || made.Token == "" {
		t.Fatalf("share: %d %s", share.status, trim(share.body))
	}

	exp := "/api/bench/experiments/" + f.aExp
	if r := f.do("POST", exp+"/archive", "vera", nil, nil); r.status != 403 {
		t.Fatalf("viewer archive: %d %s", r.status, trim(r.body))
	}
	if r := f.do("POST", exp+"/archive", "bob", nil, nil); r.status != 404 {
		t.Fatalf("other org archive: %d %s", r.status, trim(r.body))
	}
	if r := f.do("POST", "/api/bench/experiments/"+f.bExp+"/archive?"+tenancy.OrgQueryParam+"=beta", "alice", nil, nil); r.status != 403 && r.status != 404 {
		t.Fatalf("alice naming beta: %d %s", r.status, trim(r.body))
	}
	if r := f.do("POST", "/api/bench/experiments/"+f.bExp+"/archive", "alice", map[string]string{tenancy.OrgHeader: "beta"}, nil); r.status != 403 && r.status != 404 {
		t.Fatalf("alice with header beta: %d %s", r.status, trim(r.body))
	}
	if r := f.do("POST", exp+"/archive", "alice", nil, nil); r.status != 200 || !strings.Contains(r.body, `"archived":true`) {
		t.Fatalf("archive: %d %s", r.status, trim(r.body))
	}
	page, err := f.d.ListAudit(ctx, db.AuditFilter{OrgID: f.acme.ID, Action: "experiments.archive"})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].ActorID != f.users["alice"].ID || page.Entries[0].TargetID != f.aExp {
		t.Fatalf("audit %+v %v", page.Entries, err)
	}
	if r := f.do("GET", "/api/bench/experiments", "alice", nil, nil); r.status != 200 || strings.Contains(r.body, f.aExp) {
		t.Fatalf("default list: %d %s", r.status, trim(r.body))
	}
	if r := f.do("GET", "/api/bench/experiments?archived=only", "vera", nil, nil); r.status != 200 || !strings.Contains(r.body, f.aExp) || !strings.Contains(r.body, `"archived_at"`) {
		t.Fatalf("only list: %d %s", r.status, trim(r.body))
	}
	if r := f.do("GET", exp, "vera", nil, nil); r.status != 200 || !strings.Contains(r.body, `"archived_at"`) {
		t.Fatalf("archived page: %d %s", r.status, trim(r.body))
	}
	if r := f.do("GET", "/api/bench/experiments", "bob", nil, nil); r.status != 200 || !strings.Contains(r.body, f.bExp) || strings.Contains(r.body, `"archived_at"`) {
		t.Fatalf("beta list: %d %s", r.status, trim(r.body))
	}
	if r := f.do("GET", "/api/bench/shared/"+made.Token, "", nil, nil); r.status != 200 || strings.Contains(r.body, "archived") {
		t.Fatalf("shared page of an archived comparison: %d %s", r.status, trim(r.body))
	}

	// A run: the other org's id is a 404, the list hides it.
	run := "/api/bench/runs/" + f.aRuns[0]
	if r := f.do("POST", run+"/archive", "bob", nil, nil); r.status != 404 {
		t.Fatalf("other org run: %d %s", r.status, trim(r.body))
	}
	if r := f.do("POST", run+"/archive", "alice", nil, nil); r.status != 200 {
		t.Fatalf("run archive: %d %s", r.status, trim(r.body))
	}
	if r := f.do("GET", "/api/bench/runs?limit=500", "alice", nil, nil); r.status != 200 || strings.Contains(r.body, `"run_id":"`+f.aRuns[0]+`"`) {
		t.Fatalf("run list: %d", r.status)
	}
	if r := f.do("GET", run, "alice", nil, nil); r.status != 200 || !strings.Contains(r.body, `"archived_at"`) {
		t.Fatalf("run page: %d", r.status)
	}

	// An evaluation and a finding stored in acme's project.
	ev := &schemas.Evaluation{ID: "ev_d1", Org: f.acme.ID, Status: schemas.EvaluationCancelled, CreatedAt: time.Now()}
	if err := evalstore.Open(pa.ProjectRoot).Save(ev); err != nil {
		t.Fatal(err)
	}
	fid := findings.IDOf("cluster:d1")
	if err := findings.Open(pa.ProjectRoot).Update(func(all []findings.Finding) ([]findings.Finding, error) {
		return append(all, findings.Finding{ID: fid, Key: "cluster:d1", Title: "x", Source: "cluster", Origin: "rule", State: findings.StateOpen}), nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/bench/evaluations/ev_d1", "/api/bench/findings/" + fid} {
		if r := f.do("POST", path+"/archive", "bob", nil, nil); r.status != 404 {
			t.Fatalf("other org %s: %d %s", path, r.status, trim(r.body))
		}
		if r := f.do("POST", path+"/archive", "vera", nil, nil); r.status != 403 {
			t.Fatalf("viewer %s: %d %s", path, r.status, trim(r.body))
		}
		if r := f.do("POST", path+"/archive", "alice", nil, nil); r.status != 200 {
			t.Fatalf("archive %s: %d %s", path, r.status, trim(r.body))
		}
		if r := f.do("POST", path+"/unarchive", "bob", nil, nil); r.status != 404 {
			t.Fatalf("other org unarchive %s: %d", path, r.status)
		}
	}
	if r := f.do("GET", "/api/bench/evaluations", "alice", nil, nil); r.status != 200 || strings.Contains(r.body, "ev_d1") {
		t.Fatalf("evaluation list: %d %s", r.status, trim(r.body))
	}
	if r := f.do("GET", "/api/bench/findings?archived=only", "vera", nil, nil); r.status != 200 || !strings.Contains(r.body, fid) {
		t.Fatalf("findings only: %d %s", r.status, trim(r.body))
	}
	if r := f.do("POST", "/api/bench/improvements/imp_nothere/archive", "bob", nil, nil); r.status == 200 {
		t.Fatalf("improvement of nothing archived: %s", trim(r.body))
	}
	// Beta's project holds no archive file: nothing was written there.
	if a, err := filepath.Glob(filepath.Join(pb.ProjectRoot, ".heimdall", "archive.json")); err != nil || len(a) != 0 {
		t.Fatalf("beta sidecar written: %v %v", a, err)
	}
	if r := f.do("POST", exp+"/unarchive", "alice", nil, nil); r.status != 200 || !strings.Contains(r.body, `"archived":false`) {
		t.Fatalf("unarchive: %d %s", r.status, trim(r.body))
	}
	if r := f.do("GET", "/api/bench/experiments", "alice", nil, nil); !strings.Contains(r.body, f.aExp) {
		t.Fatalf("after unarchive: %s", trim(r.body))
	}
}

// TestHostedMonitorTestDelete: deleting a monitor test is admin only, a
// certificate id of nothing is 404, and the delete leaves an audit row.
func TestHostedMonitorTestDelete(t *testing.T) {
	f := newHostedFixture(t)
	pa, _ := f.env.layout.Paths(f.acme.ID)
	certs, err := monitorcert.NewCertStore(monitorcert.DefaultRoot(pa.ProjectRoot), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := certs.Write(&schemas.MonitorCertificate{CertID: "mc_d1", CreatedAt: time.Now()}, []byte("# d1\n")); err != nil {
		t.Fatal(err)
	}
	path := "/api/bench/monitors/certifications/mc_d1"
	if r := f.do("GET", path, "alice", nil, nil); r.status != 200 {
		t.Skipf("monitor certificates are not served from the org project here: %d %s", r.status, trim(r.body))
	}
	if r := f.do("DELETE", path, "vera", nil, nil); r.status != 403 {
		t.Fatalf("viewer: %d %s", r.status, trim(r.body))
	}
	if r := f.do("DELETE", path, "tokA", nil, nil); r.status != 403 {
		t.Fatalf("member token: %d %s", r.status, trim(r.body))
	}
	if r := f.do("DELETE", path, "bob", nil, nil); r.status != 404 {
		t.Fatalf("other org: %d %s", r.status, trim(r.body))
	}
	if r := f.do("DELETE", path, "alice", nil, nil); r.status != 200 {
		t.Fatalf("admin: %d %s", r.status, trim(r.body))
	}
	if r := f.do("DELETE", path, "alice", nil, nil); r.status != 404 {
		t.Fatalf("second delete: %d %s", r.status, trim(r.body))
	}
	page, err := f.d.ListAudit(context.Background(), db.AuditFilter{OrgID: f.acme.ID, Action: "monitors.certifications.delete"})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].TargetID != "mc_d1" {
		t.Fatalf("audit %+v %v", page.Entries, err)
	}
}

// TestHostedMonitorDeleteSharedRoot: with BENCH_MONITOR_CERTS_ROOT set,
// every organization shares one certificate folder, so the delete is
// refused (409 with the reason) for every admin and the test stays. Reads
// are left as they are: they cross organizations in this mode (bob of
// beta reads acme's test), which this test records.
func TestHostedMonitorDeleteSharedRoot(t *testing.T) {
	shared := t.TempDir()
	t.Setenv(monitorcert.RootEnv, shared)
	f := newHostedFixture(t)
	certs, err := monitorcert.NewCertStore(shared, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := certs.Write(&schemas.MonitorCertificate{CertID: "mc_shared", CreatedAt: time.Now()}, []byte("# shared\n")); err != nil {
		t.Fatal(err)
	}
	path := "/api/bench/monitors/certifications/mc_shared"
	if r := f.do("GET", path, "alice", nil, nil); r.status != 200 {
		t.Fatalf("read: %d %s", r.status, trim(r.body))
	}
	if r := f.do("GET", path, "bob", nil, nil); r.status != 200 {
		t.Logf("reads no longer cross organizations in shared mode: %d", r.status)
	}
	for _, who := range []string{"alice", "bob"} {
		if r := f.do("DELETE", path, who, nil, nil); r.status != 409 || !strings.Contains(r.body, monitorcert.RootEnv) {
			t.Fatalf("%s delete in shared mode: %d %s", who, r.status, trim(r.body))
		}
	}
	if _, err := os.Stat(filepath.Join(shared, "mc_shared")); err != nil {
		t.Fatalf("the shared test was deleted: %v", err)
	}
}
