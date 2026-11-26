package main

import (
	"os"
	"strings"
	"testing"
)

// TestMCPInvestigationToolsReadOnly: the investigation tools only read
// through the caller's own API token (GET), never write.
func TestMCPInvestigationToolsReadOnly(t *testing.T) {
	src, err := os.ReadFile("mcpserve_w449.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"postJSON", "MethodPost", "MethodPut", "MethodDelete", ".do(ctx"} {
		if strings.Contains(string(src), w) {
			t.Errorf("mcpserve_w449.go uses %s: the investigation tools must only read", w)
		}
	}
}
