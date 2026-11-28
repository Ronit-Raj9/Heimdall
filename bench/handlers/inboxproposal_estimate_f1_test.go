package handlers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/evidence"
	"github.com/maximhq/bifrost/bench/schemas"
)

// PT-8: the draft confirm names an estimate. The page asks this route with
// the picked model before the paid start.
func TestInboxProposalEstimateRoute(t *testing.T) {
	SetPriceList(func(model string) (evidence.ModelPrice, bool) {
		if model == "gpt-test" {
			return evidence.ModelPrice{InputPerMTok: 1, OutputPerMTok: 4}, true
		}
		return evidence.ModelPrice{}, false
	})
	t.Cleanup(func() { SetPriceList(nil) })
	svc := newInboxService(t, false)
	c := newW445Client(t, svc)
	resp := c.do(t, "POST", InboxImportSessionPath, w445Session(), authed("application/octet-stream"))
	var res schemas.InboxIngestResult
	if err := json.Unmarshal(resp.Body(), &res); err != nil || res.ItemID == "" {
		t.Fatalf("import = %d %s", resp.StatusCode(), resp.Body())
	}
	path := strings.Replace(InboxProposalEstimatePath, "{id}", res.ItemID, 1)
	var est ProposalEstimate
	resp = c.do(t, "GET", path+"?model=gpt-test&kind=qa", nil, nil)
	if resp.StatusCode() != 200 || json.Unmarshal(resp.Body(), &est) != nil {
		t.Fatalf("estimate = %d %s", resp.StatusCode(), resp.Body())
	}
	if !est.Priced || est.InputTokens <= 0 || est.OutputTokens != 1500 || !(est.USD > 0) || !(est.MaxUSD > est.USD) {
		t.Fatalf("estimate %+v", est)
	}
	resp = c.do(t, "GET", path+"?model=unknown-model", nil, nil)
	est = ProposalEstimate{}
	if resp.StatusCode() != 200 || json.Unmarshal(resp.Body(), &est) != nil || est.Priced || est.USD != 0 {
		t.Fatalf("unpriced estimate = %d %s", resp.StatusCode(), resp.Body())
	}
	if resp := c.do(t, "GET", strings.Replace(InboxProposalEstimatePath, "{id}", "nope", 1), nil, nil); resp.StatusCode() != 404 {
		t.Fatalf("unknown item = %d %s", resp.StatusCode(), resp.Body())
	}
}
