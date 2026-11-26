package traceinbox

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/traceimport"
)

// TestAcceptStepsNoCLI checks that the steps the web shows after an accept
// name no command line tool (wave 4, N78); the command line prints its own.
func TestAcceptStepsNoCLI(t *testing.T) {
	res := &AcceptResult{Task: "tools/refund-timeout", Draft: &traceimport.DraftResult{Dir: "/p/benchmarks/tools/tasks/refund-timeout", EditMe: []string{"task.yaml"}}}
	steps := AcceptSteps(res, "/p")
	for _, s := range steps {
		if strings.Contains(s, "bifrost-bench") || strings.Contains(s, "heimdall ") {
			t.Errorf("step names the command line tool: %q", s)
		}
	}
	if n := len(steps); n < 2 || steps[n-2] != "Check the task on the benchmark's Build tab (Readiness check)" || steps[n-1] != "Admit the task from the same tab" {
		t.Errorf("steps = %q", steps)
	}
}
