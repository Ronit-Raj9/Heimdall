package gates

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

func TestUpstreamErrors(t *testing.T) {
	const dns = "Post: dial tcp: lookup api.openai.com on 127.0.0.11:53: i/o timeout"
	line := func(call, status int, upErr string) schemas.LedgerEntry {
		return schemas.LedgerEntry{RunID: "r", Call: call, Kind: "message", Action: "pass", Upstream: "real", Status: status, UpstreamError: upErr}
	}
	many := make([]schemas.LedgerEntry, 0, maxUpstreamEvidence+3)
	for i := 1; i <= maxUpstreamEvidence+3; i++ {
		many = append(many, line(i, 502, dns))
	}
	cases := []struct {
		name     string
		ev       *schemas.Evidence
		fired    bool
		detail   string
		evidence []string
	}{
		{name: "no evidence", detail: "no fault proxy ledger"},
		{name: "no ledger", ev: &schemas.Evidence{}, detail: "no fault proxy ledger"},
		{name: "all answered", ev: &schemas.Evidence{Ledger: []schemas.LedgerEntry{line(1, 200, ""), line(2, 200, "")}}},
		{name: "provider unreachable",
			ev:       &schemas.Evidence{Ledger: []schemas.LedgerEntry{line(1, 200, ""), line(2, 502, dns), line(3, 502, dns)}},
			fired:    true,
			detail:   "2 model calls with no planned fault got no response from the provider; 1 answered; last call failed: true; first error: " + dns,
			evidence: []string{"ledger call 2", "ledger call 3"}},
		{name: "older proxy, inferred from the gateway",
			ev: &schemas.Evidence{
				Ledger: []schemas.LedgerEntry{line(1, 502, "")},
				Requests: &schemas.RequestLog{Requests: []schemas.GatewayRequest{
					{Kind: "message", Status: "error", ErrorStatus: 502, ErrorMessage: utils.ProxyUnreachable}}},
			},
			fired:    true,
			detail:   "inferred from the gateway's",
			evidence: []string{"ledger call 1"}},
		{name: "evidence is capped", ev: &schemas.Evidence{Ledger: many}, fired: true, detail: "23 model calls"},
	}
	g := newUpstreamGate()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := g.Check(context.Background(), nil, c.ev)
			if err != nil {
				t.Fatal(err)
			}
			if res.Gate != UpstreamErrors || res.Effect != schemas.GateReport || res.Fired != c.fired || !strings.Contains(res.Detail, c.detail) {
				t.Fatalf("result %+v, want fired %v detail containing %q", res, c.fired, c.detail)
			}
			if c.evidence != nil && !reflect.DeepEqual(res.Evidence, c.evidence) {
				t.Fatalf("evidence %v, want %v", res.Evidence, c.evidence)
			}
			if c.name == "evidence is capped" && (len(res.Evidence) != maxUpstreamEvidence+1 || res.Evidence[maxUpstreamEvidence] != "and 3 more") {
				t.Fatalf("evidence %v", res.Evidence)
			}
		})
	}
}
