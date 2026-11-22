package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// importRouter serves the import routes with every platform host pointed
// at fake.
func importRouter(t *testing.T, fake *httptest.Server, permissive bool) (*router.Router, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := projectfs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	var store projectfs.Store = st
	if permissive {
		store = rawAgentStore{Store: st, root: root}
	}
	h := newAgentImportHandler(OrgEnv{Mode: schemas.Mode("live"), Services: Services{Projects: store}})
	target, _ := url.Parse(fake.URL)
	h.client = func(bool, time.Duration) *http.Client { return fake.Client() }
	h.rewrite = func(u *url.URL) { u.Scheme, u.Host = target.Scheme, target.Host }
	if permissive {
		h.validate = func([]byte, string) error { return nil }
	}
	r := router.New()
	r.GET(APIPrefix+"/agents/platforms", h.platforms)
	r.POST(APIPrefix+"/agents/import-platform", h.importAgent)
	return r, root
}

// rawAgentStore writes agent files without the schema check, so the save
// path is tested on a server whose agent schema predates the
// voice_platform adapter and remote mapping (lanes Y10 and Y12).
type rawAgentStore struct {
	projectfs.Store
	root string
}

func (s rawAgentStore) Write(_ context.Context, a projectfs.Area, rel string, data []byte, _ projectfs.WriteOpts) (projectfs.Entry, error) {
	p := filepath.Join(s.root, "agents", rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return projectfs.Entry{}, err
	}
	return projectfs.Entry{}, os.WriteFile(p, data, 0o644)
}

func vapiFake(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "api.vapi.ai" || r.URL.Path != "/assistant/asst-1" {
			w.WriteHeader(404)
			return
		}
		if r.Header.Get("Authorization") != "Bearer vapi-private-123" {
			w.WriteHeader(401)
			io.WriteString(w, `{"message":"Invalid Key"}`)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"id": "asst-1", "name": "Support line", "firstMessage": "Hello.",
			"model": map[string]any{"provider": "openai", "model": "gpt-4.1", "messages": []any{map[string]any{"role": "system", "content": "Be kind."}}}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

var vapiImport = AgentImportRequest{ID: "support-line", Platform: "vapi", AgentID: "asst-1", Secrets: map[string]string{"api_key": "Z16_VAPI_KEY"}}

func TestAgentPlatformsList(t *testing.T) {
	r, _ := importRouter(t, vapiFake(t), false)
	code, body := abCall(r, "GET", APIPrefix+"/agents/platforms", nil)
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var out AgentPlatformList
	json.Unmarshal(body, &out)
	ids := []string{}
	for _, p := range out.Platforms {
		ids = append(ids, p.ID)
	}
	if strings.Join(ids, ",") != "vapi,retell,elevenlabs,agentcore,agentforce,copilot_studio,vertex,langgraph" {
		t.Fatalf("platforms %v", ids)
	}
}

func TestAgentImportDryRun(t *testing.T) {
	t.Setenv("Z16_VAPI_KEY", "vapi-private-123")
	r, root := importRouter(t, vapiFake(t), false)
	req := vapiImport
	req.DryRun = true
	code, body := abCall(r, "POST", APIPrefix+"/agents/import-platform", req)
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var out AgentImportResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if out.Import == nil || out.Import.Definition.Name != "Support line" || out.Import.Definition.Prompt != "Be kind." {
		t.Fatalf("import %+v", out.Import)
	}
	if !strings.Contains(out.YAML, "adapter: voice_platform") || !strings.Contains(out.YAML, "api_key_secret: Z16_VAPI_KEY") || strings.Contains(out.YAML, "vapi-private-123") {
		t.Fatalf("yaml %s", out.YAML)
	}
	if strings.Contains(string(body), "vapi-private-123") {
		t.Fatal("response leaks the key")
	}
	if _, err := os.Stat(filepath.Join(root, "agents", "support-line.yaml")); !os.IsNotExist(err) {
		t.Fatal("dry run wrote the agent")
	}
	// Until the voice_platform adapter is in this server's agent schema
	// (lane Y12), the dry run says the save would fail; after the merge it
	// validates.
	if out.ValidationError != "" && !strings.Contains(out.ValidationError, "voice_platform") {
		t.Fatalf("validation error %q", out.ValidationError)
	}
}

func TestAgentImportCreatesAgentAndDefinition(t *testing.T) {
	t.Setenv("Z16_VAPI_KEY", "vapi-private-123")
	r, root := importRouter(t, vapiFake(t), true)
	code, body := abCall(r, "POST", APIPrefix+"/agents/import-platform", vapiImport)
	if code != fasthttp.StatusCreated {
		t.Fatalf("%d %s", code, body)
	}
	y, err := os.ReadFile(filepath.Join(root, "agents", "support-line.yaml"))
	if err != nil || !strings.HasPrefix(string(y), "# Imported from Vapi asst-1 (Support line)") {
		t.Fatalf("agent yaml %s %v", y, err)
	}
	side, err := os.ReadFile(filepath.Join(root, "agents", "imported", "support-line.json"))
	if err != nil || !strings.Contains(string(side), `"first_message": "Hello."`) {
		t.Fatalf("sidecar %s %v", side, err)
	}
	if code, body := abCall(r, "POST", APIPrefix+"/agents/import-platform", vapiImport); code != fasthttp.StatusConflict {
		t.Fatalf("second import: %d %s", code, body)
	}
}

func TestAgentImportRefusesBeforeCalling(t *testing.T) {
	r, _ := importRouter(t, vapiFake(t), true)
	for _, tc := range []struct {
		req  AgentImportRequest
		want string
	}{
		{AgentImportRequest{ID: "Bad Name", Platform: "vapi", AgentID: "a"}, "agent name"},
		{AgentImportRequest{ID: "platforms", Platform: "vapi", AgentID: "a"}, "reserved"},
		{AgentImportRequest{ID: "x", Platform: "openai_agent_builder", AgentID: "a"}, "unknown platform"},
		{AgentImportRequest{ID: "x", Platform: "vapi", AgentID: "a"}, "needs the Vapi private key"},
		{AgentImportRequest{ID: "x", Platform: "vapi", AgentID: "a", Secrets: map[string]string{"api_key": "Z16_UNSET_SECRET"}}, "secret Z16_UNSET_SECRET is not set"},
	} {
		code, body := abCall(r, "POST", APIPrefix+"/agents/import-platform", tc.req)
		if code != 400 || !strings.Contains(string(body), tc.want) {
			t.Errorf("%+v: %d %s, want 400 %q", tc.req, code, body, tc.want)
		}
	}
}

func TestAgentImportRefusedKey(t *testing.T) {
	t.Setenv("Z16_VAPI_KEY", "wrong-key-0000")
	r, root := importRouter(t, vapiFake(t), true)
	code, body := abCall(r, "POST", APIPrefix+"/agents/import-platform", vapiImport)
	if code != fasthttp.StatusUnprocessableEntity || !strings.Contains(string(body), "401") || strings.Contains(string(body), "wrong-key-0000") {
		t.Fatalf("%d %s", code, body)
	}
	if _, err := os.Stat(filepath.Join(root, "agents", "support-line.yaml")); !os.IsNotExist(err) {
		t.Fatal("a failed import wrote the agent")
	}
}

func TestAgentImportLangGraphDryRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Host + r.URL.Path {
		case "dep.us.langgraph.app/assistants/3f1c0a9e-2b7d-4c1e-9a43-0d6c7e2f9b11":
			json.NewEncoder(w).Encode(map[string]any{"assistant_id": "3f1c0a9e-2b7d-4c1e-9a43-0d6c7e2f9b11", "graph_id": "agent", "name": "Research agent"})
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	r, _ := importRouter(t, srv, false)
	code, body := abCall(r, "POST", APIPrefix+"/agents/import-platform", AgentImportRequest{ID: "research", Platform: "langgraph",
		AgentID: "3f1c0a9e-2b7d-4c1e-9a43-0d6c7e2f9b11", Inputs: map[string]string{"deployment_url": "https://dep.us.langgraph.app"}, DryRun: true})
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var out AgentImportResponse
	json.Unmarshal(body, &out)
	if !strings.Contains(out.YAML, "url: https://dep.us.langgraph.app/runs/wait") || !strings.Contains(out.YAML, "preset: langgraph") || out.Import.Preset != "langgraph" {
		t.Fatalf("yaml %s", out.YAML)
	}
	// Until remote.mapping is in this server's agent schema (lane Y10) the
	// dry run reports it; after the merge the config validates.
	if out.ValidationError != "" && !strings.Contains(out.ValidationError, "mapping") {
		t.Fatalf("validation error %q", out.ValidationError)
	}
}
