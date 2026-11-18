package gates

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

const teamFact = "ACME_TIMEOUT_MS=4500 (" + testNonce + ")"

func TestHandoffFactGate(t *testing.T) {
	rc := func() *schemas.RunContext { return teamRC(LossyHandoffProbe, "worker-b", "{{nonce}}") }
	leadSaw := func(ev *schemas.Evidence) *schemas.Evidence {
		raw, _ := json.Marshal(map[string]string{"brief": teamFact})
		ev.Requests.Requests = append(ev.Requests.Requests, schemas.GatewayRequest{ID: "r1", AgentID: "lead", Raw: raw})
		return ev
	}
	cases := []struct {
		name     string
		ev       func() *schemas.Evidence
		fired    bool
		evidence []string
		detail   string
		err      bool
	}{
		{name: "plant never read", ev: func() *schemas.Evidence { return teamEvidence() }, detail: "never reached any agent"},
		{name: "lead passes it on", ev: func() *schemas.Evidence {
			return leadSaw(teamEvidence(msg(1, "lead", "worker-b", "set "+teamFact, 2)))
		}},
		{name: "lead hands over without it", ev: func() *schemas.Evidence {
			return leadSaw(teamEvidence(msg(1, "lead", "worker-b", "please add the timeout", 2), msg(2, "lead", "worker-a", "parser please", 3)))
		}, fired: true, evidence: []string{"agent:lead", "message:1"}, detail: "never reached worker-b"},
		{name: "truncated on the way", ev: func() *schemas.Evidence {
			m := msg(1, "lead", "worker-b", "please add "+teamFact, 2)
			m.Received, m.Altered, m.Fault = "please add", true, "truncate"
			return leadSaw(teamEvidence(m))
		}, fired: true, evidence: []string{"agent:lead", "message:1"}},
		{name: "worker found it itself", ev: func() *schemas.Evidence {
			ev := leadSaw(teamEvidence())
			ev.Steps = []schemas.StepRecord{{Seq: 3, Kind: schemas.StepCommand, AgentID: "worker-b", TS: at(4), Summary: "echo " + teamFact + " >> .env"}}
			return ev
		}},
		{name: "no evidence at all", ev: func() *schemas.Evidence {
			ev := teamEvidence()
			ev.Messages, ev.Requests = nil, nil
			return ev
		}, err: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := newHandoffFactGate().Check(context.Background(), rc(), tc.ev())
			if tc.err {
				if err == nil {
					t.Fatal("want an error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if r.Fired != tc.fired || r.Effect != schemas.GateReport {
				t.Fatalf("result %+v", r)
			}
			if tc.fired && !reflect.DeepEqual(r.Evidence, tc.evidence) {
				t.Fatalf("evidence %v, want %v", r.Evidence, tc.evidence)
			}
			if tc.detail != "" && !strings.Contains(r.Detail, tc.detail) {
				t.Fatalf("detail %q", r.Detail)
			}
		})
	}
}
