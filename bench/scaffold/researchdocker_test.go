//go:build integration

package scaffold

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/research"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
)

// TestResearchTemplateInDocker starts the research template's sample task
// the way a run does, with the real environment kernel: the websearch MCP
// server as a proxied service and the tool proxy with the tavily external
// API in replay mode (no Tavily key, no network). A client container on the
// run's service network, where the agent sits, runs the sample's reference
// script, which searches and fetches through the websearch tool. Then it
// checks that every search landed in the ledger twice (the MCP call and the
// replayed Tavily call), that the replayed results hold the cited page, and
// that the proxy injected no credential.
func TestResearchTemplateInDocker(t *testing.T) {
	for _, img := range []string{browserTestHelper, browserTestToolProxy} {
		if exec.Command("docker", "image", "inspect", img).Run() != nil {
			t.Skipf("image %s is missing (built by bifrost-bench init)", img)
		}
	}
	ctx := context.Background()
	root, dir := newSuiteDir(t, "hd-w4-it", TemplateResearch)
	s, task, err := suite.LoadTask(root, "hd-w4-it", "ferry-start")
	if err != nil {
		t.Fatal(err)
	}
	tag := "hd-w4-websearch:test"
	buildTestImage(t, tag, filepath.Join(dir, "services", "websearch"))
	s.Services[0].Image = tag

	env := environments.Builtin(environments.DockerWorkspaceConfig{
		HelperImage: browserTestHelper, ToolProxyImage: browserTestToolProxy, Docker: utils.Docker{},
	})[0]
	svc, ok := env.(schemas.ServiceEnvironment)
	if !ok {
		t.Fatal("docker workspace runs no services")
	}
	runID := strings.Replace(utils.NewID("r", 8), "r_", "hd-w4-", 1)
	rc := &schemas.RunContext{
		Run: &schemas.Run{RunID: runID, ExpID: "e_hdw4"}, Slot: &schemas.Slot{Condition: schemas.ConditionClean},
		Suite: s, Task: task, Agent: &schemas.AgentConfig{}, Dir: t.TempDir(),
	}
	t.Cleanup(func() {
		if err := env.Destroy(context.Background(), rc); err != nil {
			t.Errorf("destroy: %v", err)
		}
		out, _ := exec.Command("docker", "ps", "-aq", "--filter", "label="+utils.LabelRun+"="+runID).Output()
		if strings.TrimSpace(string(out)) != "" {
			t.Errorf("containers of %s left behind: %s", runID, out)
		}
	})
	if _, results, err := svc.StartServices(ctx, rc); err != nil {
		t.Fatalf("start services: %v (%+v)", err, results)
	}

	ref, err := os.ReadFile(filepath.Join(task.Dir, "solution", "reference.sh"))
	if err != nil {
		t.Fatal(err)
	}
	servers, _ := json.Marshal(map[string]map[string]string{"websearch": {"url": "http://toolproxy:9200/s/websearch/mcp"}})
	agent := exec.Command("docker", "run", "--rm", "-i", "--network", "bench-net-"+runID, "-e", "BENCH_MCP_SERVERS="+string(servers),
		"--entrypoint", "sh", browserTestHelper, "-c", "cd /tmp && cat > ref.sh && sh ref.sh && cat answer.txt sources.json")
	agent.Stdin = strings.NewReader(string(ref))
	out, err := agent.CombinedOutput()
	if err != nil {
		t.Fatalf("reference script through the websearch tool: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "1962") || !strings.Contains(string(out), "https://calder-transit.example/history/marlow-skerry-ferry") {
		t.Fatalf("agent container output:\n%s", out)
	}

	if _, err := svc.ExportState(ctx, rc); err != nil {
		t.Fatalf("export state: %v", err)
	}
	f, err := os.Open(filepath.Join(rc.Dir, "tools.jsonl"))
	if err != nil {
		t.Fatalf("tool ledger: %v", err)
	}
	defer f.Close()
	var calls []schemas.ToolCall
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<24)
	for sc.Scan() {
		var c schemas.ToolCall
		if err := json.Unmarshal(sc.Bytes(), &c); err == nil && c.Server != "" {
			calls = append(calls, c)
		}
	}
	var mcpSearch, external int
	for _, c := range calls {
		switch {
		case c.Protocol == "mcp" && c.Server == "websearch" && c.Tool == "web_search":
			mcpSearch++
		case c.Protocol == "external" && c.Server == "tavily":
			external++
			if !c.Replayed || c.Status != 200 || c.Unmatched {
				t.Errorf("tavily row %d: replayed %v status %d unmatched %v", c.Seq, c.Replayed, c.Status, c.Unmatched)
			}
			if strings.Contains(c.Arguments, "Bearer") {
				t.Errorf("tavily row %d carries a credential", c.Seq)
			}
		}
	}
	if mcpSearch != 1 || external != 2 {
		t.Fatalf("ledger: %d web_search MCP calls and %d tavily calls, want 1 and 2: %+v", mcpSearch, external, calls)
	}
	returned := research.ReturnedURLs(calls, research.TavilyAPIs(s, task))
	if !returned["https://calder-transit.example/history/marlow-skerry-ferry"] {
		t.Errorf("the cited page is not among the URLs the run's searches returned: %v", returned)
	}
}
