package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/toolproxy"
)

// checkResearchShape checks the research template (W4): track research,
// artifact output graded by verifier_command, the websearch MCP service and
// the tavily external API in replay mode at the suite level, the answer and
// sources artifacts, and script controls.
func checkResearchShape(t *testing.T, s *schemas.Suite, task *schemas.Task) {
	t.Helper()
	if s.Track != schemas.TrackResearch || s.Output == nil || s.Output.Kind != schemas.OutputArtifact || s.Grader != "verifier_command" || s.Verifier.Result != schemas.VerifierResultReward {
		t.Errorf("suite track %s output %+v grader %s result %s", s.Track, s.Output, s.Grader, s.Verifier.Result)
	}
	if len(s.Services) != 1 || s.Services[0].ID != "websearch" || s.Services[0].Role != schemas.ServiceRoleMCP || s.Services[0].Image != "bench/demo-websearch:1" {
		t.Errorf("services = %+v", s.Services)
	}
	if len(s.External) != 1 {
		t.Fatalf("external = %+v", s.External)
	}
	x := s.External[0]
	if x.ID != "tavily" || x.BaseURL != "https://api.tavily.com" || x.Mode != schemas.ExternalReplay || x.AuthEnv != "TAVILY_API_KEY" || x.AuthHeader != "Authorization" || x.AuthScheme != "Bearer" || len(x.Allow) != 2 {
		t.Errorf("tavily api = %+v", x)
	}
	for _, a := range x.Allow {
		if len(a.Methods) != 1 || a.Methods[0] != "POST" || (a.PathPrefix != "/search" && a.PathPrefix != "/extract") {
			t.Errorf("allow %+v: want POST /search and POST /extract only", a)
		}
	}
	f, err := os.Open(filepath.Join(s.Root, filepath.FromSlash(x.Cassette)))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if entries, err := toolproxy.ReadCassette(f); err != nil || len(entries) == 0 {
		t.Errorf("cassette: %d entries, %v", len(entries), err)
	}
	if task.Output == nil || len(task.Output.Artifacts) != 2 || task.Output.Artifacts[0].ID != "answer" || !task.Output.Artifacts[0].Required || task.Output.Artifacts[1].ID != "sources" {
		t.Errorf("task output = %+v", task.Output)
	}
	if !strings.HasSuffix(task.Controls.Reference, ".sh") || !strings.HasSuffix(task.Controls.Exploit, ".sh") {
		t.Errorf("controls = %+v, want scripts", task.Controls)
	}
}
