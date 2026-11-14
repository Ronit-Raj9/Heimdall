package faultproxy

import (
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestValidatePlan(t *testing.T) {
	cases := []struct {
		name string
		plan schemas.FaultPlan
		ok   bool
	}{
		{"empty mode is none", schemas.FaultPlan{}, true},
		{"none", schemas.FaultPlan{Mode: ModeNone, RunID: "r"}, true},
		{"persistent", schemas.FaultPlan{Mode: ModePersistent, FromCall: 3, Status: 503, RetryAfterS: 2}, true},
		{"once", schemas.FaultPlan{Mode: ModeOnce, Call: 1, Status: 529}, true},
		{"persistent without from_call", schemas.FaultPlan{Mode: ModePersistent, Status: 503}, false},
		{"once without call", schemas.FaultPlan{Mode: ModeOnce, Status: 503}, false},
		{"status 200", schemas.FaultPlan{Mode: ModeOnce, Call: 1, Status: 200}, false},
		{"status 600", schemas.FaultPlan{Mode: ModeOnce, Call: 1, Status: 600}, false},
		{"negative retry", schemas.FaultPlan{Mode: ModeOnce, Call: 1, Status: 503, RetryAfterS: -1}, false},
		{"unknown mode", schemas.FaultPlan{Mode: "flaky", Status: 503}, false},
		{"scripted", schemas.FaultPlan{Mode: ModeScripted, RunID: "r"}, true},
		{"scripted with a status", schemas.FaultPlan{Mode: ModeScripted, Status: 503}, false},
		{"scripted with a call", schemas.FaultPlan{Mode: ModeScripted, Call: 1}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidatePlan(tc.plan); (err == nil) != tc.ok {
				t.Fatalf("err = %v, want ok %v", err, tc.ok)
			}
		})
	}
}

func TestPlanTableBegin(t *testing.T) {
	pt := newPlanTable()
	if err := pt.set("s", schemas.FaultPlan{Mode: ModePersistent, FromCall: 2, Status: 503, RunID: "r", Nonce: "n"}); err != nil {
		t.Fatal(err)
	}
	if d := pt.begin("s", KindModels); d.Call != 0 || d.Inject || d.RunID != "r" {
		t.Errorf("uncounted kind: %+v", d)
	}
	if d := pt.begin("s", KindMessage); d.Call != 1 || d.Inject {
		t.Errorf("call 1: %+v", d)
	}
	if d := pt.begin("s", KindMessage); d.Call != 2 || !d.Inject || d.Status != 503 || d.Nonce != "n" {
		t.Errorf("call 2: %+v", d)
	}
	if d := pt.begin("other", KindMessage); d.Call != 1 || d.Inject || d.RunID != "" {
		t.Errorf("slot without plan: %+v", d)
	}
	if p, ok := pt.get("s"); !ok || p.Mode != ModePersistent {
		t.Errorf("get = %+v, %v", p, ok)
	}
	pt.clear("s")
	if _, ok := pt.get("s"); ok {
		t.Error("plan still bound after clear")
	}
	if d := pt.begin("s", KindMessage); d.Call != 1 || d.Inject {
		t.Errorf("after clear: %+v", d)
	}
	if err := pt.set("s", schemas.FaultPlan{Mode: "bad"}); err == nil {
		t.Error("invalid plan accepted")
	}
}

func TestPlanTableToolTurn(t *testing.T) {
	pt := newPlanTable()
	if !pt.takeToolTurn("s") || pt.takeToolTurn("s") {
		t.Fatal("tool turn must be served exactly once per run")
	}
	if err := pt.set("s", schemas.FaultPlan{RunID: "r2"}); err != nil {
		t.Fatal(err)
	}
	if !pt.takeToolTurn("s") {
		t.Fatal("a new plan must reset the tool turn")
	}
}

func TestPlanTableConcurrentCallsAreDistinct(t *testing.T) {
	pt := newPlanTable()
	const n = 200
	seen := make([]bool, n+1)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d := pt.begin("s", KindMessage)
			mu.Lock()
			defer mu.Unlock()
			if d.Call < 1 || d.Call > n || seen[d.Call] {
				t.Errorf("duplicate or out of range call %d", d.Call)
				return
			}
			seen[d.Call] = true
		}()
	}
	wg.Wait()
}
