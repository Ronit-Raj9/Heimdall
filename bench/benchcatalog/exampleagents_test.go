package benchcatalog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExampleAgents(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("examples/acme-node/agents/acme-agent.yaml", "id: acme-agent\nadapter: command\n")
	write("examples/acme-team/agents/solo.yaml", "id: \"acme-solo\"\nadapter: command\n")
	write("examples/acme-helpdesk/agents/claude-code.yaml", "id: claude-code\nadapter: claude-code\nmodel: x\n")
	// Identical to a built-in template: a real agent made from it stays.
	write("examples/acme-web/agents/codex.yaml", "id: codex\nadapter: codex\n")
	write("agents/codex.yaml", "id: codex\nadapter: codex\n")
	m := NewExampleAgents(root)
	cases := []struct {
		id, content string
		want        bool
	}{
		{"acme-agent", "", true},
		{"acme-solo", "anything", true},
		{"claude-code", "id: claude-code\nadapter: claude-code\nmodel: x\n", true}, // copied from an example
		{"claude-code", "id: claude-code\nadapter: claude-code\nmodel: mine\n", false},
		{"codex", "id: codex\nadapter: codex\n", false},
		{"acme-mine", "id: acme-mine\n", false}, // an org's own agent named acme-*
	}
	for _, c := range cases {
		var content []byte
		if c.content != "" {
			content = []byte(c.content)
		}
		if got := m.Match(c.id, content); got != c.want {
			t.Errorf("Match(%s, %q) = %v, want %v", c.id, c.content, got, c.want)
		}
	}
	if NewExampleAgents("").Match("acme-agent", nil) {
		t.Error("empty root matched")
	}
	// The real examples declare the three Acme agents.
	if src := SourceRoot(); src != "" {
		real := NewExampleAgents(src)
		for _, id := range []string{"acme-agent", "acme-solo", "acme-team"} {
			if !real.Match(id, nil) {
				t.Errorf("%s not recognized from %s", id, src)
			}
		}
	}
}
