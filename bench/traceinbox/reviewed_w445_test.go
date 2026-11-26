package traceinbox

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/traceimport"
)

// TestAcceptWithGuideMarksReviewed: an accept with a guide (the person
// reviewed the instruction on the review screen) marks the task's
// provenance instruction_reviewed: true; a plain accept does not.
func TestAcceptWithGuideMarksReviewed(t *testing.T) {
	src := &Source{Name: "test", Kind: KindWebhook}
	accept := func(id string, opts AcceptOptions) string {
		ib, _ := newTestInbox(t, nil, nil)
		trace := `{"id":"` + id + `","status":"failed","messages":[{"role":"user","content":"What is the refund window for order ` + id + `?"},{"role":"assistant","content":"Ten days."}]}`
		in, err := ib.Ingest(context.Background(), src, IngestRequest{Trace: json.RawMessage(trace)})
		if err != nil {
			t.Fatal(err)
		}
		res, err := ib.Accept(context.Background(), in.ItemID, opts)
		if err != nil {
			t.Fatal(err)
		}
		ty, err := os.ReadFile(filepath.Join(res.Draft.Dir, "task.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		return string(ty)
	}
	ty := accept("q-1", AcceptOptions{Suite: "answers", Guide: &traceimport.Guide{Answers: []string{"30 days"}, Match: "contains_all"}})
	if !strings.Contains(ty, "instruction_reviewed: true") {
		t.Errorf("guided task.yaml has no instruction_reviewed: true:\n%s", ty)
	}
	ty = accept("q-2", AcceptOptions{Suite: "answers"})
	if strings.Contains(ty, "instruction_reviewed: true") {
		t.Errorf("a plain accept marked the instruction reviewed:\n%s", ty)
	}
}
