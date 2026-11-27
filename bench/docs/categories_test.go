package docs

import "testing"

func TestCategoryOf(t *testing.T) {
	ids := map[string]bool{}
	for _, c := range Categories {
		ids[c.ID] = true
	}
	for _, n := range Names() {
		if c := CategoryOf(n); !ids[c] {
			t.Fatalf("%s: category %q is not listed", n, c)
		}
	}
	for name, want := range map[string]string{
		"web-quickstart": "start", "web-sync": "start", "importing-swe": "importing",
		"tool-safety": "safety", "voice-agents": "voice", "ci": "guides",
	} {
		if got := CategoryOf(name); got != want {
			t.Fatalf("%s = %s, want %s", name, got, want)
		}
	}
	for _, n := range startHere {
		if _, ok := Read(n); !ok {
			t.Fatalf("start guide %s does not exist", n)
		}
	}
}
