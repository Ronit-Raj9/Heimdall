package toolproxy

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestFrameRecorderSavesScreenshots(t *testing.T) {
	dir := t.TempDir()
	f := newFrameRecorder(filepath.Join(dir, "tools.jsonl"))
	png := []byte("\x89PNG\r\n\x1a\nshot")
	img := `{"content":[{"type":"image","mimeType":"image/png","data":"` + base64.StdEncoding.EncodeToString(png) + `"},{"type":"text","text":"screenshot"}]}`
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	f.record(&schemas.ToolCall{Seq: 1, Tool: "screenshot", Started: now}, []byte(img))
	f.record(&schemas.ToolCall{Seq: 2, Tool: "click", Arguments: `{"x":1,"y":2}`, Started: now}, nil)
	f.record(&schemas.ToolCall{Seq: 3, Tool: "screenshot", Started: now}, []byte(img))
	f.record(&schemas.ToolCall{Seq: 4, Tool: "screenshot", IsError: true}, []byte(`{"content":[{"type":"text"}]}`))
	for _, n := range []string{"0001.png", "0002.png"} {
		b, err := os.ReadFile(filepath.Join(dir, schemas.FramesDir, n))
		if err != nil || string(b) != string(png) {
			t.Fatalf("%s: %v %q", n, err, b)
		}
	}
	idx, _ := os.ReadFile(filepath.Join(dir, schemas.FramesDir, schemas.FramesIndexFile))
	lines := strings.Split(strings.TrimSpace(string(idx)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[1], `"action":"click {\"x\":1,\"y\":2}"`) {
		t.Fatalf("index:\n%s", idx)
	}
	// A restarted recorder continues numbering.
	if g := newFrameRecorder(filepath.Join(dir, "tools.jsonl")); g.n != 2 {
		t.Fatalf("restart n = %d", g.n)
	}
}
