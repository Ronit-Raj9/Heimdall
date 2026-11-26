package handlers

import "testing"

// TestPlanSentencesEndEachSentence: planner lines joined for a person read
// as sentences: a line without a final period gets one, so the next
// sentence never runs on (wave 4, group B review of D2 2.6).
func TestPlanSentencesEndEachSentence(t *testing.T) {
	got := planSentences([]string{"plan e_x: unknown agent agents/x.yaml", "plan e_y: Skill a of agent b has no passing safety scan. Open the agent's Tools tab and press Scan."})
	want := "unknown agent agents/x.yaml. Skill a of agent b has no passing safety scan. Open the agent's Tools tab and press Scan."
	if got != want {
		t.Fatalf("planSentences = %q, want %q", got, want)
	}
	if got := planSentence(" fine? "); got != "fine?" {
		t.Fatalf("planSentence kept or doubled the end: %q", got)
	}
	if got := planSentence(""); got != "" {
		t.Fatalf("planSentence of nothing = %q", got)
	}
}
