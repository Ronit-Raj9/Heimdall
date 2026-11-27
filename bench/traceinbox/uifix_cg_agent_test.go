package traceinbox

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestPullRecordsAgent: an item taken from an OTLP trace keeps the
// resource's service.name as its agent (fix wave M7-12).
func TestPullRecordsAgent(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{Sources: []Source{{Name: "collector", Kind: KindOTLPDir, Path: dir}}}
	ib, _ := newTestInbox(t, cfg, nil)
	s := map[string]any{"traceId": "eeeeeeeeeeeeeeee5555555555555555", "spanId": "eeeeeeee0001", "name": "execute_tool book",
		"startTimeUnixNano": fmt.Sprint(testNow.Add(-time.Hour).UnixNano()), "endTimeUnixNano": fmt.Sprint(testNow.Add(-time.Hour + time.Second).UnixNano()),
		"attributes": []any{map[string]any{"key": "gen_ai.operation.name", "value": map[string]any{"stringValue": "execute_tool"}},
			map[string]any{"key": "gen_ai.tool.name", "value": map[string]any{"stringValue": "book_table"}}},
		"status": map[string]any{"code": 2, "message": "boom"}}
	res := map[string]any{"attributes": []any{map[string]any{"key": "service.name", "value": map[string]any{"stringValue": "booking-agent"}}}}
	b, _ := json.Marshal(map[string]any{"resourceSpans": []any{map[string]any{"resource": res, "scopeSpans": []any{map[string]any{"spans": []any{s}}}}}})
	if err := os.WriteFile(filepath.Join(dir, "t.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ib.Pull(context.Background(), PullOptions{}); err != nil {
		t.Fatal(err)
	}
	items, err := ib.List(context.Background(), "", "")
	if err != nil || len(items) != 1 || items[0].Agent != "booking-agent" {
		t.Fatalf("items %+v %v", items, err)
	}
}
