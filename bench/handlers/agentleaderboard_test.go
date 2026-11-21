package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/valyala/fasthttp"
)

// getAgentBoard serves one GET /leaderboard/agents request.
func getAgentBoard(t *testing.T, index ReadIndex, root, query string) (int, AgentLeaderboard) {
	t.Helper()
	h := NewLeaderboardHandler(testMode, index).WithAgentBoard(root, nil)
	h.now = func() time.Time { return time.Date(2026, 11, 15, 9, 30, 0, 0, time.UTC) }
	r := router.New()
	h.RegisterRoutes(r, lib.BodyLimit(testMode, lib.MaxBodyBytes))
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod(fasthttp.MethodGet)
	ctx.Request.SetRequestURI(APIPrefix + "/leaderboard/agents" + query)
	r.Handler(&ctx)
	var body AgentLeaderboard
	if ctx.Response.StatusCode() == fasthttp.StatusOK {
		if err := json.Unmarshal(ctx.Response.Body(), &body); err != nil {
			t.Fatalf("decode %q: %v", ctx.Response.Body(), err)
		}
	}
	return ctx.Response.StatusCode(), body
}

func TestAgentLeaderboardRows(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".heimdall", "agent-versions", "codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "versions.json"), []byte(`[{"version":1,"hash":"aaa"},{"version":2,"hash":"sha256:bbb"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	status, b := getAgentBoard(t, leaderboardIndex(), root, "")
	if status != fasthttp.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if len(b.Rows) != 2 {
		t.Fatalf("rows = %+v, want 2 (two versions of codex)", b.Rows)
	}
	top := b.Rows[0]
	if top.Agent != "codex" || top.Version != 2 || top.Model != "gpt-5.4-mini" || !top.Baseline {
		t.Errorf("top = %+v, want codex v2 baseline", top)
	}
	c, ok := top.Capabilities["coding"]
	if !ok || c.Score != 1 || c.Runs != 2 || c.CILow <= 0 || c.CIHigh != 1 || len(c.Benchmarks) != 1 || c.Benchmarks[0] != "coding-core" {
		t.Errorf("coding = %+v", c)
	}
	if b.Rows[1].Version != 1 || b.Rows[1].Capabilities["coding"].Score != 0 {
		t.Errorf("second = %+v", b.Rows[1])
	}
	if len(b.Capabilities) != 1 || b.Capabilities[0] != "coding" || len(b.Models) != 1 {
		t.Errorf("capabilities = %v models = %v", b.Capabilities, b.Models)
	}
	if top.LastExperiment != testExp {
		t.Errorf("last_experiment = %q", top.LastExperiment)
	}
}

func TestAgentLeaderboardNoVersions(t *testing.T) {
	_, b := getAgentBoard(t, leaderboardIndex(), t.TempDir(), "")
	// Without an index each config hash is its own row, version 0.
	if len(b.Rows) != 2 || b.Rows[0].Version != 0 || b.Rows[0].ConfigHash != "sha256:bbb" {
		t.Errorf("rows = %+v", b.Rows)
	}
}

func TestAgentLeaderboardFilters(t *testing.T) {
	tests := []struct {
		name   string
		query  string
		status int
		rows   int
	}{
		{"capability", "?capability=coding", fasthttp.StatusOK, 2},
		{"capability without rows", "?capability=voice", fasthttp.StatusOK, 0},
		{"unknown capability", "?capability=juggling", fasthttp.StatusBadRequest, 0},
		{"model", "?model=gpt-5.4-mini", fasthttp.StatusOK, 2},
		{"other model", "?model=claude-x", fasthttp.StatusOK, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, b := getAgentBoard(t, leaderboardIndex(), "", tt.query)
			if status != tt.status || len(b.Rows) != tt.rows {
				t.Errorf("status = %d rows = %d, want %d %d", status, len(b.Rows), tt.status, tt.rows)
			}
			if status == fasthttp.StatusOK && len(b.Models) != 1 {
				t.Errorf("models = %v, want the filter list unaffected by the filter", b.Models)
			}
		})
	}
	if status, _ := getAgentBoard(t, nil, "", ""); status != fasthttp.StatusServiceUnavailable {
		t.Errorf("no index status = %d", status)
	}
}
