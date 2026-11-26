package handlers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/onboard"
)

const reservedSentence = "coding-core is the name of a built-in benchmark. Pick another id."

// TestReservedIDDraftAndPublishConflict: a draft or a publish into a
// built-in id answers 409 with the plain sentence of N46, not the 403
// read-only text.
func TestReservedIDDraftAndPublishConflict(t *testing.T) {
	fj := &onboardFakeJobs{}
	c := newOnboardClient(t, fj)
	code, body := c.do(t, "POST", "/onboard/upload?filename=faq.csv", []byte("question,answer\nWhat is 6 x 7?,42\n"), nil)
	if code != 200 {
		t.Fatalf("upload %d %s", code, body)
	}
	var up OnboardUploadResult
	_ = json.Unmarshal(body, &up)
	in := map[string]any{"upload": up.Upload}
	code, body = c.do(t, "POST", "/onboard/inspect", jsonBody(t, map[string]any{"input": in}), nil)
	var inv onboard.Inventory
	if code != 200 || json.Unmarshal(body, &inv) != nil {
		t.Fatalf("inspect %d %s", code, body)
	}
	plan := inv.Suggest
	plan.Suite = " coding-core "
	code, body = c.do(t, "POST", "/onboard/drafts", jsonBody(t, map[string]any{"input": in, "plan": plan}), nil)
	if code != 409 || !strings.Contains(string(body), reservedSentence) {
		t.Fatalf("draft into coding-core: want 409 %q, got %d %s", reservedSentence, code, body)
	}

	// A draft made under an ordinary id, renamed to the built-in id on disk:
	// publish refuses it the same way before any job is queued.
	code, body = c.do(t, "POST", "/onboard/drafts", jsonBody(t, map[string]any{"input": in, "plan": inv.Suggest}), nil)
	var set onboard.DraftSet
	if code != 201 || json.Unmarshal(body, &set) != nil {
		t.Fatalf("draft %d %s", code, body)
	}
	saved, err := onboard.LoadSet(c.root, set.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved.Suite = "coding-core"
	if err := saved.Save(); err != nil {
		t.Fatal(err)
	}
	code, body = c.do(t, "POST", "/onboard/drafts/"+set.ID+"/publish", []byte(`{"include_unready":true}`), nil)
	if code != 409 || !strings.Contains(string(body), reservedSentence) || len(fj.created) != 0 {
		t.Fatalf("publish into coding-core: want 409 %q and no job, got %d %s (jobs %d)", reservedSentence, code, body, len(fj.created))
	}
}
