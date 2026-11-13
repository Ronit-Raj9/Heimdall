package handlers

import (
	"testing"
)

func TestGateSummaries(t *testing.T) {
	idx := testIndex()
	idx.files["r_t02_B"][FileGates] = []byte(`[{"gate":"protected_files","version":"1","effect":"veto","fired":true,"detail":"edited tests"}]`)
	delete(idx.files["r_t02_A"], FileGates)
	got, err := GateSummaries(idx, idx.Runs(testExp), fakeCatalog{}.Catalog())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("summaries = %+v", got)
	}
	g := got[0]
	if g.Gate != "protected_files" || !g.Baseline || g.Fired.Num != 1 || g.Fired.Den != 3 || g.Fired.Unknown != 1 {
		t.Fatalf("summary = %+v", g)
	}
	if g.Detail != "first fired on run r_t02_B: edited tests" {
		t.Errorf("detail = %q", g.Detail)
	}
	idx.files["r_t01_A"][FileGates] = []byte("{")
	if _, err := GateSummaries(idx, idx.Runs(testExp), nil); err == nil {
		t.Error("malformed gates.json must fail")
	}
}
