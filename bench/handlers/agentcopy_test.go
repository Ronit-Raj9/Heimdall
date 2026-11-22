package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/evalstore"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

const agentCopyYAML = "id: zz-test-loop\n# my loop agent\nadapter: command\nimage: bench/agent-zz-test-loop:1\nbuild:\n  dockerfile: agents/images/zz-test-loop/Dockerfile\n  tag: bench/agent-zz-test-loop:1\nargv: [\"loop\"]\n"

// agentCopyRouter is a project with one agent of its own (zz-test-loop, with
// an image folder, a skills folder and one saved version), codex, and the
// built-in library-bot, served by the copy and delete routes.
func agentCopyRouter(t *testing.T) (*router.Router, string) {
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
	write("agents/zz-test-loop.yaml", agentCopyYAML)
	write("agents/images/zz-test-loop/Dockerfile", "FROM scratch\n")
	write("agents/zz-test-loop/skills/s1/SKILL.md", "# s1\n")
	write("agents/codex.yaml", "id: codex\nadapter: command\nimage: bench/codex:1\nargv: [\"codex\"]\n")
	ps, err := projectfs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	env := OrgEnv{Mode: "local", Services: Services{Projects: ps, Agents: agentDeleteSource{root: root}}}
	r := router.New()
	registerAgentCopy(env, r)
	return r, root
}

func agentCopyPost(r *router.Router, id string, req AgentCopyRequest) (int, map[string]any) {
	body, _ := json.Marshal(req)
	return doAgentOps(r, "POST", APIPrefix+"/agents/"+id+"/copy", "application/json", body)
}

func agentCopyRead(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func TestCopyAgentWritesNewID(t *testing.T) {
	r, root := agentCopyRouter(t)
	code, body := agentCopyPost(r, "zz-test-loop", AgentCopyRequest{NewID: "zz-test-loop-copy"})
	if code != fasthttp.StatusCreated {
		t.Fatalf("status %d, want 201 (%v)", code, body)
	}
	if body["id"] != "zz-test-loop-copy" || body["path"] != "agents/zz-test-loop-copy.yaml" {
		t.Fatalf("answer %v", body)
	}
	if v, _ := body["version"].(float64); v != 1 {
		t.Fatalf("version %v, want 1 (the copy starts at v1)", body["version"])
	}
	doc := agentCopyRead(t, root, "agents/zz-test-loop-copy.yaml")
	for _, want := range []string{"id: zz-test-loop-copy", "# my loop agent", "image: bench/agent-zz-test-loop-copy:1", "dockerfile: agents/images/zz-test-loop-copy/Dockerfile", "tag: bench/agent-zz-test-loop-copy:1"} {
		if !strings.Contains(doc, want) {
			t.Fatalf("copied YAML lacks %q:\n%s", want, doc)
		}
	}
	if got := agentCopyRead(t, root, "agents/images/zz-test-loop-copy/Dockerfile"); got != "FROM scratch\n" {
		t.Fatalf("image folder not copied: %q", got)
	}
	if got := agentCopyRead(t, root, "agents/zz-test-loop-copy/skills/s1/SKILL.md"); got != "# s1\n" {
		t.Fatalf("skills folder not copied: %q", got)
	}
	if !agentDeleteExists(filepath.Join(root, ".heimdall", "agent-versions", "zz-test-loop-copy")) {
		t.Fatal("no saved version for the copy")
	}
	// The original is untouched.
	if got := agentCopyRead(t, root, "agents/zz-test-loop.yaml"); got != agentCopyYAML {
		t.Fatalf("original changed:\n%s", got)
	}
}

func TestCopyAgentRefusesTakenName(t *testing.T) {
	r, root := agentCopyRouter(t)
	code, body := agentCopyPost(r, "zz-test-loop", AgentCopyRequest{NewID: "codex"})
	if code != fasthttp.StatusConflict {
		t.Fatalf("status %d, want 409 (%v)", code, body)
	}
	if msg := agentDeleteMsg(body); msg != "An agent named codex already exists. Pick another name." {
		t.Fatalf("message %q", msg)
	}
	if !strings.Contains(agentCopyRead(t, root, "agents/codex.yaml"), "id: codex") {
		t.Fatal("codex was overwritten")
	}
	// A built-in name is taken too.
	if code, _ := agentCopyPost(r, "zz-test-loop", AgentCopyRequest{NewID: "library-bot"}); code != fasthttp.StatusConflict {
		t.Fatalf("library-bot: status %d, want 409", code)
	}
	for _, c := range []struct {
		id, msg string
	}{
		{strings.Repeat("a", 120), "Use lowercase letters, digits, '.', '_' or '-', starting with a letter or digit, up to 63 characters."},
		{"Bad Name", "Use lowercase letters, digits, '.', '_' or '-', starting with a letter or digit, up to 63 characters."},
		{"zz-test-loop", "Pick a different name."},
	} {
		code, body := agentCopyPost(r, "zz-test-loop", AgentCopyRequest{NewID: c.id})
		if code != fasthttp.StatusBadRequest || agentDeleteMsg(body) != c.msg {
			t.Fatalf("new_id %q: status %d message %q", c.id, code, agentDeleteMsg(body))
		}
	}
	if code, _ := agentCopyPost(r, "nobody", AgentCopyRequest{NewID: "x"}); code != fasthttp.StatusNotFound {
		t.Fatalf("unknown agent: status %d, want 404", code)
	}
}

func TestRenameAgentRemovesOriginal(t *testing.T) {
	r, root := agentCopyRouter(t)
	code, body := agentCopyPost(r, "zz-test-loop", AgentCopyRequest{NewID: "zz-test-loop-2", RemoveOriginal: true})
	if code != fasthttp.StatusCreated {
		t.Fatalf("status %d, want 201 (%v)", code, body)
	}
	if !strings.Contains(agentCopyRead(t, root, "agents/zz-test-loop-2.yaml"), "id: zz-test-loop-2") {
		t.Fatal("renamed agent not written")
	}
	for _, gone := range []string{"agents/zz-test-loop.yaml", "agents/images/zz-test-loop", "agents/zz-test-loop"} {
		if agentDeleteExists(filepath.Join(root, filepath.FromSlash(gone))) {
			t.Fatalf("%s still there after the rename", gone)
		}
	}
	if !agentDeleteExists(filepath.Join(root, "agents", "zz-test-loop-2", "skills", "s1", "SKILL.md")) {
		t.Fatal("skills did not move with the rename")
	}
}

func TestRenameRefusedWhileEvaluationRuns(t *testing.T) {
	r, root := agentCopyRouter(t)
	ev := &schemas.Evaluation{ID: "ev_run1", Status: schemas.EvaluationRunning, CreatedAt: time.Now(), Spec: schemas.EvaluationSpec{Arms: []schemas.EvaluationArm{{Agent: "zz-test-loop"}}}}
	if err := evalstore.Open(root).Save(ev); err != nil {
		t.Fatal(err)
	}
	code, body := agentCopyPost(r, "zz-test-loop", AgentCopyRequest{NewID: "zz-test-loop-2", RemoveOriginal: true})
	if code != fasthttp.StatusConflict {
		t.Fatalf("status %d, want 409 (%v)", code, body)
	}
	if msg := agentDeleteMsg(body); msg != "This agent has evaluations still running. Cancel them first." {
		t.Fatalf("message %q", msg)
	}
	if agentDeleteExists(filepath.Join(root, "agents", "zz-test-loop-2.yaml")) {
		t.Fatal("copy written despite the refusal")
	}
	// A plain duplicate is fine while it runs.
	if code, body := agentCopyPost(r, "zz-test-loop", AgentCopyRequest{NewID: "zz-test-loop-copy"}); code != fasthttp.StatusCreated {
		t.Fatalf("duplicate: status %d (%v)", code, body)
	}
}

func TestCopyBuiltinRefused(t *testing.T) {
	r, _ := agentCopyRouter(t)
	code, body := agentCopyPost(r, "library-bot", AgentCopyRequest{NewID: "my-bot"})
	if code != fasthttp.StatusConflict {
		t.Fatalf("status %d, want 409 (%v)", code, body)
	}
	if msg := agentDeleteMsg(body); msg != "Built-in agents cannot be copied. Create a new agent from a template instead." {
		t.Fatalf("message %q", msg)
	}
}
