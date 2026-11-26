package handlers

import (
	"context"
	"strings"
	"testing"
)

// TestFeedbackWritesOnlyTokenOrgAndEchoesNothing: feedback lands in the
// token's organization only, and the answer never repeats what was stored.
func TestFeedbackWritesOnlyTokenOrgAndEchoesNothing(t *testing.T) {
	f := newFeedbackFixture(t, nil)
	c, res := f.send(t, "acme", f.secret, map[string]any{"session_id": "s-123", "rating": "down",
		"comment": "private remark about order A-1", "trace": feedbackTrace()})
	if c.Response.StatusCode() != 201 || res.ItemID == "" {
		t.Fatalf("send %d %s", c.Response.StatusCode(), c.Response.Body())
	}
	body := string(c.Response.Body())
	for _, s := range []string{"private remark", "Refund order A-1", "Your refund was issued"} {
		if strings.Contains(body, s) {
			t.Fatalf("answer echoes %q: %s", s, body)
		}
	}
	if l, err := f.inbox("org_b").List(context.Background(), "", ""); err != nil || len(l) != 0 {
		t.Fatalf("other org's inbox %+v %v", l, err)
	}
	if f.feedbackLog(t, "org_b") != "" {
		t.Fatal("feedback log written in the other org")
	}
}
