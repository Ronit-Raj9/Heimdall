package handlers

import (
	"encoding/json"
	"testing"

	"github.com/valyala/fasthttp"
)

// Lane F1: the Check fix form's own body (exp_id, arm, trials, budget_usd,
// yes) caps the rerun; a cap out of range is refused before anything starts.
func TestRerunTakesTheFormsCap(t *testing.T) {
	r, _, l, id := w443Setup(t, w443NewDoc)
	body, _ := json.Marshal(map[string]any{"exp_id": testExp, "arm": "A", "trials": 1, "budget_usd": 0.4, "yes": true})
	code, out := doAgentOps(r, "POST", APIPrefix+"/findings/"+id+"/rerun", "application/json", body)
	if code != fasthttp.StatusAccepted {
		t.Fatalf("status %d (%v)", code, out)
	}
	if l.planned == nil || l.planned.Budget.TotalUSD != 0.4 || l.planned.Budget.ReserveUSD > 0.4 {
		t.Fatalf("planned budget %+v", l.planned)
	}
	r, _, l, id = w443Setup(t, w443NewDoc)
	body, _ = json.Marshal(map[string]any{"exp_id": testExp, "arm": "A", "trials": 1, "budget_usd": 500, "yes": true})
	code, out = doAgentOps(r, "POST", APIPrefix+"/findings/"+id+"/rerun", "application/json", body)
	if code != fasthttp.StatusBadRequest || l.planned != nil {
		t.Fatalf("cap 500: status %d (%v)", code, out)
	}
}
