package schemas

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestWorkerAPIJSON(t *testing.T) {
	cost := Unknown("not observed", EvidenceGateway)
	for _, v := range []any{
		WorkerLease{LeaseID: "l_1", Slot: Slot{SlotID: "s_1"}, HeartbeatEveryMS: 15000, StaleAfterMS: 120000},
		WorkerComplete{WorkerID: "w1", RunID: "r_1", SealSHA256: "a", ArchiveSHA256: "b", ArchiveBytes: 3, CostUSD: cost},
		WorkerRelease{WorkerID: "w1", Reason: "x", Refuse: true, CostUSD: &cost},
		WorkerRegister{WorkerID: "w1", Parts: map[string]string{"store/fs": "1.0.0"}, Slots: 2},
	} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var keys map[string]any
		if err := json.Unmarshal(b, &keys); err != nil {
			t.Fatal(err)
		}
		for k := range keys {
			if k != strings.ToLower(k) || strings.Contains(k, "-") {
				t.Fatalf("%T key %q is not snake_case", v, k)
			}
		}
	}
	var rel WorkerRelease
	if err := json.Unmarshal([]byte(`{"worker_id":"w1","reason":"r"}`), &rel); err != nil || rel.CostUSD != nil || rel.Refuse {
		t.Fatalf("release without cost: %+v %v", rel, err)
	}
	if !errors.Is(fmt.Errorf("x: %w", ErrLeaseGone), ErrLeaseGone) || errors.Is(ErrLeaseGone, ErrWorkerUnknown) {
		t.Fatal("sentinel errors")
	}
}
