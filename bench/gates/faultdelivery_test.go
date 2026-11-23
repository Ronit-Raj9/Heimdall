package gates

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// faultRun is a run of a gateway fault condition on the given fault path.
func faultRun(path string, layer schemas.ProbeLayerName) *schemas.RunContext {
	run := &schemas.Run{RunID: "r_abc"}
	run.Gateway.FaultPath = path
	return &schemas.RunContext{Run: run, Probe: &schemas.ProbeSpec{ID: "model-down", Layer: layer}}
}

func deliveryRows(planIDs ...string) *schemas.Evidence {
	var rows []schemas.GatewayRequest
	for i, id := range planIDs {
		rows = append(rows, schemas.GatewayRequest{ID: string(rune('a' + i)), Kind: "message", ModelRequested: "gpt-6-luna", FaultPlanID: id})
	}
	return &schemas.Evidence{Requests: &schemas.RequestLog{Requests: rows, Complete: true}}
}

func checkDelivery(t *testing.T, rc *schemas.RunContext, ev *schemas.Evidence) *schemas.GateResult {
	t.Helper()
	res, err := newFaultDeliveryGate().Check(context.Background(), rc, ev)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestFaultDeliveryFiresWhenPlanMissing(t *testing.T) {
	g := newFaultDeliveryGate()
	if g.GetName() != FaultDelivery || g.Effect() != schemas.GateInvalidate || g.GetVersion() != "1.0.0" || !g.Baseline() {
		t.Fatalf("gate identity %s %s %s baseline=%v", g.GetName(), g.Effect(), g.GetVersion(), g.Baseline())
	}
	res := checkDelivery(t, faultRun("plugin", schemas.LayerGateway), deliveryRows("", ""))
	if !res.Fired || res.Detail != "the fault plan never reached the gateway: the agent did not send the heimdall headers" {
		t.Fatalf("fired=%v detail=%q", res.Fired, res.Detail)
	}
	// Another run's plan id does not count as this run's.
	if res := checkDelivery(t, faultRun("plugin", schemas.LayerGateway), deliveryRows("r_other:persistent")); !res.Fired {
		t.Fatal("a row with another run's plan id kept the run")
	}
	// One row with the run's plan id is enough.
	if res := checkDelivery(t, faultRun("plugin", schemas.LayerGateway), deliveryRows("", "r_abc:persistent")); res.Fired {
		t.Fatalf("plan reached the gateway but the gate fired: %q", res.Detail)
	}
	// A compound schedule arms its gateway step at start too.
	if res := checkDelivery(t, faultRun("plugin", schemas.LayerCompound), deliveryRows("")); !res.Fired {
		t.Fatal("compound fault run without the plan id was not thrown out")
	}
}

func TestFaultDeliveryIgnoresProxyPath(t *testing.T) {
	for _, path := range []string{"proxy", "", "mixed"} {
		if res := checkDelivery(t, faultRun(path, schemas.LayerGateway), deliveryRows("")); res.Fired {
			t.Fatalf("path %q fired: %q", path, res.Detail)
		}
	}
	// A clean run (no probe) or a probe that is not a model fault loses nothing.
	clean := faultRun("plugin", schemas.LayerGateway)
	clean.Probe = nil
	if res := checkDelivery(t, clean, deliveryRows("")); res.Fired {
		t.Fatal("clean run fired")
	}
	if res := checkDelivery(t, faultRun("plugin", schemas.LayerFixture), deliveryRows("")); res.Fired {
		t.Fatal("fixture probe fired")
	}
}

func TestFaultDeliveryNoCallsNotFired(t *testing.T) {
	if res := checkDelivery(t, faultRun("plugin", schemas.LayerGateway), deliveryRows()); res.Fired {
		t.Fatal("a run with no model call fired")
	}
	if res := checkDelivery(t, faultRun("plugin", schemas.LayerGateway), &schemas.Evidence{}); res.Fired {
		t.Fatal("a run with no gateway log fired")
	}
	if res := checkDelivery(t, nil, deliveryRows("")); res.Fired {
		t.Fatal("no run context fired")
	}
}
