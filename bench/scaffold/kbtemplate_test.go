package scaffold

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

// TestKnowledgeBaseTemplate checks the knowledge-base template (Y14): an
// answer suite whose sample task reaches the documents only through the kb
// MCP service, and that service's search, read and read log.
func TestKnowledgeBaseTemplate(t *testing.T) {
	root := filepath.Join(t.TempDir(), "benchmarks")
	files, err := Suite(SuiteOptions{ID: "demo", Template: TemplateKnowledgeBase})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "demo")
	if err := WriteFiles(dir, files, false); err != nil {
		t.Fatal(err)
	}
	s, task, err := suite.LoadTask(root, "demo", "refund-window")
	if err != nil {
		t.Fatal(err)
	}
	if s.Grader != "answer_match" || s.Output == nil || s.Output.Kind != schemas.OutputAnswer {
		t.Errorf("suite grader %s output %+v", s.Grader, s.Output)
	}
	if len(task.Services) != 1 || task.Services[0].ID != "kb" || task.Services[0].Role != schemas.ServiceRoleMCP || task.Services[0].Image != "bench/demo-kb:1" ||
		len(task.Services[0].Export) != 1 || task.Services[0].Export[0].ID != "reads.json" {
		t.Errorf("services = %+v", task.Services)
	}
	if DetectTemplate(mustRead(t, filepath.Join(dir, "suite.yaml"))) != TemplateKnowledgeBase {
		t.Error("the template is not detected from suite.yaml")
	}
	for _, p := range suite.ValidateTask(context.Background(), s, task) {
		if p.Severity == suite.SeverityError {
			t.Errorf("sample: %s: %s", p.Path, p.Message)
		}
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	docs := filepath.Join(dir, "services", "kb", "docs")
	if err := os.WriteFile(filepath.Join(docs, "faq.html"), []byte(`<html><head><style>p{}</style></head><body><h1>FAQ</h1><p>Gift cards never expire.</p><script>x()</script></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(t.TempDir(), "reads.json")
	port := scenarioPort(t)
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "python3", "-I", filepath.Join(dir, "services", "kb", "server.py"))
	cmd.Env = append(os.Environ(), fmt.Sprintf("PORT=%d", port), "DOCS_DIR="+docs, "STATE_PATH="+state)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = cmd.Wait() }()
	waitUp(t, port)
	call := func(tool string, args map[string]any) (string, bool) {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
		resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/mcp", port), "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out struct {
			Result struct {
				Content []struct{ Text string } `json:"content"`
				IsError bool                    `json:"isError"`
			} `json:"result"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || len(out.Result.Content) == 0 {
			t.Fatalf("%s: %v", tool, err)
		}
		return out.Result.Content[0].Text, out.Result.IsError
	}
	res, bad := call("search_docs", map[string]any{"query": "return an opened item refund", "k": 2})
	if bad || !strings.Contains(res, `"doc_id": "refund-policy"`) || strings.Index(res, "refund-policy") > strings.Index(res+"warranty", "warranty") {
		t.Fatalf("search: %s", res)
	}
	if res, bad := call("search_docs", map[string]any{"query": "gift cards expire"}); bad || !strings.Contains(res, `"doc_id": "faq"`) || strings.Contains(res, "x()") {
		t.Fatalf("html search: %s", res)
	}
	if res, bad := call("read_doc", map[string]any{"doc_id": "refund-policy"}); bad || !strings.Contains(res, "14 days") {
		t.Fatalf("read: %s", res)
	}
	if _, bad := call("read_doc", map[string]any{"doc_id": "../etc/passwd"}); !bad {
		t.Fatal("an unknown document was read")
	}
	if _, bad := call("search_docs", map[string]any{"query": "refund", "k": 99}); !bad {
		t.Fatal("k over 20 was taken")
	}
	var log struct {
		Corpus   string   `json:"corpus_sha256"`
		Reads    []string `json:"reads"`
		Searches []struct {
			Query   string   `json:"query"`
			Results []string `json:"results"`
		} `json:"searches"`
	}
	if err := json.Unmarshal(mustRead(t, state), &log); err != nil {
		t.Fatal(err)
	}
	if len(log.Corpus) != 64 || len(log.Reads) != 1 || log.Reads[0] != "refund-policy" || len(log.Searches) != 2 {
		t.Fatalf("reads.json = %+v", log)
	}
}
