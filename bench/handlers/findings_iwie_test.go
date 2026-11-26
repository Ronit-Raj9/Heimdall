package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/findings"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

// Group E security review of findings (W4-43): a targeted rerun reads the
// experiment only from the caller's organization's own index. An experiment
// id the index does not hold (another organization's, say) is 404, and
// nothing is planned or written.
func TestFindingsRerunOnlyOwnOrgExperiment(t *testing.T) {
	r, root, l, id := w443Setup(t, w443NewDoc)
	body, _ := json.Marshal(map[string]any{"exp_id": "e_other_org", "arm": "A", "trials": 3, "yes": true})
	code, got := doAgentOps(r, "POST", APIPrefix+"/findings/"+id+"/rerun", "application/json", body)
	if code != fasthttp.StatusNotFound {
		t.Fatalf("status %d (%v), want 404", code, got)
	}
	if l.planned != nil || l.created != nil || l.started != "" {
		t.Fatalf("a rerun was planned for an experiment outside the org: %+v", l.planned)
	}
	if _, err := os.Stat(filepath.Join(root, findings.Dir, "reruns")); !os.IsNotExist(err) {
		t.Fatalf("an old arm file was written: %v", err)
	}
	f, _ := findings.Open(root).Get(id)
	if len(f.Reruns) != 0 {
		t.Fatalf("reruns %v", f.Reruns)
	}
}

// A rerun spends money: without yes nothing is planned.
func TestFindingsRerunNeedsConfirm(t *testing.T) {
	r, _, l, id := w443Setup(t, w443NewDoc)
	body, _ := json.Marshal(map[string]any{"exp_id": testExp, "arm": "A", "trials": 3})
	code, got := doAgentOps(r, "POST", APIPrefix+"/findings/"+id+"/rerun", "application/json", body)
	if code != fasthttp.StatusBadRequest {
		t.Fatalf("status %d (%v), want 400", code, got)
	}
	if l.planned != nil {
		t.Fatal("a rerun was planned without confirmation")
	}
}

// A rerun needs the same role as starting an evaluation or an experiment,
// or rerunning failures (the budget checks are the launcher's Plan and
// Start, which all of them share).
func TestFindingsRerunRoleMatchesEvaluationStart(t *testing.T) {
	rerun, ok := tenancy.OrgPolicy["POST "+APIPrefix+"/findings/{id}/rerun"]
	if !ok {
		t.Fatal("no policy for the findings rerun")
	}
	for _, k := range []string{"POST " + APIPrefix + "/evaluations", "POST " + APIPrefix + "/experiments", "POST " + APIPrefix + "/experiments/{id}/rerun-failures"} {
		p, ok := tenancy.OrgPolicy[k]
		if !ok {
			t.Fatalf("no policy for %s", k)
		}
		if rerun.Access != p.Access || rerun.Min != p.Min {
			t.Errorf("findings rerun %+v, %s %+v: want the same role", rerun, k, p)
		}
	}
}
