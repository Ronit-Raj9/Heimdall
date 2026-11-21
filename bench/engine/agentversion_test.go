package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/agentversions"
	"github.com/maximhq/bifrost/bench/utils"
)

const avDoc = `id: exploit
adapter: scripted
image: bench/coding-core-base:1
scripted:
  kind: exploit
model: claude-sonnet-4-5
budgets:
  wall_s: 900
  cpus: 1
  mem_gb: 1
gateway:
  provider: anthropic
`

// The versions' hash is the planner's arm config hash, so run counts by
// config hash line up with versions.
func TestAgentVersionHashIsPlannerHash(t *testing.T) {
	cfg, err := ResolveAgentConfig([]byte(avDoc), nil)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := utils.HashJSON(&cfg)
	got, err := agentversions.ConfigHash([]byte(avDoc))
	if err != nil || got != want {
		t.Fatalf("hash %s want %s (%v)", got, want, err)
	}
}

func TestAgentVersionPath(t *testing.T) {
	root, lib := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(root, "agents"), 0o755)
	os.WriteFile(filepath.Join(root, "agents", "mine.yaml"), []byte(strings.Replace(avDoc, "id: exploit", "id: mine", 1)), 0o644)
	os.MkdirAll(filepath.Join(lib, "agents"), 0o755)
	os.WriteFile(filepath.Join(lib, "agents", "exploit.yaml"), []byte(avDoc), 0o644)
	v1 := strings.Replace(avDoc, "id: exploit", "id: mine", 1)
	if _, _, err := agentversions.Snapshot(root, "mine", []byte(v1), "", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := agentversions.Snapshot(root, "mine", []byte(strings.Replace(v1, "900", "60", 1)), "", ""); err != nil {
		t.Fatal(err)
	}
	p := &Planner{benchRoot: root, library: lib}
	cases := map[string]string{
		"agents/mine.yaml":       "agents/mine.yaml",
		"agents/mine.yaml@v1":    ".heimdall/agent-versions/mine/v1.yaml",
		"agents/mine.yaml@v2":    ".heimdall/agent-versions/mine/v2.yaml",
		"agents/exploit.yaml@v1": "agents/exploit.yaml",
	}
	for in, want := range cases {
		got, err := p.agentVersionPath(in)
		if err != nil || got != want {
			t.Fatalf("%s: %q %v, want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"agents/mine.yaml@v3", "agents/exploit.yaml@v2", "agents/none.yaml@v1"} {
		if _, err := p.agentVersionPath(bad); err == nil {
			t.Fatalf("%s resolved", bad)
		}
	}
	// The frozen snapshot is what the arm reads.
	raw, err := os.ReadFile(p.abs(cases["agents/mine.yaml@v2"]))
	if err != nil || !strings.Contains(string(raw), "wall_s: 60") {
		t.Fatalf("%s %v", raw, err)
	}
	if got := p.abs("agents/exploit.yaml"); got != filepath.Join(lib, "agents", "exploit.yaml") {
		t.Fatalf("library abs %s", got)
	}
}
