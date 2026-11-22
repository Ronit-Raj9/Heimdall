package engine

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestGatewayOutcome(t *testing.T) {
	keyed := func() *schemas.RunContext {
		return &schemas.RunContext{Run: &schemas.Run{Gateway: schemas.RunGatewayInfo{VirtualKeyID: "vk-1", SessionID: "r_1"}}}
	}
	empty := &schemas.Evidence{Requests: &schemas.RequestLog{Complete: true}}
	oneCall := &schemas.Evidence{Requests: &schemas.RequestLog{Complete: true, Requests: []schemas.GatewayRequest{{ID: "a", Kind: "message"}}}}
	incomplete := &schemas.Evidence{Requests: &schemas.RequestLog{Complete: false}}
	ledger := &schemas.Evidence{Requests: &schemas.RequestLog{Complete: true}, Ledger: []schemas.LedgerEntry{{}}}
	for name, c := range map[string]struct {
		rc   *schemas.RunContext
		ev   *schemas.Evidence
		in   schemas.Outcome
		want schemas.Outcome
		pf   bool
	}{
		"timeout with no call":        {keyed(), empty, schemas.OutcomeTimeout, schemas.OutcomeInfraError, true},
		"timeout with a call":         {keyed(), oneCall, schemas.OutcomeTimeout, schemas.OutcomeTimeout, false},
		"gateway log incomplete":      {keyed(), incomplete, schemas.OutcomeTimeout, schemas.OutcomeTimeout, false},
		"no gateway log":              {keyed(), &schemas.Evidence{}, schemas.OutcomeTimeout, schemas.OutcomeTimeout, false},
		"fault proxy saw a call":      {keyed(), ledger, schemas.OutcomeTimeout, schemas.OutcomeTimeout, false},
		"crash stays":                 {keyed(), empty, schemas.OutcomeCrash, schemas.OutcomeCrash, false},
		"fail stays":                  {keyed(), empty, schemas.OutcomeFail, schemas.OutcomeFail, false},
		"no virtual key (no gateway)": {&schemas.RunContext{Run: &schemas.Run{}}, empty, schemas.OutcomeTimeout, schemas.OutcomeTimeout, false},
	} {
		t.Run(name, func(t *testing.T) {
			got, pf := gatewayOutcome(c.rc, c.ev, c.in)
			if got != c.want || (pf != nil) != c.pf {
				t.Fatalf("gatewayOutcome = %s, failure %v; want %s, failure %v", got, pf != nil, c.want, c.pf)
			}
			if pf != nil && (pf.Part != gatewayPart || !strings.Contains(pf.Error, "excluded from scores")) {
				t.Fatalf("failure = %+v", pf)
			}
		})
	}
}
