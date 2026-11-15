package metrics

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestReportedMetrics(t *testing.T) {
	ctx := context.Background()
	local := &schemas.RunContext{Agent: &schemas.AgentConfig{}}
	remote := &schemas.RunContext{Agent: &schemas.AgentConfig{Remote: &schemas.RemoteAgent{URL: "https://a"}}}
	in, out, cost := int64(100), int64(20), 0.25
	full := &schemas.Evidence{Remote: &schemas.RemoteEvidence{Reported: &schemas.RemoteReport{InputTokens: &in, OutputTokens: &out, CostUSD: &cost}}}
	empty := &schemas.Evidence{Remote: &schemas.RemoteEvidence{Reported: &schemas.RemoteReport{}}}
	tok, usd := newReportedTokensMetric(), newReportedCostMetric()

	for _, m := range []schemas.Metric{tok, usd} {
		if v, _ := m.Compute(ctx, local, full); v.State != schemas.ValueNotApplicable {
			t.Errorf("%s on a local agent = %+v, want not applicable", m.GetName(), v)
		}
		if v, _ := m.Compute(ctx, remote, &schemas.Evidence{}); v.State != schemas.ValueUnknown {
			t.Errorf("%s without a report = %+v, want unknown", m.GetName(), v)
		}
		if v, _ := m.Compute(ctx, remote, empty); v.State != schemas.ValueUnknown {
			t.Errorf("%s with an empty report = %+v, want unknown", m.GetName(), v)
		}
	}
	if v, _ := tok.Compute(ctx, remote, full); !v.IsObserved() || *v.V != 120 || v.Evidence != schemas.EvidenceNative || v.Source != reportedSource {
		t.Fatalf("reported_tokens = %+v", v)
	}
	total := int64(200)
	full.Remote.Reported.TotalTokens = &total
	if v, _ := tok.Compute(ctx, remote, full); *v.V != 200 {
		t.Fatalf("reported_tokens with total = %+v", v)
	}
	if v, _ := usd.Compute(ctx, remote, full); !v.IsObserved() || *v.V != 0.25 {
		t.Fatalf("reported_cost_usd = %+v", v)
	}
	// The trusted metrics never read the self-report.
	if v, _ := newTokensMetric().Compute(ctx, remote, full); v.IsObserved() {
		t.Fatalf("tokens read the self-report: %+v", v)
	}
	if v, _ := newCostUSDMetric().Compute(ctx, remote, full); v.IsObserved() {
		t.Fatalf("cost_usd read the self-report: %+v", v)
	}
}
