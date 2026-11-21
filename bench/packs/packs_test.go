package packs

import (
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	avail := []string{"tau2", "agentdojo-banking", "agentdojo-slack", "hd-harbor-tb2", "gaia"}
	if got := strings.Join(Resolve("support", avail), ","); got != "agentdojo-banking,agentdojo-slack,tau2" {
		t.Fatal(got)
	}
	if got := Resolve("voice", avail); len(got) != 0 {
		t.Fatal(got)
	}
	if got := Resolve(AllCompatible, avail); len(got) != len(avail) || got[0] != "agentdojo-banking" {
		t.Fatal(got)
	}
	if Resolve("nope", avail) != nil {
		t.Fatal("unknown pack")
	}
}
