package scaffold

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/visual"
)

// TestBrowserRecordingPin keeps the browser template on the Playwright MCP
// image that was checked to record (H10): @playwright/mcp 0.0.83 reads
// browser.contextOptions from --config and passes recordVideo and recordHar
// to each isolated context (checked in the image: a navigate, then SIGTERM,
// leaves video/page-1.webm, har/context-1.har and console.jsonl). The
// template's base and the visual renderer move together, and record.json
// keeps asking for video and HAR.
func TestBrowserRecordingPin(t *testing.T) {
	df, err := templates.ReadFile("templates/suites/browser/services/browser/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	from := ""
	for _, line := range strings.Split(string(df), "\n") {
		if strings.HasPrefix(line, "FROM ") {
			from = strings.TrimSpace(strings.TrimPrefix(line, "FROM "))
		}
	}
	if from != visual.DefaultRenderer {
		t.Errorf("browser template base %q, visual renderer %q: bump both together", from, visual.DefaultRenderer)
	}
	if !strings.Contains(from, "@sha256:") {
		t.Errorf("browser template base %q is not pinned by digest", from)
	}
	raw, err := templates.ReadFile("templates/suites/browser/services/browser/record.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Browser struct {
			ContextOptions map[string]json.RawMessage `json:"contextOptions"`
		} `json:"browser"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"recordVideo", "recordHar"} {
		if _, ok := cfg.Browser.ContextOptions[k]; !ok {
			t.Errorf("record.json contextOptions has no %s", k)
		}
	}
}
