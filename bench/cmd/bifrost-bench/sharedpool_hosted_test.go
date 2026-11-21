package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/quota"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/sharedpool"
	"github.com/maximhq/bifrost/bench/utils"
)

type leaseAPI struct {
	handlers.WorkerAPI
	leases int
}

func (l *leaseAPI) OrgID() string { return "org1" }
func (l *leaseAPI) Lease(context.Context, string) (*schemas.WorkerLease, error) {
	l.leases++
	return &schemas.WorkerLease{}, nil
}

type killQuota struct {
	quota.Store
	on bool
}

func (k *killQuota) KillSwitch(context.Context) (bool, error) { return k.on, nil }

func TestKillSwitchBlocksLeases(t *testing.T) {
	inner := &leaseAPI{}
	q := &killQuota{Store: quota.NewStub()}
	api := gateWorkers(inner, q)
	if h, ok := api.(handlers.HostedWorkerAPI); !ok || h.OrgID() != "org1" {
		t.Fatal("gate dropped the hosted worker API")
	}
	if l, _ := api.Lease(context.Background(), "w"); l == nil || inner.leases != 1 {
		t.Fatal("lease blocked with kill switch off")
	}
	q.on = true
	if l, _ := api.Lease(context.Background(), "w"); l != nil || inner.leases != 1 {
		t.Fatal("lease handed out with kill switch on")
	}
}

func TestReconcileShared(t *testing.T) {
	loops := map[string]*sharedLoop{}
	started := 0
	start := func(a sharedpool.Assignment, slots int) *sharedLoop {
		started++
		d := make(chan struct{})
		l := &sharedLoop{slots: slots, done: d, policy: &utils.DockerPolicy{}}
		l.cancel = func() { close(d) }
		return l
	}
	as := &sharedpool.Assignments{Assignments: []sharedpool.Assignment{{OrgID: "a", MaxConcurrentRuns: 1}, {OrgID: "b"}}}
	reconcileShared(loops, as, 2, start)
	if len(loops) != 2 || started != 2 || loops["a"].slots != 1 || loops["b"].slots != 2 {
		t.Fatalf("start: %v", loops)
	}
	// Loops claim their tokens; the server then reports those ids.
	loops["a"].setClaimed("t1")
	loops["b"].setClaimed("t2")
	as = &sharedpool.Assignments{Assignments: []sharedpool.Assignment{{OrgID: "a", TokenID: "t1", MaxConcurrentRuns: 1, Images: []string{"x@sha256:1"}}, {OrgID: "b", TokenID: "t2"}}}
	reconcileShared(loops, as, 2, start)
	if started != 2 {
		t.Fatal("restarted unchanged loops")
	}
	if len(loops["a"].policy.Approved) != 1 {
		t.Fatal("approved images not updated in place")
	}
	// b's token was revoked or claimed by someone else: b restarts.
	as.Assignments[1].TokenID = "t9"
	reconcileShared(loops, as, 2, start)
	if started != 3 || loops["b"].claimed() != "" {
		t.Fatalf("revoked token kept its loop: started %d", started)
	}
	// A loop that ended on its own (claim failed) starts again next poll.
	close(loops["a"].done)
	loops["a"].cancel = func() {}
	reconcileShared(loops, as, 2, start)
	if started != 4 {
		t.Fatalf("ended loop not restarted: started %d", started)
	}
	reconcileShared(loops, &sharedpool.Assignments{KillSwitch: true}, 2, start)
	if len(loops) != 0 {
		t.Fatal("kill switch kept loops")
	}
}

func TestResolveSharedRuntime(t *testing.T) {
	cases := []struct {
		want       string
		has        bool
		rt, filter string
		warn, fail bool
	}{
		{"auto", true, "runsc", "", false, false},
		{"auto", false, "runc", "runc", true, false},
		{"runsc", true, "runsc", "runsc", false, false},
		{"runsc", false, "", "", false, true},
		{"runc", true, "runc", "runc", true, false},
		{"runc", false, "runc", "runc", false, false},
	}
	for _, c := range cases {
		rt, filter, warn, err := resolveSharedRuntime(c.want, c.has)
		if (err != nil) != c.fail || rt != c.rt || filter != c.filter || (warn != "") != c.warn {
			t.Errorf("%s has=%v: %q %q %q %v", c.want, c.has, rt, filter, warn, err)
		}
	}
	if !runtimesHaveRunsc([]byte(`{"runc":{"path":"runc"},"runsc":{"path":"/usr/bin/runsc"}}`)) || runtimesHaveRunsc([]byte(`{"runc":{}}`)) || runtimesHaveRunsc([]byte(`x`)) {
		t.Fatal("runtime detection")
	}
	if f, err := parseSharedFlags([]string{"-shared", "-server", "http://x", "-secret-file", "s"}); err != nil || f.runtime != "auto" {
		t.Fatalf("default runtime: %+v %v", f, err)
	}
	if gatewayAlias("http://bifrost:8080") != "bifrost" {
		t.Fatal("gateway alias")
	}
	if orgNetworkName("org_a") != "heimdall-evalnet-org_a" {
		t.Fatal("org network name")
	}
}

// The runner keeps an org's project in step with the server's copy through
// GET /workers/project, sending the org's own token and an ETag.
func TestOrgProjectRefresh(t *testing.T) {
	src := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(src, "benchmarks", "s"), 0o755))
	must(os.WriteFile(filepath.Join(src, "benchmarks", "s", "suite.yaml"), []byte("id: s\n"), 0o644))
	must(os.MkdirAll(filepath.Join(src, ".heimdall", "webhooks"), 0o755))
	must(os.WriteFile(filepath.Join(src, ".heimdall", "webhooks", "hooks.json"), []byte("{}"), 0o600))
	gets, notModified := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != sharedpool.ProjectPath || r.Header.Get("Authorization") != "Bearer hmr_org" {
			w.WriteHeader(403)
			return
		}
		gets++
		tag, _ := sharedpool.ProjectETag(src)
		w.Header().Set("ETag", tag)
		if r.Header.Get("If-None-Match") == tag {
			notModified++
			w.WriteHeader(304)
			return
		}
		_ = sharedpool.WriteProject(w, src)
	}))
	defer srv.Close()
	dst := filepath.Join(t.TempDir(), "org_a")
	p := &orgProject{server: srv.URL, token: "hmr_org", dst: dst, client: srv.Client()}
	must(p.refresh(context.Background()))
	if b, err := os.ReadFile(filepath.Join(dst, "benchmarks", "s", "suite.yaml")); err != nil || string(b) != "id: s\n" {
		t.Fatalf("suite not downloaded: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dst, ".heimdall", "webhooks")); err == nil {
		t.Fatal("org state (.heimdall/webhooks) reached the runner")
	}
	must(p.refresh(context.Background()))
	if notModified != 1 {
		t.Fatalf("unchanged project downloaded again (%d gets, %d not modified)", gets, notModified)
	}
	must(os.Remove(filepath.Join(src, "benchmarks", "s", "suite.yaml")))
	must(p.refresh(context.Background()))
	if _, err := os.Stat(filepath.Join(dst, "benchmarks", "s", "suite.yaml")); err == nil {
		t.Fatal("deleted file kept")
	}
	bad := &orgProject{server: srv.URL, token: "hmr_other", dst: t.TempDir(), client: srv.Client()}
	if err := bad.refresh(context.Background()); err == nil {
		t.Fatal("wrong token accepted")
	}
}
