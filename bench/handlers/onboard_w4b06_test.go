package handlers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/onboard"
)

// TestDraftRefusesHiddenPracticeID: a draft into the practice suite id
// coding-core is refused even though no library pack serves it here, so a
// hosted org whose list hides coding-core cannot take the built-in name.
func TestDraftRefusesHiddenPracticeID(t *testing.T) {
	c := newOnboardClient(t, &onboardFakeJobs{})
	csv := "question,answer\nWhat is 6 x 7?,42\n"
	code, body := c.do(t, "POST", "/onboard/upload?filename=faq.csv", []byte(csv), nil)
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
	plan.Suite = "coding-core"
	code, body = c.do(t, "POST", "/onboard/drafts", jsonBody(t, map[string]any{"input": in, "plan": plan}), nil)
	if code < 400 || !strings.Contains(string(body), "coding-core") {
		t.Fatalf("draft into coding-core: want a refusal naming the id, got %d %s", code, body)
	}
}

// TestIsReservedSuiteID: the practice ids are reserved with no library.
func TestIsReservedSuiteID(t *testing.T) {
	if !isReservedSuiteID(nil, "coding-core") {
		t.Fatal("coding-core is not reserved")
	}
	if isReservedSuiteID(nil, "my-suite") || isReservedSuiteID(nil, "") {
		t.Fatal("an ordinary id is reserved")
	}
}
