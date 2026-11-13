package verdict

import "testing"

func TestBuiltin(t *testing.T) {
	policies := Builtin()
	seen := map[string]bool{}
	for _, p := range policies {
		t.Run(p.GetName(), func(t *testing.T) {
			if seen[p.GetName()] {
				t.Fatalf("duplicate policy %s", p.GetName())
			}
			seen[p.GetName()] = true
			if p.GetVersion() == "" {
				t.Fatal("empty version")
			}
		})
	}
	if !seen[PolicyPairedSignFlip] {
		t.Fatalf("builtin policies %v lack %s", seen, PolicyPairedSignFlip)
	}
	if v := newPairedSignFlipPolicy().GetVersion(); v != "1.0.0" {
		t.Fatalf("version %s, want 1.0.0", v)
	}
}
