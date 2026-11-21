package auth

import "testing"

func TestIsSharedPoolPath(t *testing.T) {
	for p, want := range map[string]bool{
		SharedPoolAssignmentsPath:                    true,
		SharedPoolAssignmentsPath + "/org_a/token":   true,
		SharedPoolAssignmentsPath + "/org_a":         false,
		SharedPoolAssignmentsPath + "//token":        false,
		SharedPoolAssignmentsPath + "/org_a/token/x": false,
		"/api/bench/runs":                            false,
	} {
		if IsSharedPoolPath(p) != want {
			t.Errorf("%s: want %v", p, want)
		}
	}
}
