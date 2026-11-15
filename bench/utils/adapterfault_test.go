package utils

import (
	"github.com/maximhq/bifrost/bench/schemas"
	"testing"
	"time"
)

func TestVerifyAdapterFaultEvidence(t *testing.T) {
	rule := schemas.AdapterFaultRule{Kind: "tool", Name: "search", Mode: "error"}
	ruleHash, _ := HashJSON(rule)
	row := schemas.AdapterFaultEntry{Seq: 1, At: time.Unix(1, 0).UTC(), RunID: "r", Session: "s", Phase: "decision", Mode: "error"}
	row.Hash, _ = AdapterFaultEntryHash(row)
	ev := &schemas.AdapterFaultEvidence{Version: schemas.AdapterFaultProtocol, RunID: "r", RuleHash: ruleHash, Complete: true, LedgerHash: row.Hash, Entries: []schemas.AdapterFaultEntry{row}}
	if err := VerifyAdapterFaultEvidence(ev, "r", ruleHash); err != nil {
		t.Fatal(err)
	}
	ev.Entries[0].Mode = "pass"
	if err := VerifyAdapterFaultEvidence(ev, "r", ruleHash); err == nil {
		t.Fatal("tampered row accepted")
	}
}
