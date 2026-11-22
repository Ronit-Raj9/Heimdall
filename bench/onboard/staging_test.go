package onboard

import "testing"

func TestIsStagingSuite(t *testing.T) {
	for id, want := range map[string]bool{
		"draft-20261122-150634-a4b821":    true,
		"draft-20261122-150634-A4B821":    false, // set ids are lower-case hex
		"draft-20261122-150634-a4b82":     false,
		"draft-20261122-150634-a4b821-x":  false,
		"draft-notes":                     false, // a team may name a benchmark draft-anything
		"draft-":                          false,
		"20261122-150634-a4b821":          false,
		"zz-test-rows":                    false,
		"my-draft-20261122-150634-a4b821": false,
	} {
		if got := IsStagingSuite(id); got != want {
			t.Errorf("IsStagingSuite(%q) = %v, want %v", id, got, want)
		}
	}
	// The id format newSetID makes is a staging id once prefixed.
	if id := "draft-" + newSetID(); !IsStagingSuite(id) {
		t.Errorf("IsStagingSuite(%q) = false for a fresh set id", id)
	}
}
