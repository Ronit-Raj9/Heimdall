package handlers

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// agentDiffIndex is the leaderboard index (arms A and B of agent codex, t01
// A fail and B pass, t02 A unsealed) with seal verification.
func agentDiffIndex() sealIndex {
	idx := leaderboardIndex()
	a := idx.manifests[testExp].Arms["A"]
	a.Config.Files = map[string]string{"/home/agent/prompt.md": "Run the tests.\n"}
	a.Files = map[string]map[string]string{"clean": {"/home/agent/prompt.md": "h1"}}
	idx.manifests[testExp].Arms["A"] = a
	idx.manifests[testExp].Arms["B"] = schemas.ArmManifest{Label: "notes", ConfigHash: "sha256:bbb",
		Config: schemas.AgentConfig{ID: "codex", Adapter: "command", Model: "gpt-5.5", Env: map[string]string{"OPENAI_API_KEY": "sk-live"},
			Files: map[string]string{"/home/agent/prompt.md": "Run every test.\n"}},
		Files: map[string]map[string]string{"clean": {"/home/agent/prompt.md": "h2"}}}
	return sealIndex{fakeIndex: idx, bad: map[string]error{}}
}

// getAgentDiff serves one agent diff request on a fresh router.
func getAgentDiff(t *testing.T, index ReadIndex, path string) (int, map[string]any) {
	t.Helper()
	r := router.New()
	NewAgentDiffHandler(testMode, index, "").RegisterRoutes(r, lib.BodyLimit(testMode, lib.MaxBodyBytes))
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod(fasthttp.MethodGet)
	ctx.Request.SetRequestURI(APIPrefix + path)
	r.Handler(&ctx)
	var body map[string]any
	if err := json.Unmarshal(ctx.Response.Body(), &body); err != nil {
		t.Fatalf("decode %q: %v", ctx.Response.Body(), err)
	}
	if body["mode"] != string(testMode) {
		t.Errorf("mode = %v", body["mode"])
	}
	return ctx.Response.StatusCode(), body
}

func TestAgentDiffRoute(t *testing.T) {
	tests := []struct {
		name   string
		index  ReadIndex
		path   string
		status int
		errHas string
	}{
		{name: "two arms", index: agentDiffIndex(), path: "/agents/diff?a=e_test:A&b=e_test:B", status: fasthttp.StatusOK},
		{name: "hash prefix form", index: agentDiffIndex(), path: "/agents/diff?a=e_test:A&b=e_test:B&condition=clean&suite=coding-core@1.0.0&synthetic=0", status: fasthttp.StatusOK},
		{name: "no versions", index: agentDiffIndex(), path: "/agents/diff", status: fasthttp.StatusBadRequest, errHas: "two versions"},
		{name: "no common task", index: agentDiffIndex(), path: "/agents/diff?a=e_test:A&b=e_test:B&condition=stale-notes", status: fasthttp.StatusBadRequest, errHas: "share no task"},
		{name: "bad synthetic", index: agentDiffIndex(), path: "/agents/diff?a=e_test:A&b=e_test:B&synthetic=maybe", status: fasthttp.StatusBadRequest, errHas: "synthetic"},
		{name: "no seal verifier", index: leaderboardIndex(), path: "/agents/diff?a=e_test:A&b=e_test:B", status: fasthttp.StatusServiceUnavailable, errHas: "seal verification"},
		{name: "no index", index: nil, path: "/agents/diff/versions", status: fasthttp.StatusServiceUnavailable},
		{name: "versions", index: agentDiffIndex(), path: "/agents/diff/versions", status: fasthttp.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, body := getAgentDiff(t, tt.index, tt.path)
			if status != tt.status {
				t.Fatalf("status = %d, want %d: %v", status, tt.status, body)
			}
			if tt.errHas != "" {
				msg, _ := body["error"].(map[string]any)["message"].(string)
				if !strings.Contains(msg, tt.errHas) {
					t.Fatalf("error %q lacks %q", msg, tt.errHas)
				}
			}
		})
	}
}

func TestAgentDiffValues(t *testing.T) {
	idx := agentDiffIndex()
	_, body := getAgentDiff(t, idx, "/agents/diff?a=e_test:A&b=e_test:B")
	out := body["outcome"].(map[string]any)
	if out["common_tasks"] != float64(1) || out["counts"].(map[string]any)["flipped_to_pass"] != float64(1) {
		t.Fatalf("outcome %v", out)
	}
	task := objectsAt(t, body, "outcome.tasks[]")[0]
	if task["task"] != "t01" || task["pair"].(map[string]any)["pair_id"] != "p_t01" {
		t.Fatalf("task %v", task)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sk-live") {
		t.Fatal("env secret served")
	}
	t.Run("seal mismatch leaves the run out", func(t *testing.T) {
		idx := agentDiffIndex()
		idx.bad["r_t01_B"] = fmt.Errorf("run r_t01_B: %w", schemas.ErrSealMismatch)
		status, body := getAgentDiff(t, idx, "/agents/diff?a=e_test:A&b=e_test:B")
		msg, _ := body["error"].(map[string]any)["message"].(string)
		if status != fasthttp.StatusBadRequest || !strings.Contains(msg, "seal mismatch") {
			t.Fatalf("status %d %q", status, msg)
		}
	})
}

// TestAgentDiffContract pins the responses to the key lists the UI reads.
func TestAgentDiffContract(t *testing.T) {
	_, body := getAgentDiff(t, agentDiffIndex(), "/agents/diff?a=e_test:A&b=e_test:B")
	checkKeys(t, "AgentDiffReport", body, loadKeys(t, "AgentDiffReport"), true)
	nested := map[string]string{
		"a": "AgentDiffVersion", "b": "AgentDiffVersion", "a.excluded[]": "AgentDiffExclusion",
		"config": "AgentDiffConfig", "config.groups[]": "AgentDiffConfigGroup", "config.groups[].fields[]": "AgentDiffFieldChange", "config.groups[].files[]": "AgentDiffFileChange",
		"outcome": "AgentDiffOutcome", "outcome.counts": "AgentDiffClassCounts", "outcome.test": "AgentDiffSignTest",
		"outcome.tasks[]": "AgentDiffTask", "outcome.tasks[].pair": "AgentDiffRunPair", "outcome.tasks[].pair.a": "AgentDiffRunRef",
		"outcome.metrics[]": "AgentDiffMetric",
	}
	for path, typ := range nested {
		for i, obj := range objectsAt(t, body, path) {
			checkKeys(t, fmt.Sprintf("AgentDiffReport.%s #%d", path, i), obj, loadKeys(t, typ), false)
		}
	}
	_, versions := getAgentDiff(t, agentDiffIndex(), "/agents/diff/versions")
	checkKeys(t, "AgentDiffVersionList", versions, loadKeys(t, "AgentDiffVersionList"), true)
	for path, typ := range map[string]string{"versions[]": "AgentDiffVersionInfo", "versions[].arms[]": "AgentDiffArmRef"} {
		for i, obj := range objectsAt(t, versions, path) {
			checkKeys(t, fmt.Sprintf("AgentDiffVersionList.%s #%d", path, i), obj, loadKeys(t, typ), false)
		}
	}
}
