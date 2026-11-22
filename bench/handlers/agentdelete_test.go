package handlers

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/evalstore"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// agentDeleteSource lists the project's agents plus one built-in library
// agent ("library-bot"), the way serveAgents does.
type agentDeleteSource struct{ root string }

func (s agentDeleteSource) AgentConfigs(context.Context) ([]AgentConfigFile, error) {
	out := []AgentConfigFile{{Path: "agents/library-bot.yaml", Config: &schemas.AgentConfig{ID: "library-bot"}}}
	files, _ := filepath.Glob(filepath.Join(s.root, "agents", "*.yaml"))
	for _, f := range files {
		id := filepath.Base(f)
		id = id[:len(id)-len(".yaml")]
		out = append(out, AgentConfigFile{Path: "agents/" + filepath.Base(f), Config: &schemas.AgentConfig{ID: id}})
	}
	return out, nil
}

func agentDeleteRouter(t *testing.T) (*router.Router, string) {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("agents/zz-test-loop.yaml", "id: zz-test-loop\nadapter: command\nimage: bench/loop:1\nargv: [\"loop\"]\n")
	write("agents/images/zz-test-loop/Dockerfile", "FROM scratch\n")
	write("agents/zz-test-loop/skills/s1/SKILL.md", "# s1\n")
	write(".heimdall/agent-versions/zz-test-loop/1.yaml", "id: zz-test-loop\n")
	write("agents/codex.yaml", "id: codex\nadapter: command\nimage: bench/codex:1\nargv: [\"codex\"]\n")
	ps, err := projectfs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	r := router.New()
	registerAgentDelete(OrgEnv{Mode: "local", Services: Services{Projects: ps, Agents: agentDeleteSource{root: root}}}, r)
	return r, root
}

func agentDeleteExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func TestDeleteAgentRemovesFile(t *testing.T) {
	r, root := agentDeleteRouter(t)
	code, body := doAgentOps(r, "DELETE", APIPrefix+"/agents/zz-test-loop", "", nil)
	if code != fasthttp.StatusNoContent {
		t.Fatalf("delete: status %d, body %v", code, body)
	}
	if agentDeleteExists(filepath.Join(root, "agents", "zz-test-loop.yaml")) {
		t.Fatal("agents/zz-test-loop.yaml still there")
	}
	if agentDeleteExists(filepath.Join(root, "agents", "images", "zz-test-loop")) {
		t.Fatal("agents/images/zz-test-loop still there")
	}
	if agentDeleteExists(filepath.Join(root, "agents", "zz-test-loop")) {
		t.Fatal("agents/zz-test-loop (skills and tools) still there")
	}
	if !agentDeleteExists(filepath.Join(root, ".heimdall", "agent-versions", "zz-test-loop", "1.yaml")) {
		t.Fatal("saved versions were removed; they must stay")
	}
	if !agentDeleteExists(filepath.Join(root, "agents", "codex.yaml")) {
		t.Fatal("another agent was removed")
	}
	// Gone now: a second delete is a 404.
	if code, _ := doAgentOps(r, "DELETE", APIPrefix+"/agents/zz-test-loop", "", nil); code != fasthttp.StatusNotFound {
		t.Fatalf("second delete: status %d, want 404", code)
	}
}

func TestDeleteAgentUnknownIsNotFound(t *testing.T) {
	r, _ := agentDeleteRouter(t)
	if code, _ := doAgentOps(r, "DELETE", APIPrefix+"/agents/nobody", "", nil); code != fasthttp.StatusNotFound {
		t.Fatalf("status %d, want 404", code)
	}
	if code, _ := doAgentOps(r, "DELETE", APIPrefix+"/agents/..%2Fx", "", nil); code != fasthttp.StatusBadRequest && code != fasthttp.StatusNotFound {
		t.Fatalf("bad id: status %d, want 400 or 404", code)
	}
}

func TestDeleteBuiltinAgentRefused(t *testing.T) {
	r, _ := agentDeleteRouter(t)
	code, body := doAgentOps(r, "DELETE", APIPrefix+"/agents/library-bot", "", nil)
	if code != fasthttp.StatusConflict {
		t.Fatalf("status %d, want 409 (%v)", code, body)
	}
	if msg := agentDeleteMsg(body); msg != "Built-in agents cannot be deleted." {
		t.Fatalf("message %q", msg)
	}
}

func TestDeleteAgentWithRunningEvaluationRefused(t *testing.T) {
	r, root := agentDeleteRouter(t)
	st := evalstore.Open(root)
	for _, ev := range []*schemas.Evaluation{
		{ID: "ev_done1", Status: schemas.EvaluationDone, CreatedAt: time.Now(), Spec: schemas.EvaluationSpec{Arms: []schemas.EvaluationArm{{Agent: "codex"}}}},
		{ID: "ev_run1", Status: schemas.EvaluationRunning, CreatedAt: time.Now(), Spec: schemas.EvaluationSpec{Arms: []schemas.EvaluationArm{{Agent: "other"}, {Agent: "zz-test-loop"}}}},
	} {
		if err := st.Save(ev); err != nil {
			t.Fatal(err)
		}
	}
	code, body := doAgentOps(r, "DELETE", APIPrefix+"/agents/zz-test-loop", "", nil)
	if code != fasthttp.StatusConflict {
		t.Fatalf("status %d, want 409 (%v)", code, body)
	}
	if msg := agentDeleteMsg(body); msg != "This agent has evaluations still running. Cancel them first." {
		t.Fatalf("message %q", msg)
	}
	if !agentDeleteExists(filepath.Join(root, "agents", "zz-test-loop.yaml")) {
		t.Fatal("file removed despite the refusal")
	}
	// A finished evaluation does not block.
	if code, body := doAgentOps(r, "DELETE", APIPrefix+"/agents/codex", "", nil); code != fasthttp.StatusNoContent {
		t.Fatalf("codex: status %d (%v)", code, body)
	}
}

func TestAgentDeleteNoProjects(t *testing.T) {
	r := router.New()
	registerAgentDelete(OrgEnv{Mode: "local"}, r)
	if code, _ := doAgentOps(r, "DELETE", APIPrefix+"/agents/codex", "", nil); code != fasthttp.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", code)
	}
}

func agentDeleteMsg(body map[string]any) string {
	if e, ok := body["error"].(map[string]any); ok {
		if m, ok := e["message"].(string); ok {
			return m
		}
	}
	return ""
}
