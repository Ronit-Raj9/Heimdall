package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/jobs"
)

// K5 (PQ2-15): the Fault test form's body without a spend cap is refused with
// a plain sentence before any run; a cap out of range is refused; with a cap
// validation goes on to the next check.
func TestProbeJobWebBodyNeedsCap(t *testing.T) {
	for _, raw := range []string{
		`{"exp_id":"e_parent","trials":1,"faults":["model-brownout"]}`,
		`{"exp_id":"e_parent","trials":1,"faults":["model-brownout"],"budget_usd":0}`,
		`{"exp_id":"e_parent","trials":1,"faults":["model-brownout"],"budget_usd":500}`,
	} {
		_, _, err := validateProbeJob(context.Background(), jobEnv{}, json.RawMessage(raw))
		if !errors.Is(err, jobs.ErrInvalidParams) || !strings.Contains(err.Error(), "spend cap") {
			t.Fatalf("%s: want a spend cap refusal, got %v", raw, err)
		}
	}
	// A capped body passes the cap check: the next check (an unknown fault) answers instead.
	raw := `{"exp_id":"e_parent","trials":1,"faults":["no-such-fault"],"budget_usd":0.5}`
	if _, _, err := validateProbeJob(context.Background(), jobEnv{}, json.RawMessage(raw)); err == nil || strings.Contains(err.Error(), "spend cap") {
		t.Fatalf("capped body refused for its cap: %v", err)
	}
}
