package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/valyala/fasthttp"
)

// fakeBifrostRepos answers GET paths of the org's Bifrost from fixed JSON.
func fakeBifrostRepos(answers map[string]string) func(ctx context.Context, path string, out any) error {
	return func(_ context.Context, path string, out any) error {
		a, ok := answers[path]
		if !ok {
			return fmt.Errorf("the gateway answered 404: %s", path)
		}
		return json.Unmarshal([]byte(a), out)
	}
}

// gatewayReposTestRouter serves the gatewayrepos routes over a project with
// agent codex, with get as the org's Bifrost (nil: none connected).
func gatewayReposTestRouter(t *testing.T, get func(ctx context.Context, path string, out any) error) (*router.Router, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "id: codex\nadapter: command\nimage: bench/codex:1\nargv: [\"codex\", \"exec\"]\n"
	if err := os.WriteFile(filepath.Join(root, "agents", "codex.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	ps, err := projectfs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	old := gatewayReposSource
	gatewayReposSource = func(context.Context, OrgEnv) func(ctx context.Context, path string, out any) error { return get }
	t.Cleanup(func() { gatewayReposSource = old })
	r := router.New()
	newGatewayReposHandler(OrgEnv{Mode: "local", Services: Services{Projects: zipStore{ps}}}).routes(r)
	return r, root
}

func gatewayReposCall(r *router.Router, method, uri, body string) (int, string) {
	ctx := &fasthttp.RequestCtx{}
	ctx.Init(&fasthttp.Request{}, nil, nil)
	ctx.Request.Header.SetMethod(method)
	ctx.Request.SetRequestURI(uri)
	ctx.Request.SetBodyString(body)
	r.Handler(ctx)
	return ctx.Response.StatusCode(), string(ctx.Response.Body())
}

func TestGatewayReposNeedSource409(t *testing.T) {
	r, _ := gatewayReposTestRouter(t, nil)
	for _, p := range []string{"/gateway/prompts", "/gateway/prompts/p1/versions", "/gateway/skills", "/gateway/skills/s1/versions"} {
		st, out := gatewayReposCall(r, "GET", APIPrefix+p, "")
		if st != 409 || !strings.Contains(out, "Connect your Bifrost first") || !strings.Contains(out, `"/gateway"`) {
			t.Fatalf("%s: %d %s", p, st, out)
		}
	}
}

func TestGatewayReposListPrompts(t *testing.T) {
	r, _ := gatewayReposTestRouter(t, fakeBifrostRepos(map[string]string{
		"/api/prompt-repo/prompts": `{"prompts": [{"id": "p1", "name": "support", "folder_id": "f"}]}`,
		"/api/prompt-repo/prompts/p1/versions": `{"versions": [{"version_number": 3, "commit_message": "first", "created_at": "2026-11-01T00:00:00Z", "model": "gpt-5",
			"messages": [{"order_index": 0, "message": {"role": "system", "content": "hi"}}, {"order_index": 1, "message": {"role": "user", "content": "x"}}]}]}`,
	}))
	st, out := gatewayReposCall(r, "GET", APIPrefix+"/gateway/prompts", "")
	if st != 200 || !strings.Contains(out, `"prompts":[{"id":"p1","name":"support"}]`) {
		t.Fatalf("prompts: %d %s", st, out)
	}
	st, out = gatewayReposCall(r, "GET", APIPrefix+"/gateway/prompts/p1/versions", "")
	if st != 200 || !strings.Contains(out, `"versions":[{"version":3,"commit_message":"first","created_at":"2026-11-01T00:00:00Z","messages":2,"model":"gpt-5"}]`) {
		t.Fatalf("versions: %d %s", st, out)
	}
}

func TestGatewayReposListSkills(t *testing.T) {
	r, _ := gatewayReposTestRouter(t, fakeBifrostRepos(map[string]string{
		"/api/skills":             `{"skills": [{"id": "s1", "name": "review", "description": "d", "latest_version": "4"}], "total": 1}`,
		"/api/skills/s1/versions": `{"versions": [{"id": "v", "skill_id": "s1", "version": "3", "skill_md_body": "abcd", "created_at": "2026-11-02T00:00:00Z", "files": []}]}`,
	}))
	st, out := gatewayReposCall(r, "GET", APIPrefix+"/gateway/skills", "")
	if st != 200 || !strings.Contains(out, `"skills":[{"id":"s1","name":"review","latest_version":"4"}]`) {
		t.Fatalf("skills: %d %s", st, out)
	}
	st, out = gatewayReposCall(r, "GET", APIPrefix+"/gateway/skills/s1/versions", "")
	if st != 200 || !strings.Contains(out, `"versions":[{"version":"3","created_at":"2026-11-02T00:00:00Z","size":4}]`) {
		t.Fatalf("versions: %d %s", st, out)
	}
}
