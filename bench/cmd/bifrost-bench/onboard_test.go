package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/connectlocal"
)

// fakeOnboardHeimdall is a hosted Heimdall with the routes connect, login,
// first look and serve call. detect false answers 404 for the Z01 routes,
// like a server without them; preset false refuses the first_look preset.
type fakeOnboardHeimdall struct {
	mu       sync.Mutex
	detect   bool
	preset   bool
	polls    int
	agents   map[string]string
	evals    []map[string]any
	requests []string
	turns    []connectlocal.PullTurn
	answers  map[string]map[string]any
	serving  bool
}

func (f *fakeOnboardHeimdall) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := r.Method + " " + r.URL.Path
	f.requests = append(f.requests, key)
	body, _ := io.ReadAll(r.Body)
	send := func(status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	if !strings.HasPrefix(r.URL.Path, "/api/bench/auth/") && r.Header.Get("Authorization") != "Bearer hm_good" {
		send(401, map[string]any{"error": map[string]any{"message": "invalid or expired token", "type": "unauthorized"}})
		return
	}
	switch {
	case key == "GET /api/bench/auth/config":
		send(200, map[string]any{"auth_mode": "hosted"})
	case key == "POST /api/bench/auth/device/code":
		send(200, map[string]any{"device_code": "dev1", "user_code": "BCDF-GHJK", "verification_uri": "https://h/device",
			"verification_uri_complete": "https://h/device?code=BCDF-GHJK", "expires_in": 600, "interval": 1})
	case key == "POST /api/bench/auth/device/token":
		f.polls++
		if f.polls < 2 {
			send(400, map[string]any{"error": "authorization_pending"})
			return
		}
		send(200, map[string]any{"access_token": "hm_good", "token_type": "bearer", "expires_in": 3600, "org_id": "org_1", "role": "owner", "scopes": []string{"evaluate"}})
	case key == "GET /api/bench/orgs":
		send(200, map[string]any{"orgs": []any{map[string]any{"id": "org_1", "slug": "acme", "name": "Acme", "role": "owner"}}})
	case key == "POST /api/v1/connect/detect":
		if !f.detect {
			send(404, map[string]any{"error": map[string]any{"message": "no route", "type": "not_found"}})
			return
		}
		send(200, map[string]any{"kind": "openai-compatible", "summary": "OpenAI-compatible server", "yaml": "id: remote-bot\nadapter: remote_http\n",
			"secrets": []any{map[string]any{"name": "BOT_KEY"}}})
	case key == "POST /api/v1/agents":
		var req struct{ YAML string }
		_ = json.Unmarshal(body, &req)
		id := agentIDOfYAML(req.YAML)
		if f.agents == nil {
			f.agents = map[string]string{}
		}
		if _, ok := f.agents[id]; ok {
			send(409, map[string]any{"error": map[string]any{"message": "taken"}})
			return
		}
		f.agents[id] = req.YAML
		send(200, map[string]any{"id": id})
	case strings.HasPrefix(key, "POST /api/v1/agents/") && strings.HasSuffix(key, "/verify"):
		send(404, map[string]any{"error": map[string]any{"message": "no route", "type": "not_found"}})
	case key == "GET /api/v1/pull/agents":
		send(200, map[string]any{"agents": []any{map[string]any{"name": "support-bot", "connected": f.serving}}})
	case key == "POST /api/v1/pull/next":
		f.serving = true
		if len(f.turns) == 0 {
			w.WriteHeader(204)
			return
		}
		t := f.turns[0]
		f.turns = f.turns[1:]
		send(200, t)
	case strings.HasPrefix(key, "POST /api/v1/pull/turns/"):
		var a map[string]any
		_ = json.Unmarshal(body, &a)
		if f.answers == nil {
			f.answers = map[string]map[string]any{}
		}
		f.answers[strings.Split(r.URL.Path, "/")[5]] = a
		w.WriteHeader(204)
	case strings.HasSuffix(key, "/compatibility"):
		send(200, map[string]any{"benchmarks": []any{map[string]any{"id": "slow-suite", "ok": true}, map[string]any{"id": "tau2-mini", "ok": true}, map[string]any{"id": "no", "ok": false}}})
	case key == "POST /api/v1/evaluations/estimate":
		send(200, map[string]any{"per_benchmark": []any{
			map[string]any{"benchmark": "slow-suite", "est_minutes": map[string]any{"low": 20.0, "high": 40.0, "known": true}, "est_cost_usd": map[string]any{"low": 1.0, "high": 2.0, "known": true}},
			map[string]any{"benchmark": "tau2-mini", "est_minutes": map[string]any{"low": 2.0, "high": 3.0, "known": true}, "est_cost_usd": map[string]any{"low": 0.02, "high": 0.05, "known": true}},
		}})
	case key == "POST /api/v1/evaluations":
		var spec map[string]any
		_ = json.Unmarshal(body, &spec)
		if _, ok := spec["preset"]; ok && !f.preset {
			send(400, map[string]any{"error": map[string]any{"message": `parse request body: json: unknown field "preset"`, "type": "bad_request"}})
			return
		}
		f.evals = append(f.evals, spec)
		send(202, map[string]any{"id": "ev_9", "status": "queued"})
	case key == "GET /api/v1/evaluations/ev_9":
		send(200, map[string]any{"id": "ev_9", "status": "done"})
	case key == "GET /api/v1/evaluations/ev_9/report":
		send(200, map[string]any{"mode": "live", "arms": []any{map[string]any{"label": "A", "score": map[string]any{"value": 0.6, "low": 0.2, "high": 0.9}}}})
	default:
		send(404, map[string]any{"error": map[string]any{"message": "no route " + key, "type": "not_found"}})
	}
}

func TestLoginDeviceFlowStoresToken(t *testing.T) {
	t.Setenv("HEIMDALL_CONFIG_DIR", t.TempDir())
	fake := &fakeOnboardHeimdall{}
	hs := httptest.NewServer(fake)
	defer hs.Close()
	var out strings.Builder
	res, err := doLogin(context.Background(), loginOptions{server: hs.URL, scope: "evaluate", out: &out, poll: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if res.Org != "acme" || res.Role != "owner" || !strings.Contains(out.String(), "BCDF-GHJK") {
		t.Fatalf("login = %+v\n%s", res, out.String())
	}
	t.Setenv("HEIMDALL_TOKEN", "")
	t.Setenv("HEIMDALL_URL", "")
	srv, tok, src := connectlocal.Resolve("", "")
	if srv != hs.URL || tok != "hm_good" || src != "login" {
		t.Fatalf("stored = %s %s %s", srv, tok, src)
	}
	raw, _ := os.ReadFile(res.Path)
	if st, _ := os.Stat(res.Path); st.Mode().Perm() != 0o600 || !strings.Contains(string(raw), "hm_good") {
		t.Fatalf("credentials file %v", st.Mode())
	}
	// -with-token checks the pasted token.
	if _, err := doLogin(context.Background(), loginOptions{server: hs.URL, withToken: true, stdin: strings.NewReader("hm_bad\n"), out: io.Discard}); err == nil {
		t.Fatal("bad pasted token accepted")
	}
}

var codeRE = regexp.MustCompile(`HMD-[A-Z0-9]{5}`)

func TestConnectURLRelaysWhenServerHasNoDetection(t *testing.T) {
	bot := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("X-Api-Key") != "k-123" {
			w.WriteHeader(401)
			return
		}
		var b map[string]string
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b["message"] == "" {
			w.WriteHeader(422)
			return
		}
		ans := codeRE.FindString(b["message"])
		if ans == "" {
			ans = "echo: " + b["message"]
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"answer": ans})
	}))
	defer bot.Close()
	fake := &fakeOnboardHeimdall{}
	hs := httptest.NewServer(fake)
	defer hs.Close()
	api := newOnboardAPI(hs.URL, "hm_good")
	ctx := context.Background()
	dir := t.TempDir()
	prop, err := proposeConnect(ctx, api, "curl "+bot.URL+"/chat -H 'X-Api-Key: k-123' -d '{\"message\":\"hi\"}'", dir, map[string]string{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if prop.Where != "local" || prop.Serve == nil || prop.Serve.Map == nil || prop.Serve.Map.Answer != "answer" {
		t.Fatalf("proposal = %+v", prop)
	}
	if prop.Serve.Map.Headers["X-Api-Key"] != "${HEIMDALL_AGENT_X_API_KEY}" || prop.secretValues["HEIMDALL_AGENT_X_API_KEY"] != "k-123" {
		t.Fatalf("secret not moved to the environment: %v", prop.Serve.Map.Headers)
	}
	prop.rename("support-bot")
	res, err := applyConnect(ctx, api, prop, dir, hs.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fake.agents["support-bot"], "adapter: pull") {
		t.Fatalf("registered = %v", fake.agents)
	}
	raw, _ := os.ReadFile(res.ProjectFile)
	if strings.Contains(string(raw), "k-123") || !strings.Contains(string(raw), "agent: support-bot") {
		t.Fatalf("project file:\n%s", raw)
	}
	// Second connect keeps the existing agent.
	if again, err := applyConnect(ctx, api, prop, dir, hs.URL); err != nil || !again.Existed {
		t.Fatalf("again = %+v %v", again, err)
	}

	// heimdall serve . relays a turn with the secret from the environment.
	t.Setenv("HEIMDALL_AGENT_X_API_KEY", "k-123")
	fake.mu.Lock()
	fake.turns = []connectlocal.PullTurn{{ID: "turn1", Agent: "support-bot", Payload: connectlocal.PullPayload{Kind: "task", Instruction: "say HMD-ZZZZZ"}}}
	fake.mu.Unlock()
	plan, err := resolveServePlan(ctx, agentServeOptions{target: dir, maxTurns: 1}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if plan.agent != "support-bot" || plan.mapping == nil {
		t.Fatalf("plan = %+v", plan)
	}
	plan.dir = dir
	if n, err := plan.run(ctx, hs.URL, "hm_good", io.Discard); err != nil || n != 1 {
		t.Fatalf("serve = %d %v", n, err)
	}
	if a := fake.answers["turn1"]["answer"].(map[string]any); a["text"] != "HMD-ZZZZZ" {
		t.Fatalf("answer = %v", fake.answers)
	}
	ver, err := api.verify(ctx, "support-bot", true)
	if err != nil || !ver.OK || ver.Source != "pull" {
		t.Fatalf("verify = %+v %v", ver, err)
	}
}

func TestConnectUsesServerDetection(t *testing.T) {
	fake := &fakeOnboardHeimdall{detect: true}
	hs := httptest.NewServer(fake)
	defer hs.Close()
	prop, err := proposeConnect(context.Background(), newOnboardAPI(hs.URL, "hm_good"), "https://bot.example.com", t.TempDir(), map[string]string{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if prop.Where != "heimdall" || prop.Agent != "remote-bot" || prop.Serve != nil || len(prop.Secrets) != 1 {
		t.Fatalf("proposal = %+v", prop)
	}
}

func TestConnectRepoFindsObject(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("crewai==0.80\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "crew.py"), []byte("from crewai import Crew, Agent\nsupport = Crew(agents=[], tasks=[])\n"), 0o644)
	prop, err := proposeConnect(context.Background(), newOnboardAPI("http://unused", "x"), "", dir, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if prop.Framework != "crewai" || prop.Serve == nil || prop.Serve.Target != "crew.py:support" || !strings.Contains(prop.Config, "adapter: pull") {
		t.Fatalf("proposal = %+v", prop)
	}
	py, args := (&servePlan{object: "crew.py:support", agent: "x", concurrency: 2}).pythonCommand()
	if py != "python3" || strings.Join(args, " ") != "-m heimdall.cli serve crew.py:support --agent x --concurrency 2" {
		t.Fatalf("python command = %s %v", py, args)
	}
}

func TestFirstLookPicksFastestAndFallsBackFromPreset(t *testing.T) {
	fake := &fakeOnboardHeimdall{}
	hs := httptest.NewServer(fake)
	defer hs.Close()
	api := newOnboardAPI(hs.URL, "hm_good")
	ctx := context.Background()
	plan, err := api.planFirstLook(ctx, "support-bot", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Benchmark != "tau2-mini" || plan.Tasks != 5 || plan.Trials != 1 || plan.Considered != 2 {
		t.Fatalf("plan = %+v", plan)
	}
	ev, err := api.startFirstLook(ctx, plan, true)
	if err != nil || ev["id"] != "ev_9" {
		t.Fatalf("start = %v %v", ev, err)
	}
	if len(fake.evals) != 1 || fake.evals[0]["tasks_per_benchmark"].(float64) != 5 || fake.evals[0]["trials"].(float64) != 1 {
		t.Fatalf("evaluation spec = %v", fake.evals)
	}
	// A server with the preset gets it.
	fake.preset = true
	if _, err := api.startFirstLook(ctx, plan, true); err != nil || fake.evals[1]["preset"] != "first_look" {
		t.Fatalf("preset = %v %v", fake.evals, err)
	}
	var out strings.Builder
	result := map[string]any{}
	if code := runFirstLookFlow(ctx, api, hs.URL, firstLookOptions{agent: "support-bot", yes: true, wait: true}, &out, result); code != exitOK {
		t.Fatalf("flow = %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), hs.URL+"/evaluations/ev_9") || !strings.Contains(out.String(), "score 0.6") {
		t.Fatalf("flow output:\n%s", out.String())
	}
}

func TestMCPConnectAndFirstLookTools(t *testing.T) {
	fake := &fakeOnboardHeimdall{}
	hs := httptest.NewServer(fake)
	defer hs.Close()
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "langgraph.json"), []byte(`{"graphs":{"agent":"./app/graph.py:graph"}}`), 0o644)
	srv := &mcpServer{api: &heimdallAPI{base: hs.URL, token: "hm_good", client: &http.Client{Timeout: 5 * time.Second}}, version: "test"}
	ctx := context.Background()
	args, _ := json.Marshal(map[string]any{"path": dir, "name": "support-bot", "register": true})
	got, err := mcpToolTable["connect_agent"].run(ctx, srv, args)
	if err != nil {
		t.Fatal(err)
	}
	m := got.(map[string]any)
	if m["registered"] != true || !strings.Contains(fake.agents["support-bot"], "adapter: pull") {
		t.Fatalf("connect_agent = %v", m)
	}
	if _, err := os.Stat(filepath.Join(dir, connectlocal.ProjectPath)); err != nil {
		t.Fatal(err)
	}
	// A literal secret is refused before anything is sent.
	args, _ = json.Marshal(map[string]any{"target": "https://bot.example.com", "headers": map[string]string{"Authorization": "Bearer sk-abcdefghijklmnopqrstuvwxyz"}})
	if _, err := mcpToolTable["connect_agent"].run(ctx, srv, args); err == nil || !strings.Contains(err.Error(), "literal secret") {
		t.Fatalf("literal secret = %v", err)
	}
	// first_look without confirm only plans.
	got, err = mcpToolTable["first_look"].run(ctx, srv, json.RawMessage(`{"agent":"support-bot"}`))
	if err != nil || got.(map[string]any)["started"] != false || len(fake.evals) != 0 {
		t.Fatalf("first_look plan = %v %v", got, err)
	}
	got, err = mcpToolTable["first_look"].run(ctx, srv, json.RawMessage(`{"agent":"support-bot","confirm":true}`))
	if err != nil || got.(map[string]any)["report_link"] != hs.URL+"/evaluations/ev_9" {
		t.Fatalf("first_look start = %v %v", got, err)
	}
}

func TestAgentServeTargetKeepsWebServer(t *testing.T) {
	for args, want := range map[string]bool{"": false, "-config x.yaml": false, "app.py:agent": true, "http://bot:8080": true, ".": true} {
		if got := agentServeTarget(strings.Fields(args)); got != want {
			t.Errorf("agentServeTarget(%q) = %v", args, got)
		}
	}
}
