package main

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestA2ARows(t *testing.T) {
	if a2aRows(&checkInput{agent: &schemas.AgentConfig{Adapter: "command"}}) != nil {
		t.Fatal("no row for an agent that is not an a2a agent")
	}
	agent := &schemas.AgentConfig{Adapter: "a2a", A2A: &schemas.A2AAgent{CardURL: "http://a/card"}}
	in := &checkInput{agent: agent}
	if rows := a2aRows(in); len(rows) != 1 || rows[0].Status != checkFail {
		t.Fatalf("no witness fails: %+v", rows)
	}
	in.ev.A2A = &schemas.A2AEvidence{Entry: "hb-r-1-lead", Complete: true, Strength: "witnessed", Operations: []schemas.A2AOperation{{ID: "op1"}}}
	if rows := a2aRows(in); rows[0].Status != checkWarn {
		t.Fatalf("no linked model call warns: %+v", rows)
	}
	in.ev.A2A.Linked = []schemas.A2ALinkedRow{{Kind: "llm", ID: "l1", TaskID: "t1"}}
	if rows := a2aRows(in); rows[0].Status != checkPass {
		t.Fatalf("a complete, linked witness passes: %+v", rows)
	}
	in.ev.A2A.Complete, in.ev.A2A.Reason = false, "1 launcher operation(s) are not in the agent log"
	if rows := a2aRows(in); rows[0].Status != checkFail {
		t.Fatalf("an incomplete witness fails: %+v", rows)
	}
}
