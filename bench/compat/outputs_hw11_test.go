package compat

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// BFCL grades a file the agent writes (/app/result.json), not a code
// change; the bare model's refusal says so (fix wave 3, N187, D37).
func TestOutputWordsFileAnswer(t *testing.T) {
	bare := &schemas.AgentConfig{ID: "bare-model", Adapter: "command", Env: map[string]string{"BENCH_NO_TOOLS": "1"}}
	for _, s := range []*schemas.Suite{
		{ID: "hd-harbor-bfcl", Tasks: []*schemas.Task{{ID: "b1"}}},
		{ID: "hd-harbor-bfcl-multi", Tasks: []*schemas.Task{{ID: "b1"}}},
		{ID: "mine", Output: &schemas.OutputSpec{Kind: "file"}, Tasks: []*schemas.Task{{ID: "f1"}}},
	} {
		ok, why := TaskOK(bare, s, s.Tasks[0])
		if ok || !strings.Contains(why, "it grades a file the agent writes, and the bare model only answers in text") {
			t.Fatalf("%s: ok=%v why=%q", s.ID, ok, why)
		}
	}
	// Other suites keep their words.
	code := &schemas.Suite{ID: "hd-harbor-other", Tasks: []*schemas.Task{{ID: "c1"}}}
	if _, why := TaskOK(bare, code, code.Tasks[0]); !strings.Contains(why, "code change") {
		t.Fatalf("code suite: %q", why)
	}
}
