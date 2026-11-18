package reporters

import (
	"bufio"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestWriteSupport(t *testing.T) {
	render := func(m *schemas.Manifest) string {
		var b strings.Builder
		w := bufio.NewWriter(&b)
		writeSupport(w, m)
		w.Flush()
		return b.String()
	}
	if render(nil) != "" || render(&schemas.Manifest{}) != "" {
		t.Fatal("nothing for a plan without a simulated user")
	}
	if got := render(&schemas.Manifest{Simulator: &schemas.SimulatorSpec{Mode: schemas.SimulatorScripted}}); !strings.Contains(got, "customer: scripted user.") {
		t.Fatalf("scripted: %q", got)
	}
	got := render(&schemas.Manifest{Simulator: &schemas.SimulatorSpec{Mode: schemas.SimulatorLLM, Provider: "anthropic", Model: "claude-sim"}})
	if !strings.Contains(got, "simulated user (model: anthropic/claude-sim)") || !strings.Contains(got, "resolution") {
		t.Fatalf("llm: %q", got)
	}
}
