package handlers

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/valyala/fasthttp"
)

// zipStore unpacks zips through Write (the stub store has no ExtractZip yet).
type zipStore struct{ projectfs.Store }

func (z zipStore) ExtractZip(ctx context.Context, a projectfs.Area, dir string, r io.ReaderAt, size int64, o projectfs.WriteOpts) ([]projectfs.Entry, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, projectfs.ErrInvalid
	}
	var out []projectfs.Entry
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		e, err := z.Write(ctx, a, dir+"/"+f.Name, data, o)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

func agentOpsRouter(t *testing.T) (*router.Router, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "# my agent\nid: codex\nadapter: command\nimage: bench/codex:1\nargv: [\"codex\", \"exec\"]\n"
	if err := os.WriteFile(filepath.Join(root, "agents", "codex.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	ps, err := projectfs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	r := router.New()
	registerAgentOps(OrgEnv{Mode: "local", Services: Services{Projects: zipStore{ps}}}, r)
	return r, root
}

func doAgentOps(r *router.Router, method, uri, ctype string, body []byte) (int, map[string]any) {
	ctx := &fasthttp.RequestCtx{}
	ctx.Init(&fasthttp.Request{}, nil, nil)
	ctx.Request.Header.SetMethod(method)
	ctx.Request.SetRequestURI(uri)
	if ctype != "" {
		ctx.Request.Header.SetContentType(ctype)
	}
	ctx.Request.SetBody(body)
	r.Handler(ctx)
	var out map[string]any
	_ = json.Unmarshal(ctx.Response.Body(), &out)
	return ctx.Response.StatusCode(), out
}

func agentOpsZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestAgentSkillsUploadListDelete(t *testing.T) {
	r, root := agentOpsRouter(t)
	base := APIPrefix + "/agents/codex/skills"

	if code, _ := doAgentOps(r, "GET", base, "", nil); code != 200 {
		t.Fatalf("list empty: %d", code)
	}
	if code, _ := doAgentOps(r, "POST", base+"?name=review", "application/zip", []byte("not a zip")); code != 415 {
		t.Fatalf("non-zip: %d, want 415", code)
	}
	if code, _ := doAgentOps(r, "POST", base+"?name=../x", "application/zip", agentOpsZip(t, map[string]string{"SKILL.md": "x"})); code != 400 {
		t.Fatalf("bad name: %d, want 400", code)
	}
	if code, _ := doAgentOps(r, "POST", base+"?name=empty", "application/zip", agentOpsZip(t, map[string]string{"README.md": "x"})); code != 400 {
		t.Fatalf("no SKILL.md: %d, want 400", code)
	}
	if _, err := os.Stat(filepath.Join(root, "agents", "codex", "skills", "empty")); !os.IsNotExist(err) {
		t.Fatalf("rejected skill was kept: %v", err)
	}
	// One top folder is hoisted.
	z := agentOpsZip(t, map[string]string{"review/SKILL.md": "---\nname: review\n---\n", "review/scripts/run.sh": "echo hi\n"})
	code, body := doAgentOps(r, "POST", base+"?name=review", "application/zip", z)
	if code != 201 {
		t.Fatalf("upload: %d %v", code, body)
	}
	if _, err := os.Stat(filepath.Join(root, "agents", "codex", "skills", "review", "SKILL.md")); err != nil {
		t.Fatalf("SKILL.md not at the skill's top: %v", err)
	}
	if body["installed"] != true {
		t.Fatalf("skills.extra not set: %v", body)
	}
	cfg, _ := os.ReadFile(filepath.Join(root, "agents", "codex.yaml"))
	if !strings.Contains(string(cfg), "agents/codex/skills") || !strings.Contains(string(cfg), "# my agent") {
		t.Fatalf("config after upload:\n%s", cfg)
	}
	skills, _ := body["skills"].([]any)
	if len(skills) != 1 || skills[0].(map[string]any)["valid"] != true {
		t.Fatalf("skills: %v", body["skills"])
	}
	if code, _ := doAgentOps(r, "POST", base+"?name=review", "application/zip", z); code != 409 {
		t.Fatalf("duplicate: %d, want 409", code)
	}
	if code, _ := doAgentOps(r, "POST", base+"?name=review&replace=true", "application/zip", z); code != 201 {
		t.Fatalf("replace: %d", code)
	}
	if code, body := doAgentOps(r, "DELETE", base+"/review", "", nil); code != 200 || len(body["skills"].([]any)) != 0 {
		t.Fatalf("delete: %d %v", code, body)
	}
	if code, _ := doAgentOps(r, "GET", APIPrefix+"/agents/nobody/skills", "", nil); code != 404 {
		t.Fatalf("unknown agent: %d, want 404", code)
	}
}

func TestAgentMCPEditor(t *testing.T) {
	r, root := agentOpsRouter(t)
	uri := APIPrefix + "/agents/codex/mcp"
	put := func(v AgentMCPRequest) (int, map[string]any) {
		b, _ := json.Marshal(v)
		return doAgentOps(r, "PUT", uri, "application/json", b)
	}
	if code, _ := put(AgentMCPRequest{Format: "toml"}); code != 400 {
		t.Fatalf("bad format: %d", code)
	}
	if code, _ := put(AgentMCPRequest{ServersYAML: "services:\n  - id: x\n    role: data\n    image: a\n"}); code != 400 {
		t.Fatalf("non-mcp role: %d", code)
	}
	servers := "services:\n  - id: files\n    image: ghcr.io/x/files@sha256:abc\n    port: 8080\n"
	code, body := put(AgentMCPRequest{Format: "codex_toml", MountPath: "/root/.codex/config.toml", ServersYAML: servers})
	if code != 200 || body["format"] != "codex_toml" || len(body["servers"].([]any)) != 1 {
		t.Fatalf("put: %d %v", code, body)
	}
	cfg, _ := os.ReadFile(filepath.Join(root, "agents", "codex.yaml"))
	if !strings.Contains(string(cfg), "format: codex_toml") {
		t.Fatalf("config:\n%s", cfg)
	}
	if code, body := doAgentOps(r, "GET", uri, "", nil); code != 200 || body["servers_yaml"] != servers {
		t.Fatalf("get: %d %v", code, body)
	}
	if code, _ := put(AgentMCPRequest{}); code != 200 {
		t.Fatalf("clear: %d", code)
	}
	if _, err := os.Stat(filepath.Join(root, "agents", "codex", "mcp.yaml")); !os.IsNotExist(err) {
		t.Fatalf("servers file kept after clear: %v", err)
	}
}

func TestAgentOpsNoProjects(t *testing.T) {
	r := router.New()
	registerAgentOps(OrgEnv{Mode: "local"}, r)
	if code, _ := doAgentOps(r, "GET", APIPrefix+"/agents/codex/skills", "", nil); code != 503 {
		t.Fatalf("no projects: %d, want 503", code)
	}
}
