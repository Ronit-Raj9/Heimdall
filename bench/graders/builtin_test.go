package graders

import "testing"

func TestBuiltin(t *testing.T) {
	gs := Builtin(HiddenTestsConfig{})
	if len(gs) != 1 {
		t.Fatalf("got %d graders, want 1", len(gs))
	}
	if gs[0].GetName() != HiddenTestsType || gs[0].GetVersion() != hiddenTestsVersion {
		t.Fatalf("got %s@%s", gs[0].GetName(), gs[0].GetVersion())
	}
}
