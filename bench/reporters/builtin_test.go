package reporters

import "testing"

func TestBuiltin(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range Builtin() {
		if r.GetName() == "" || r.GetVersion() == "" || seen[r.GetName()] {
			t.Fatalf("bad or duplicate reporter %q %q", r.GetName(), r.GetVersion())
		}
		seen[r.GetName()] = true
	}
	if !seen[ReporterJSON] || !seen[ReporterMarkdown] {
		t.Fatalf("builtin = %v", seen)
	}
}
