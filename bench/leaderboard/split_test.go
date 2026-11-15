package leaderboard

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestHeldOutBoardIsSeparate checks the public board never counts a run of
// a held-out task and the held-out board counts nothing else.
func TestHeldOutBoardIsSeparate(t *testing.T) {
	f := newFake()
	arms := map[string]schemas.ArmManifest{"A": arm("a", "hash-a", "command")}
	f.exp("e_pub", "s@1.0.0", 1, arms)
	held := f.exp("e_held", "s@1.0.0", 2, arms)
	held.HeldOutTasks = []string{"h1"}
	f.pass("e_pub", "p1", "A", "clean", 1, 0.1)
	f.fail("e_held", "h1", "A", "clean", 1, 0.1)
	f.pass("e_held", "p1", "A", "clean", 1, 0.1) // a public task run inside a held-out experiment stays public

	tests := []struct {
		split     string
		wantRuns  int
		wantSplit string
	}{
		{"", 2, ""},
		{"public", 2, ""},
		{"held_out", 1, "held_out"},
	}
	for _, tt := range tests {
		t.Run("split="+tt.split, func(t *testing.T) {
			lb := buildOrFail(t, f, Query{Suite: "s@1.0.0", Split: tt.split})
			if len(lb.Entries) != 1 || lb.Entries[0].Runs != tt.wantRuns || lb.Split != tt.wantSplit {
				t.Fatalf("entries %+v split %q, want %d runs split %q", lb.Entries, lb.Split, tt.wantRuns, tt.wantSplit)
			}
			md := Markdown(lb)
			if got := strings.Contains(md, "HELD-OUT BOARD"); got != (tt.split == "held_out") {
				t.Fatalf("markdown banner %t:\n%s", got, md)
			}
			raw, err := json.Marshal(lb)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(string(raw), `"split"`); got != (tt.split == "held_out") {
				t.Fatalf("json split key %t: a public board must encode as before", got)
			}
		})
	}
}

func TestSplitQueryValidated(t *testing.T) {
	if _, err := (Query{Split: "secret"}).Normalize(); !errors.Is(err, ErrBadQuery) {
		t.Fatalf("err = %v", err)
	}
	for _, s := range []string{"", "public", "held_out"} {
		if !validSplit(s) {
			t.Fatalf("%q rejected", s)
		}
	}
	if boardSplit("public") != "" || boardSplit("held_out") != "held_out" {
		t.Fatal("boardSplit")
	}
}
