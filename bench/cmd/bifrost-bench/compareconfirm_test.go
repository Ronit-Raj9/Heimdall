package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestConfirmSpendNotRun covers 7-B-08: a compare that is not run must not
// report success, and the exit code for it is distinct from every check code.
func TestConfirmSpendNotRun(t *testing.T) {
	var out bytes.Buffer
	if confirmSpend(strings.NewReader("y\n"), &out, false, false, 4, 1, "run x") {
		t.Fatal("non-interactive stdin without --yes must not run")
	}
	if !strings.Contains(out.String(), "no verdict") {
		t.Fatalf("output %q", out.String())
	}
	if !confirmSpend(strings.NewReader(""), &out, false, true, 4, 1, "") || !confirmSpend(strings.NewReader(""), &out, true, false, 4, 0, "") {
		t.Fatal("--yes and all-scripted experiments run")
	}
	for _, c := range []int{exitOK, 1, 2, 3, exitInfra, 5, exitUsage} {
		if exitNotRun == c {
			t.Fatalf("exitNotRun %d collides with another exit code", c)
		}
	}
}
