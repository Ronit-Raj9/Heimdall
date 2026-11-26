package apptasks

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/taskcat"
	"github.com/maximhq/bifrost/bench/taskguard"
)

// TestAppTasksWriteWritesGuard: each written task has guard.json with builder
// api and a clear-by-person instruction, and the policy category.
func TestAppTasksWriteWritesGuard(t *testing.T) {
	st, d, m := newDraft(t)
	a := &Assistant{Chat: &fakeChat{}, Model: Pinned{Provider: "openai", Model: "test-model"}, Budget: &Budget{MaxCalls: 50, Price: price}}
	_, policy, _ := st.Files(d)
	if err := DraftRules(context.Background(), a, d, m, policy); err != nil {
		t.Fatal(err)
	}
	_ = ApproveRules(d, m, d.Rules, "ana")
	if err := GenerateTasks(context.Background(), a, d, m, policy, GenerateOptions{}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), d.Suite)
	written, err := WriteBenchmark(dir, st, d, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) == 0 {
		t.Fatal("nothing written")
	}
	cats, err := taskcat.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range written {
		r, err := taskguard.Read(filepath.Join(dir, "tasks", w.ID))
		if err != nil || r == nil {
			t.Fatalf("%s: guard.json = %v %v", w.ID, r, err)
		}
		if r.Builder != taskguard.BuilderAPI || r.Clarity == nil || r.Clarity.Verdict != taskguard.ClarityClear || r.Clarity.Origin != taskguard.OriginPerson || r.Source == "" {
			t.Errorf("%s: guard record = %+v", w.ID, r)
		}
		if cats[w.ID].Category != taskcat.Policy {
			t.Errorf("%s: category = %+v", w.ID, cats[w.ID])
		}
	}
}

func TestGuardRecordSkipsGivenValues(t *testing.T) {
	sc := Scenario{Opening: "My order ORDER-ABCDEF-123 never came", Reply: "Your refund of 45 pounds is on its way",
		Expected: []Action{{Tool: "refund", Args: map[string]any{"order_id": "ORDER-ABCDEF-123", "note": "refund for late order", "n": 3}}}}
	r := guardRecordOf(&Draft{ID: "d1"}, sc)
	if len(r.LeakTexts) != 2 || r.LeakTexts[0] != sc.Reply || r.LeakTexts[1] != "refund for late order" {
		t.Fatalf("leak texts = %q", r.LeakTexts)
	}
}
