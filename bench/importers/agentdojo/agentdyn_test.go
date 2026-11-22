package agentdojo

import (
	"strings"
	"testing"
)

// AgentDyn suites are accepted, carry the fork as their source and tag, and
// the adaptive attack gives the service gateway access.
func TestAgentDynAdaptiveSettings(t *testing.T) {
	set, err := parseSettings(map[string]string{SetSuite: "shopping", SetAttacks: "important_instructions,adaptive"})
	if err != nil {
		t.Fatal(err)
	}
	if set.sourceRepo() != agentDynRepo || !set.adaptive() {
		t.Fatalf("source %q adaptive %v", set.sourceRepo(), set.adaptive())
	}
	w := &writer{set: set, up: &upstream{license: "MIT"}, imageTag: "bench/agentdojo:x"}
	y := w.taskYAML("shopping-user-task-0", userTask{ID: "user_task_0"})
	for _, want := range []string{"tags: [agentdojo, agentdyn, shopping]", "    gateway: {}\n", agentDynRepo} {
		if !strings.Contains(y, want) {
			t.Errorf("task.yaml lacks %q:\n%s", want, y)
		}
	}
	clean, err := parseSettings(map[string]string{SetSuite: "banking"})
	if err != nil {
		t.Fatal(err)
	}
	w = &writer{set: clean, up: &upstream{license: "MIT"}, imageTag: "bench/agentdojo:x"}
	if y := w.taskYAML("banking-user-task-0", userTask{ID: "user_task_0"}); strings.Contains(y, "gateway") || !strings.Contains(y, upstreamRepo) {
		t.Errorf("banking task.yaml:\n%s", y)
	}
}
