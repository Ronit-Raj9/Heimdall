package main

import (
	"context"
	"encoding/json"
	"testing"
)

// TestMCPCostPerTaskByTaskSaysSo: grouping by task (also the default) is
// answered with a plain sentence, before any read.
func TestMCPCostPerTaskByTaskSaysSo(t *testing.T) {
	for _, raw := range []string{`{"exp_id":"e1"}`, `{"exp_id":"e1","group_by":"task"}`} {
		_, err := mcpCostPerTask(context.Background(), nil, json.RawMessage(raw))
		if err == nil || err.Error() != mcpCostPerTaskMissing {
			t.Fatalf("%s: %v", raw, err)
		}
	}
}
