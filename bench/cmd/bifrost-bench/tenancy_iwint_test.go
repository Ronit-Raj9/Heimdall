package main

import (
	"encoding/json"
	"testing"
)

// suitesListHas reports whether a GET /suites answer serves the suite id in
// its suites list (reserved_ids only names ids a new benchmark cannot take).
func suitesListHas(t *testing.T, body, id string) bool {
	t.Helper()
	var out struct {
		Suites []struct {
			ID string `json:"id"`
		} `json:"suites"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("suites answer: %v: %s", err, body)
	}
	for _, s := range out.Suites {
		if s.ID == id {
			return true
		}
	}
	return false
}
