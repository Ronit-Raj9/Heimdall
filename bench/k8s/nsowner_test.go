package k8s

import (
	"strings"
	"testing"
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
