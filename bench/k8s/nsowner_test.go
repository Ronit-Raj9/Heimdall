package k8s

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// 2-D-15: a namespace that exists under the run's name but belongs to
// another run is never deleted.
func TestCreateRefusesForeignNamespace(t *testing.T) {
	f := newFakeAPI(t)
	f.objs["/api/v1/namespaces/heimdall-run-r-test1"] = map[string]any{
		"metadata": map[string]any{"name": "heimdall-run-r-test1", "labels": map[string]any{LabelBench: "1", LabelRole: RoleRun, LabelRun: "r_other"}}}
	d := testDriver(t, f)
	_, err := d.Create(t.Context(), testRun(t))
	if err == nil || !strings.Contains(err.Error(), "does not belong to run r_test1") {
		t.Fatalf("err = %v", err)
	}
	for _, p := range f.deleted {
		if strings.Contains(p, "heimdall-run-r-test1") {
			t.Fatalf("deleted %s", p)
		}
	}
}

// QC2-10-01: the pipeline's cleanup after a refused Create (Destroy on the
// same run context) leaves the foreign namespace alone too, while the run's
// own namespace is still removed.
func TestDestroyLeavesForeignNamespace(t *testing.T) {
	f := newFakeAPI(t)
	f.objs["/api/v1/namespaces/heimdall-run-r-test1"] = map[string]any{
		"metadata": map[string]any{"name": "heimdall-run-r-test1", "uid": "foreign", "labels": map[string]any{LabelBench: "1", LabelRole: RoleRun, LabelRun: "r_other"}}}
	d := testDriver(t, f)
	rc := testRun(t)
	if _, err := d.Create(t.Context(), rc); err == nil {
		t.Fatal("create accepted a foreign namespace")
	}
	if err := d.Destroy(t.Context(), rc); err != nil {
		t.Fatal(err)
	}
	for _, p := range f.deleted {
		if strings.Contains(p, "heimdall-run-r-test1") {
			t.Fatalf("deleted %s", p)
		}
	}
	if _, ok := f.objs["/api/v1/namespaces/heimdall-run-r-test1"]; !ok {
		t.Fatal("foreign namespace gone")
	}

	g := newFakeAPI(t)
	d = testDriver(t, g)
	rc = testRun(t)
	if _, err := d.Create(t.Context(), rc); err != nil {
		t.Fatal(err)
	}
	if err := d.Destroy(t.Context(), rc); err != nil {
		t.Fatal(err)
	}
	if _, ok := g.objs["/api/v1/namespaces/heimdall-run-r-test1"]; ok {
		t.Fatal("own namespace not deleted")
	}
}

// 2-D-05: a configured service account is created, tokenless, in the run
// namespace before the pod names it.
func TestCreateMakesServiceAccount(t *testing.T) {
	f := newFakeAPI(t)
	d := testDriver(t, f)
	d.cfg.ServiceAccount = "bench-runner"
	if _, err := d.Create(t.Context(), testRun(t)); err != nil {
		t.Fatal(err)
	}
	sa, ok := f.objs["/api/v1/namespaces/heimdall-run-r-test1/serviceaccounts/bench-runner"]
	if !ok {
		t.Fatal("no service account in the run namespace")
	}
	if sa["automountServiceAccountToken"] != false {
		t.Fatalf("token automount not disabled: %v", sa)
	}
}

// QC2-10-11: naming the namespace's default account creates nothing (the
// API server makes it, and a POST would race it), and an account that
// admission policy already put in the namespace is used as it is.
func TestServiceAccountDefaultAndExisting(t *testing.T) {
	f := newFakeAPI(t)
	d := testDriver(t, f)
	d.cfg.ServiceAccount = "default"
	f.objs["/api/v1/namespaces/heimdall-run-r-test1/serviceaccounts/default"] = map[string]any{"metadata": map[string]any{"name": "default"}}
	if err := d.createServiceAccount(t.Context(), "heimdall-run-r-test1", nil, nil); err != nil {
		t.Fatalf("default account: %v", err)
	}
	d.cfg.ServiceAccount = "bench-runner"
	f.objs["/api/v1/namespaces/heimdall-run-r-test1/serviceaccounts/bench-runner"] = map[string]any{"metadata": map[string]any{"name": "bench-runner"}}
	if err := d.createServiceAccount(t.Context(), "heimdall-run-r-test1", nil, nil); err != nil {
		t.Fatalf("existing account: %v", err)
	}
}

// QC2-10-03: the doctor checks pod-log reads, which the egress probe needs.
func TestDoctorChecksPodLog(t *testing.T) {
	f := newFakeAPI(t)
	f.deny = map[string]bool{"get pods/log": true}
	d := testDriver(t, f)
	rows := d.permissionRows(t.Context())
	if rows[0].Status != schemas.DoctorFail || !strings.Contains(rows[0].Detail, "get pods/log") {
		t.Fatalf("rows = %+v", rows)
	}
}
