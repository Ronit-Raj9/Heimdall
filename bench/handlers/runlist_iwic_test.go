package handlers

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// TestListRunsFailedAcceptsTrue checks that failed=true filters like failed=1
// (the runs page sends true), that 0 and false do not filter, and that any
// other value is a plain 400.
func TestListRunsFailedAcceptsTrue(t *testing.T) {
	idx := runListIndex()
	for i := range idx.runs {
		if idx.runs[i].Run.RunID == "r_t02_B" {
			idx.runs[i].Run.Outcome = schemas.OutcomeInfraError
		}
	}
	c := runListClient(t, idx, runListSuites())
	for _, q := range []string{"?failed=1", "?failed=true"} {
		if out := getRunList(t, c, q); out.Total != 2 {
			t.Errorf("%s total %d, want 2", q, out.Total)
		}
	}
	for _, q := range []string{"?failed=0", "?failed=false", "?failed="} {
		if out := getRunList(t, c, q); out.Total != 4 {
			t.Errorf("%s total %d, want 4", q, out.Total)
		}
	}
	for _, q := range []string{"?failed=yes", "?failed=2", "?failed=TRUEISH"} {
		resp := c.do(t, "GET", APIPrefix+"/runs"+q, nil, nil)
		if resp.StatusCode() != fasthttp.StatusBadRequest {
			t.Errorf("%s = %d, want 400", q, resp.StatusCode())
			continue
		}
		if body := string(resp.Body()); !strings.Contains(body, "failed must be 1 or true") {
			t.Errorf("%s body %s", q, body)
		}
	}
}
