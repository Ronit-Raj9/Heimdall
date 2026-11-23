package apptasks

import (
	"encoding/json"
	"strings"
	"testing"
)

// A draft never stores or serves a null list (fix wave 3, N237): a draft
// approved with no plans was saved as "plans": null and crashed the page.
func TestDraftRoundTripNeverNull(t *testing.T) {
	d := &Draft{ID: "ag_0123456789ab", Status: StatusRulesApproved, Rules: []Rule{{ID: "R1", Text: "x", Kind: "must"}}}
	raw, err := d.store()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "null") {
		t.Fatalf("stored draft has a null:\n%s", raw)
	}
	back, err := loadDraft(raw)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(back)
	if strings.Contains(string(out), "null") {
		t.Fatalf("served draft has a null: %s", out)
	}

	// A file saved before the fix opens with empty lists.
	old := []byte(`{"id":"ag_0123456789ab","status":"rules_approved","rules":[{"id":"R1","text":"x","kind":"must","operations":null}],"plans":null,"dropped":null,"written":null,"calls":null,"warnings":null}`)
	back, err = loadDraft(old)
	if err != nil {
		t.Fatal(err)
	}
	if back.Plans == nil || back.Dropped == nil || back.Written == nil || back.Calls == nil || back.Warnings == nil || back.Rules[0].Operations == nil {
		t.Fatalf("nil list after load: %+v", back)
	}
	out, _ = json.Marshal(back)
	if strings.Contains(string(out), "null") {
		t.Fatalf("old draft served with a null: %s", out)
	}
}
