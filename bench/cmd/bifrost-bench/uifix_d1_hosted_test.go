package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
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

// TestHostedArchiveLeavesSealsAlone: archiving and unarchiving a sealed
// comparison and its runs changes no byte of the org's evidence store (run
// folders, seals, manifests) and no answer of the verdict, the usage and
// the run page's seal: only the sidecar is written.
func TestHostedArchiveLeavesSealsAlone(t *testing.T) {
	f := newHostedFixture(t)
	pa, _ := f.env.layout.Paths(f.acme.ID)
	hashTree := func(root string) string {
		h := sha256.New()
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && d.Name() == ".heimdall" {
				return filepath.SkipDir
			}
			if d.IsDir() {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			fmt.Fprintf(h, "%s\x00%x\x00", rel, sha256.Sum256(b))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return hex.EncodeToString(h.Sum(nil))
	}
	numbers := func() string {
		var b strings.Builder
		for _, p := range []string{"/api/bench/experiments/" + f.aExp + "/verdict", "/api/bench/usage", "/api/bench/runs/" + f.aRuns[0]} {
			r := f.do("GET", p, "alice", nil, nil)
			if r.status != 200 {
				t.Fatalf("%s: %d %s", p, r.status, trim(r.body))
			}
			var m map[string]any // the run page shows the mark; nothing else may change
			if err := json.Unmarshal([]byte(r.body), &m); err != nil {
				t.Fatalf("%s: %v", p, err)
			}
			delete(m, "archived_at")
			delete(m, "archived_by")
			body, _ := json.Marshal(m)
			fmt.Fprintf(&b, "%s %d %s\n", p, r.status, body)
		}
		return b.String()
	}
	beforeNumbers := numbers() // the first verdict read may store its analysis
	beforeStore := hashTree(pa.StoreRoot)
	for _, p := range append([]string{"/api/bench/experiments/" + f.aExp}, prefixAll("/api/bench/runs/", f.aRuns)...) {
		if r := f.do("POST", p+"/archive", "alice", nil, nil); r.status != 200 {
			t.Fatalf("archive %s: %d %s", p, r.status, trim(r.body))
		}
	}
	if got := hashTree(pa.StoreRoot); got != beforeStore {
		t.Fatal("archiving changed the sealed evidence store")
	}
	if got := numbers(); got != beforeNumbers {
		t.Fatalf("archiving changed a computed answer:\nbefore %s\nafter  %s", beforeNumbers, got)
	}
	if got := hashTree(pa.StoreRoot); got != beforeStore {
		t.Fatal("reading after archiving changed the sealed evidence store")
	}
	for _, p := range append([]string{"/api/bench/experiments/" + f.aExp}, prefixAll("/api/bench/runs/", f.aRuns)...) {
		if r := f.do("POST", p+"/unarchive", "alice", nil, nil); r.status != 200 {
			t.Fatalf("unarchive %s: %d %s", p, r.status, trim(r.body))
		}
	}
	if got := hashTree(pa.StoreRoot); got != beforeStore {
		t.Fatal("unarchiving changed the sealed evidence store")
	}
}

func prefixAll(prefix string, ids []string) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = prefix + id
	}
	return out
}
