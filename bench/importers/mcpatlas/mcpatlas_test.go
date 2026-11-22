package mcpatlas

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/suite"
)

func TestImportMapsServersAndClaims(t *testing.T) {
	root := t.TempDir()
	im := &importer{now: func() time.Time { return time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC) }}
	res, err := im.Import(context.Background(), importers.Options{BenchRoot: root, From: "testdata/rows.jsonl", SuiteID: "mcp-atlas-gh",
		Extra: map[string]string{SetServers: "github=https://mcp.example.com/github", SetAuth: "github=GITHUB_MCP_TOKEN:Bearer"}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Tasks, []string{"mcpatlas-t-github-1", "mcpatlas-t-github-3"}) {
		t.Fatalf("tasks = %v", res.Tasks)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].ID != "t-mixed-2" || !strings.Contains(res.Skipped[0].Reason, "whois") {
		t.Fatalf("skipped = %+v", res.Skipped)
	}
	dir := filepath.Join(root, "benchmarks", "mcp-atlas-gh")
	s, err := suite.Builtin(filepath.Dir(dir))[0].Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("imported suite does not load: %v", err)
	}
	for _, p := range suite.Lint(s) {
		if p.Severity == "error" {
			t.Errorf("lint error: %s: %s", p.Path, p.Message)
		}
	}
	tk := s.Tasks[0]
	if len(tk.External) != 1 || !tk.External[0].MCP || tk.External[0].ID != "github" || tk.External[0].AuthEnv != "GITHUB_MCP_TOKEN" || tk.External[0].AuthScheme != "Bearer" {
		t.Fatalf("external = %+v", tk.External)
	}
	ans, _ := os.ReadFile(filepath.Join(dir, "tasks/mcpatlas-t-github-1/verifier/answer.yaml"))
	if !strings.Contains(string(ans), "at least 3 of these 3") && !strings.Contains(string(ans), "every one of these 3") {
		t.Fatalf("rubric = %s", ans)
	}
	if !strings.Contains(string(ans), "star count is 942") {
		t.Fatalf("python-quoted claim lost: %s", ans)
	}
	if _, err := os.Stat(filepath.Join(dir, "tasks/mcpatlas-t-github-1/controls/mutant-one-claim.txt")); err != nil {
		t.Fatal("no near-miss mutant for a three-claim task")
	}
	if _, err := os.Stat(filepath.Join(dir, "tasks/mcpatlas-t-github-3/controls/mutant-one-claim.txt")); err == nil {
		t.Fatal("a one-claim task cannot have a one-claim mutant")
	}
	if _, err := os.Stat(filepath.Join(dir, "LICENSE-NOTICE.md")); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsAndLists(t *testing.T) {
	if _, err := parseSettings(map[string]string{}); err == nil {
		t.Fatal("no servers accepted")
	}
	if _, err := parseSettings(map[string]string{SetServers: "github=ftp://x"}); err == nil {
		t.Fatal("bad url accepted")
	}
	if _, err := parseSettings(map[string]string{SetServers: "github=https://x.example/mcp", SetAuth: "fetch=TOKEN"}); err == nil {
		t.Fatal("auth for unknown server accepted")
	}
	got, err := pyStrings(`['a', "b's", 'c\'d']`)
	if err != nil || !slices.Equal(got, []string{"a", "b's", "c'd"}) {
		t.Fatalf("pyStrings = %q, %v", got, err)
	}
	set, _ := parseSettings(map[string]string{SetServers: "mcp-code=https://a.example/mcp,mcp-code-executor=https://b.example/mcp"})
	if s, _ := serverOf("mcp-code-executor_execute_code", set.servers); s == nil || s.name != "mcp-code-executor" {
		t.Fatalf("longest prefix not chosen: %+v", s)
	}
	if needed(3) != 3 || needed(4) != 3 || needed(1) != 1 {
		t.Fatal("coverage rounding")
	}
}
