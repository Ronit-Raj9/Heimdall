package faultproxy

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestValidatePlanWindows(t *testing.T) {
	cases := []struct {
		name string
		plan schemas.FaultPlan
		ok   bool
	}{
		{"brownout by calls", schemas.FaultPlan{Mode: ModePersistent, FromCall: 3, Calls: 6, Status: 503}, true},
		{"brownout by time", schemas.FaultPlan{Mode: ModePersistent, FromCall: 2, UntilS: 30, Status: 503}, true},
		{"negative calls", schemas.FaultPlan{Mode: ModePersistent, FromCall: 1, Calls: -1, Status: 503}, false},
		{"calls on once", schemas.FaultPlan{Mode: ModeOnce, Call: 1, Calls: 2, Status: 503}, false},
		{"slow", schemas.FaultPlan{Mode: ModeSlow, FirstByteDelayMS: 8000}, true},
		{"slow without delay", schemas.FaultPlan{Mode: ModeSlow}, false},
		{"slow with status", schemas.FaultPlan{Mode: ModeSlow, DelayMS: 10, Status: 503}, false},
		{"slow delay over cap", schemas.FaultPlan{Mode: ModeSlow, DelayMS: maxDelayMS + 1}, false},
		{"delay on persistent", schemas.FaultPlan{Mode: ModePersistent, FromCall: 1, Status: 503, DelayMS: 10}, false},
		{"flaky", schemas.FaultPlan{Mode: ModeFlaky, FailProb: 0.3, Status: 529}, true},
		{"flaky without prob", schemas.FaultPlan{Mode: ModeFlaky, Status: 529}, false},
		{"flaky without status", schemas.FaultPlan{Mode: ModeFlaky, FailProb: 0.3}, false},
		{"fail_prob on persistent", schemas.FaultPlan{Mode: ModePersistent, FromCall: 1, Status: 503, FailProb: 0.5}, false},
		{"flaky for one model", schemas.FaultPlan{Mode: ModeFlaky, FailProb: 0.5, Status: 503, Model: "m"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidatePlan(tc.plan); (err == nil) != tc.ok {
				t.Fatalf("err = %v, want ok %v", err, tc.ok)
			}
		})
	}
}

func injects(pt *planTable, slot string, n int) []bool {
	out := make([]bool, n)
	for i := range out {
		out[i] = pt.begin(slot, KindMessage).Inject
	}
	return out
}

func TestBrownoutEndsAfterCalls(t *testing.T) {
	pt := newPlanTable()
	if err := pt.set("s", schemas.FaultPlan{Mode: ModePersistent, FromCall: 3, Calls: 2, Status: 503}); err != nil {
		t.Fatal(err)
	}
	got := injects(pt, "s", 6)
	want := []bool{false, false, true, true, false, false}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("injects = %v, want %v", got, want)
		}
	}
}

func TestBrownoutEndsAfterTime(t *testing.T) {
	pt := newPlanTable()
	now := time.Unix(100, 0)
	pt.now = func() time.Time { return now }
	if err := pt.set("s", schemas.FaultPlan{Mode: ModePersistent, FromCall: 1, UntilS: 10, Status: 503}); err != nil {
		t.Fatal(err)
	}
	if !pt.begin("s", KindMessage).Inject {
		t.Fatal("first call not failed")
	}
	now = now.Add(9 * time.Second)
	if !pt.begin("s", KindMessage).Inject {
		t.Fatal("call inside the window passed")
	}
	now = now.Add(time.Second)
	if pt.begin("s", KindMessage).Inject {
		t.Fatal("call after the window failed")
	}
}

func TestWindowStartSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plans.json")
	pt, err := loadPlanTable(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0).UTC()
	pt.now = func() time.Time { return now }
	if err := pt.set("s", schemas.FaultPlan{Mode: ModePersistent, FromCall: 1, UntilS: 10, Status: 503}); err != nil {
		t.Fatal(err)
	}
	pt.begin("s", KindMessage)
	again, err := loadPlanTable(path)
	if err != nil {
		t.Fatal(err)
	}
	again.now = func() time.Time { return now.Add(11 * time.Second) }
	if again.begin("s", KindMessage).Inject {
		t.Fatal("restart forgot the window start and failed a call after it ended")
	}
}

func TestFlakySeededPlan(t *testing.T) {
	run := func() []bool {
		pt := newPlanTable()
		if err := pt.set("s", schemas.FaultPlan{Mode: ModeFlaky, FailProb: 0.5, Status: 529, Nonce: "abc", Calls: 40}); err != nil {
			t.Fatal(err)
		}
		return injects(pt, "s", 50)
	}
	a, b := run(), run()
	fails := 0
	for i := range a {
		if a[i] != b[i] {
			t.Fatal("same nonce, different coins")
		}
		if a[i] {
			fails++
			if i >= 40 {
				t.Fatalf("call %d failed after the window", i+1)
			}
		}
	}
	if fails < 8 || fails > 32 {
		t.Fatalf("%d of 40 failed at 0.5", fails)
	}
}

func TestSlowPlanHoldsAndAnswers(t *testing.T) {
	env := newTestEnv(t, UpstreamScripted, "")
	var mu sync.Mutex
	var holds []time.Duration
	env.srv.sleep = func(_ context.Context, d time.Duration) error {
		mu.Lock()
		holds = append(holds, d)
		mu.Unlock()
		return nil
	}
	env.setPlan(t, "slot-a", schemas.FaultPlan{Mode: ModeSlow, FromCall: 1, Calls: 1, DelayMS: 300, FirstByteDelayMS: 700, RunID: "r"})
	resp, _ := env.call(t, "POST", "/v1/messages", "auth-a", msgBody)
	if resp.StatusCode != 200 {
		t.Fatalf("slow call status %d", resp.StatusCode)
	}
	resp, _ = env.call(t, "POST", "/v1/messages", "auth-a", msgBody)
	if resp.StatusCode != 200 {
		t.Fatalf("second call status %d", resp.StatusCode)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(holds) != 2 || holds[0] != 300*time.Millisecond || holds[1] != 700*time.Millisecond {
		t.Fatalf("holds %v", holds)
	}
	es := env.entries(t)
	if len(es) != 2 || es[0].DelayMS != 1000 || es[0].Action != ActionPass || es[1].DelayMS != 0 {
		t.Fatalf("ledger %+v", es)
	}
}
