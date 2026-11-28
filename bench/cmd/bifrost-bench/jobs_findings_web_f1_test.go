package main

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/investigator"
)

// Lane F1. These bodies are the ones the web sends, byte for byte the shape
// of investigationBody (components/findings/findingPlans.ts) and
// traceDraftJob (components/inbox/proposalBar.tsx); the UI tests hold the
// same field lists. The old Find out why body had no model and was refused.

const webInvestigateBody = `{"exp_id":"exp1","provider":"openai","model":"gpt-5.4-mini","budget_usd":0.75,"yes":true}`

func TestInvestigateParamsFromTheWebForm(t *testing.T) {
	p, err := parseInvestigateParams(json.RawMessage(webInvestigateBody))
	if err != nil {
		t.Fatalf("the web body is refused: %v", err)
	}
	if p.ExpID != "exp1" || p.Provider != "openai" || p.Model != "gpt-5.4-mini" || p.BudgetUSD != 0.75 || !p.Yes {
		t.Fatalf("params %+v", p)
	}
	_, err = parseInvestigateParams(json.RawMessage(`{"exp_id":"exp1","budget_usd":0.75,"yes":true}`))
	if err == nil || !strings.Contains(err.Error(), "Pick a model first.") {
		t.Fatalf("a body with no model: %v", err)
	}
}

func TestTraceDraftParamsFromTheWebForm(t *testing.T) {
	var p traceDraftParams
	body := `{"item":"it1","suite":"s1","kind":"qa","provider":"openai","model":"gpt-5.4-mini","budget_usd":0.5,"yes":true}`
	if err := liveDecode(json.RawMessage(body), &p); err != nil {
		t.Fatalf("the web body is refused: %v", err)
	}
	if p.Provider != "openai" || p.Model != "gpt-5.4-mini" || p.BudgetUSD != 0.5 {
		t.Fatalf("params %+v", p)
	}
}

// PT-1: the job id the runner really makes names a key the gateway accepts.
// The investigator's tests used the job id "j1", which hid the fault.
func TestInvestigatorKeyNameFromARealJobID(t *testing.T) {
	rule := regexp.MustCompile(`^r_[a-z0-9]+$`) // gateways/bifrost/keys.go runIDPattern
	for range 20 {
		id := db.NewID("job")
		if got := investigator.KeyRunID(id); !rule.MatchString(got) {
			t.Fatalf("job %s names key %s", id, got)
		}
		if got := investigator.KeyRunID("iv_" + id); !rule.MatchString(got) {
			t.Fatalf("job iv_%s names key %s", id, got)
		}
	}
}
