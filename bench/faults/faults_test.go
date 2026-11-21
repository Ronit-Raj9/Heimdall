package faults

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestConditions(t *testing.T) {
	s := &schemas.Suite{ProbeIDs: []string{"tool-error", "stale-notes"}}
	conds, skips := Conditions([]string{"clean", MisleadingInstruction, ModelDown, MisleadingInstruction}, s)
	if strings.Join(conds, ",") != "clean,stale-notes" || len(skips) != 1 || skips[0].Fault != ModelDown {
		t.Fatalf("%v %v", conds, skips)
	}
	conds, skips = Conditions([]string{MisleadingInstruction}, nil)
	if strings.Join(conds, ",") != "clean" || len(skips) != 1 {
		t.Fatalf("%v %v", conds, skips)
	}
}
