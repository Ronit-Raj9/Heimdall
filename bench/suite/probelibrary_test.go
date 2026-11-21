package suite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestProbeLibrary: every probe in probes/library decodes against the probe
// schema and its overlays name fixtures shipped beside it, so a suite can
// copy the folder in as it is (probes/library/README.md).
func TestProbeLibrary(t *testing.T) {
	lib := filepath.Join("..", "probes", "library")
	dirs, err := os.ReadDir(lib)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		n++
		path := filepath.Join(lib, d.Name(), "probe.yaml")
		var p schemas.ProbeSpec
		if err := DecodeYAMLFile(path, path, SchemaProbe, &p); err != nil {
			t.Fatalf("%s: %v", d.Name(), err)
		}
		if p.ID != d.Name() {
			t.Errorf("%s: id %q", d.Name(), p.ID)
		}
		for _, o := range p.Apply.Overlays {
			rel, ok := strings.CutPrefix(o.From, "{{suite.root}}/fixtures/")
			if !ok {
				t.Errorf("%s: overlay %q is not under fixtures/", d.Name(), o.From)
				continue
			}
			// fixtures/<probe dir name>/... in the suite is fixtures/... here.
			if _, after, ok := strings.Cut(rel, "/"); !ok {
				t.Errorf("%s: overlay %q", d.Name(), o.From)
			} else if _, err := os.Stat(filepath.Join(lib, d.Name(), "fixtures", after)); err != nil {
				t.Errorf("%s: fixture missing: %v", d.Name(), err)
			}
		}
	}
	if n == 0 {
		t.Fatal("no library probes")
	}
}

// TestGatedAgentConfig: the shipped Claude Code config that asks before it
// acts decodes against the agent schema and opts in to the approval channel.
func TestGatedAgentConfig(t *testing.T) {
	path := filepath.Join("..", "agents", "claude-code-gated.yaml")
	var a schemas.AgentConfig
	if err := DecodeYAMLFile(path, path, SchemaAgent, &a); err != nil {
		t.Fatal(err)
	}
	if !a.Approver || !strings.Contains(strings.Join(a.Argv, " "), "--permission-prompt-tool mcp__approver__request_approval") || strings.Contains(strings.Join(a.Argv, " "), "dangerously") {
		t.Errorf("config = %+v", a)
	}
}
