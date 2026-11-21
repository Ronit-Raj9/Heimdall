package k8s

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func anchorObj(run, uid string) map[string]any {
	return map[string]any{"metadata": map[string]any{"name": "run", "uid": uid,
		"labels": map[string]any{LabelBench: "1", LabelRole: RoleRun, LabelRun: run}}, "data": map[string]any{"run_id": run}}
}

// namespaceDeletes lists the deletes of whole namespaces.
func namespaceDeletes(f *fakeAPI) []string {
	var out []string
	for _, p := range f.deleted {
		if strings.HasPrefix(p, "/api/v1/namespaces/") && strings.Count(p, "/") == 4 {
			out = append(out, p)
		}
	}
	return out
}

func TestPoolLeaseSkipsBusyReplacesLeftoverAndNeverDeletesNamespaces(t *testing.T) {
	f := newFakeAPI(t)
	d := testDriver(t, f)
	d.cfg.Namespaces = []string{"pool-1", "pool-2", "pool-3"}
	busy := "/api/v1/namespaces/pool-1/configmaps/run"
	f.objs[busy] = anchorObj("r_other", "uid-other")
	// pool-2 holds a crashed earlier attempt of the same run.
	f.objs["/api/v1/namespaces/pool-2/configmaps/run"] = anchorObj("r_test1", "uid-old")
	f.objs["/api/v1/namespaces/pool-2/pods/run"] = map[string]any{"metadata": map[string]any{"name": "run",
		"labels": map[string]any{LabelBench: "1", LabelRun: "r_test1"}}, "spec": map[string]any{}}
	rc := testRun(t)
	ctx := t.Context()
	h, err := d.Create(ctx, rc)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rc.Handle = h
	a, ok := f.objs["/api/v1/namespaces/pool-2/configmaps/run"]
	if !ok || a["metadata"].(map[string]any)["uid"] != "uid-run" {
		t.Fatalf("leftover anchor not replaced: %v", a)
	}
	if _, ok := f.objs["/api/v1/namespaces/pool-2/pods/run"]; ok {
		t.Fatal("leftover run pod of the earlier attempt survived the lease")
	}
	if _, ok := f.objs["/apis/networking.k8s.io/v1/namespaces/pool-2/networkpolicies/deny-all"]; !ok {
		t.Fatal("no deny-all policy in the leased namespace")
	}
	if _, ok := f.objs["/api/v1/namespaces/pool-2"]; ok {
		t.Fatal("pool mode created a namespace")
	}
	r, err := d.ref(rc)
	if err != nil || r.ns != "pool-2" {
		t.Fatalf("ref: %+v %v", r, err)
	}
	d.leases = nil // a restarted process finds the lease by its anchor
	if r, err := d.ref(rc); err != nil || r.ns != "pool-2" {
		t.Fatalf("ref after restart: %+v %v", r, err)
	}
	if err := d.Destroy(ctx, rc); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if err := d.Destroy(ctx, rc); err != nil {
		t.Fatalf("destroy twice: %v", err)
	}
	if nd := namespaceDeletes(f); len(nd) > 0 {
		t.Fatalf("pool mode deleted namespaces: %v", nd)
	}
	for k := range f.objs {
		if strings.Contains(k, "/namespaces/pool-2/") {
			t.Fatalf("left in the released namespace: %s", k)
		}
	}
	if _, ok := f.objs[busy]; !ok {
		t.Fatal("another run's anchor was touched")
	}
	// The released namespace is free again for the next run.
	rc2 := testRun(t)
	rc2.Run.RunID = "r_test2"
	if _, err := d.Create(ctx, rc2); err != nil {
		t.Fatalf("second create: %v", err)
	}
	if r, _ := d.ref(rc2); r.ns != "pool-2" {
		t.Fatalf("second run leased %q, want the freed pool-2", r.ns)
	}
}

func TestPoolNoneFree(t *testing.T) {
	f := newFakeAPI(t)
	d := testDriver(t, f)
	d.cfg.Namespaces = []string{"pool-1", "pool-2"}
	f.objs["/api/v1/namespaces/pool-1/configmaps/run"] = anchorObj("r_a", "uid-a")
	f.objs["/api/v1/namespaces/pool-2/configmaps/run"] = anchorObj("r_b", "uid-b")
	_, err := d.Create(t.Context(), testRun(t))
	if err == nil || !strings.Contains(err.Error(), "no free namespace in kubernetes.namespaces (pool-1, pool-2)") {
		t.Fatalf("err = %v", err)
	}
	if _, err := d.ref(testRun(t)); err == nil {
		t.Fatal("a run without a lease must not resolve to a namespace")
	}
	if len(namespaceDeletes(f)) > 0 || len(f.deleted) > 0 {
		t.Fatalf("deleted: %v", f.deleted)
	}
}

func TestPoolEgressProbeAndPermissions(t *testing.T) {
	f := newFakeAPI(t)
	d := testDriver(t, f)
	d.cfg.Namespaces = []string{"pool-1"}
	rows := d.policyRows(t.Context(), true)
	if rows[0].Status != schemas.DoctorPass {
		t.Fatalf("pool probe: %+v", rows)
	}
	for k := range f.objs {
		if strings.Contains(k, "/namespaces/pool-1/") {
			t.Fatalf("probe left %s", k)
		}
		if strings.Contains(k, "check-") {
			t.Fatalf("probe made a namespace: %s", k)
		}
	}
	if nd := namespaceDeletes(f); len(nd) > 0 {
		t.Fatalf("probe deleted namespaces: %v", nd)
	}
	f.deny = map[string]bool{"create namespaces": true, "delete namespaces": true}
	if rows := d.permissionRows(t.Context()); rows[0].Status != schemas.DoctorPass {
		t.Fatalf("pool mode must not need namespace create/delete: %+v", rows)
	}
	f.deny = map[string]bool{"create pods/exec": true}
	if rows := d.permissionRows(t.Context()); rows[0].Status != schemas.DoctorFail || !strings.Contains(rows[0].Detail, "create pods/exec in pool-1") {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestPodRuntimeClass(t *testing.T) {
	in := testInput()
	if p := buildPod(in, nil); p.Spec.RuntimeClassName != "" {
		t.Fatalf("no runtime class configured: %q", p.Spec.RuntimeClassName)
	}
	in.RuntimeClass = "gvisor"
	if p := buildPod(in, nil); p.Spec.RuntimeClassName != "gvisor" {
		t.Fatalf("runtimeClassName = %q", p.Spec.RuntimeClassName)
	}
}

func TestCheckRuntimeClass(t *testing.T) {
	f := newFakeAPI(t)
	d := testDriver(t, f)
	if _, ok := d.runtimeClassRow(t.Context()); ok {
		t.Fatal("no row without runtime_class")
	}
	d.cfg.RuntimeClass = "gvisor"
	row, ok := d.runtimeClassRow(t.Context())
	if !ok || row.Status != schemas.DoctorFail || !strings.Contains(row.Detail, "no such RuntimeClass") {
		t.Fatalf("missing runtime class: %+v", row)
	}
	f.objs["/apis/node.k8s.io/v1/runtimeclasses/gvisor"] = map[string]any{"metadata": map[string]any{"name": "gvisor"}, "handler": "runsc"}
	if row, _ := d.runtimeClassRow(t.Context()); row.Status != schemas.DoctorPass || !strings.Contains(row.Detail, "runsc") {
		t.Fatalf("present runtime class: %+v", row)
	}
}

func TestClassifyPoolAnchors(t *testing.T) {
	mk := func(ns, run, install string) PoolAnchor {
		return PoolAnchor{Namespace: ns, Anchor: ConfigMap{Metadata: ObjectMeta{Labels: map[string]string{LabelBench: "1", LabelRole: RoleRun, LabelRun: run, LabelInstall: install}}}}
	}
	got := ClassifyPoolAnchors([]PoolAnchor{mk("p1", "r_dead", "me"), mk("p2", "r_live", "me"), mk("p3", "r_dead", "other")},
		CleanupInput{Install: "me", Live: map[string]bool{"r_live": true}, Dead: map[string]bool{"r_dead": true}})
	if len(got) != 2 || !got[0].Remove || got[1].Remove {
		t.Fatalf("verdicts: %+v", got)
	}
}
