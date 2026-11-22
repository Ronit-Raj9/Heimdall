package connectlocal

import (
	"context"
	"encoding/json"
	"fmt"
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
)

var nonceRE = regexp.MustCompile(`HMD-[A-Z0-9]{5}`)

// fastAPIBot answers POST /chat with {"reply": ...} and demands "query",
// the way FastAPI and Pydantic answer a missing field.
func fastAPIBot(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != UserAgent && !strings.HasPrefix(r.Header.Get("User-Agent"), "heimdall-relay") {
			t.Errorf("probe without the heimdall user agent: %q", r.Header.Get("User-Agent"))
		}
		if r.URL.Path != "/chat" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer s3cret" {
			w.WriteHeader(401)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		q, _ := body["query"].(string)
		if q == "" {
			w.WriteHeader(422)
			_, _ = io.WriteString(w, `{"detail":[{"loc":["body","query"],"msg":"Field required","type":"missing"}]}`)
			return
		}
		reply := "I cannot help with that."
		if m := nonceRE.FindString(q); m != "" {
			reply = m
		}
		if s, _ := body["session_id"].(string); s != "" {
			reply += " (session " + s + ")"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"reply": reply, "echo": map[string]any{"q": q}})
	}))
}

func TestProbeFindsFieldFromValidationError(t *testing.T) {
	srv := fastAPIBot(t)
	defer srv.Close()
	res := Probe(context.Background(), ProbeOptions{URL: srv.URL, Headers: map[string]string{"Authorization": "Bearer ${BOT_TOKEN}"},
		Getenv: func(k string) string {
			if k == "BOT_TOKEN" {
				return "s3cret"
			}
			return ""
		}})
	if res.Mapping == nil {
		t.Fatalf("no mapping: %+v", res)
	}
	m := res.Mapping
	if m.URL != srv.URL+"/chat" || m.Kind != KindField || m.Answer != "reply" || m.History != "server" {
		t.Fatalf("mapping = %+v\n%s", m, strings.Join(res.Evidence, "\n"))
	}
	if !strings.Contains(string(m.Body), `"query":"{{input}}"`) || !strings.Contains(string(m.Body), SessionVar) {
		t.Fatalf("body = %s", m.Body)
	}
	if m.Headers["Authorization"] != "Bearer ${BOT_TOKEN}" {
		t.Fatalf("headers lost their env reference: %v", m.Headers)
	}
	joined := strings.Join(res.Evidence, "\n")
	if !strings.Contains(joined, "naming the field query") {
		t.Fatalf("evidence does not say why: %s", joined)
	}
}

func TestProbeAuthFailure(t *testing.T) {
	srv := fastAPIBot(t)
	defer srv.Close()
	res := Probe(context.Background(), ProbeOptions{URL: srv.URL + "/chat"})
	if res.Mapping != nil || res.Code != "auth" {
		t.Fatalf("res = %+v", res)
	}
}

func TestProbeOpenAICompatible(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = io.WriteString(w, `{"data":[{"id":"qwen3-8b"}]}`)
		case "/v1/chat/completions":
			var body struct {
				Model    string    `json:"model"`
				Messages []Message `json:"messages"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			last := body.Messages[len(body.Messages)-1].Content
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": nonceRE.FindString(last)}}},
				"usage": map[string]any{"prompt_tokens": 12, "completion_tokens": 3}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	res := Probe(context.Background(), ProbeOptions{URL: srv.URL})
	if res.Mapping == nil || res.Mapping.Kind != KindOpenAI || res.Mapping.Model != "qwen3-8b" || res.Mapping.Answer != "choices.0.message.content" {
		t.Fatalf("res = %+v", res)
	}
	// The relay forwarder sends the chat and reads usage.
	f := &Forwarder{Mapping: res.Mapping}
	msg := "second"
	turn := &PullTurn{ID: "t1", Payload: PullPayload{Kind: "conversation", Message: &msg,
		Messages: []Message{{Role: "user", Content: "first"}, {Role: "assistant", Content: "ok"}, {Role: "user", Content: "say HMD-ABCDE"}}}}
	ans, err := f.Forward(context.Background(), turn)
	if err != nil || ans["text"] != "HMD-ABCDE" {
		t.Fatalf("forward = %v, %v", ans, err)
	}
	if u, ok := ans["usage"].(map[string]int64); !ok || u["input_tokens"] != 12 {
		t.Fatalf("usage = %v", ans["usage"])
	}
}

func TestProbePlainTextAndSSE(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		text, _ := json.Marshal(body)
		code := nonceRE.FindString(string(text))
		switch r.URL.Path {
		case "/invoke":
			if _, ok := body["message"]; !ok {
				w.WriteHeader(400)
				return
			}
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, "Sure: "+code)
		case "/stream":
			if _, ok := body["messages"]; !ok {
				w.WriteHeader(400)
				_, _ = io.WriteString(w, `{"issues":[{"path":["messages"],"message":"Required"}]}`)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			for _, piece := range []string{code[:3], code[3:]} {
				fmt.Fprintf(w, "data: {\"delta\":%q}\n\n", piece)
			}
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	res := Probe(context.Background(), ProbeOptions{URL: srv.URL})
	if res.Mapping == nil || res.Mapping.URL != srv.URL+"/invoke" || res.Mapping.Answer != "" {
		t.Fatalf("plain = %+v", res)
	}
	res = Probe(context.Background(), ProbeOptions{URL: srv.URL + "/stream"})
	if res.Mapping == nil || !res.Mapping.Stream || res.Mapping.Answer != "delta" || res.Mapping.Kind != KindMessages {
		t.Fatalf("sse = %+v\n%v", res.Mapping, res.Evidence)
	}
}

func TestProbeFromCurlAndEcho(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input struct {
				Text string `json:"text"`
			} `json:"input"`
			User string `json:"user"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.Header.Get("X-Api-Key") != "k" {
			w.WriteHeader(403)
			return
		}
		// Echoes the input and answers in output.answer.
		_ = json.NewEncoder(w).Encode(map[string]any{"request": body.Input.Text, "output": map[string]any{"answer": nonceRE.FindString(body.Input.Text)}})
	}))
	defer srv.Close()
	c, err := ParseCurl(`curl -sS -X POST ` + srv.URL + `/v2/agent \
  -H 'X-Api-Key: k' -H "Content-Type: application/json" \
  --data-raw '{"user":"u1","input":{"text":"where is my order, please tell me now"}}'`)
	if err != nil {
		t.Fatal(err)
	}
	if c.Method != "POST" || c.URL != srv.URL+"/v2/agent" || c.Headers["X-Api-Key"] != "k" {
		t.Fatalf("curl = %+v", c)
	}
	if got := c.SecretHeaders(); len(got) != 1 || got[0] != "X-Api-Key" {
		t.Fatalf("secret headers = %v", got)
	}
	res := Probe(context.Background(), ProbeOptions{Curl: c})
	if res.Mapping == nil || res.Mapping.Answer != "output.answer" || res.Mapping.Kind != KindTemplate {
		t.Fatalf("curl probe = %+v", res)
	}
	if !strings.Contains(string(res.Mapping.Body), `"text":"{{input}}"`) || !strings.Contains(string(res.Mapping.Body), `"user":"u1"`) {
		t.Fatalf("template = %s", res.Mapping.Body)
	}
}

func TestFindStringAndLookup(t *testing.T) {
	var doc any
	_ = json.Unmarshal([]byte(`{"messages":[{"role":"user","content":"say HMD-X1"},{"role":"assistant","content":"HMD-X1"}]}`), &doc)
	p, ok := FindString(doc, "HMD-X1", "say HMD-X1")
	if !ok || p != "messages.-1.content" {
		t.Fatalf("path = %q %v", p, ok)
	}
	v, ok := Lookup(doc, p)
	if !ok || v != "HMD-X1" {
		t.Fatalf("lookup = %v", v)
	}
	if f := FieldFromError([]byte(`{"error":"question is required"}`)); f != "question" {
		t.Fatalf("field = %q", f)
	}
}

func TestSplitShell(t *testing.T) {
	got, err := SplitShell(`curl -d '{"a": "b c"}' -H "X: \"q\"" a\ b`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"curl", "-d", `{"a": "b c"}`, "-H", `X: "q"`, "a b"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("split = %q", got)
	}
	if _, err := SplitShell(`curl 'open`); err == nil {
		t.Fatal("unclosed quote accepted")
	}
}

// fakePull is a pull server with one queued turn per entry.
type fakePull struct {
	mu      sync.Mutex
	turns   []PullTurn
	answers map[string]map[string]any
	token   string
}

func (f *fakePull) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+f.token {
			w.WriteHeader(401)
			_, _ = io.WriteString(w, `{"error":{"message":"invalid or expired token"}}`)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.URL.Path == PullPrefix+"/next":
			var req struct {
				Agent string `json:"agent"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.Agent != "support-bot" {
				w.WriteHeader(404)
				return
			}
			if len(f.turns) == 0 {
				w.WriteHeader(204)
				return
			}
			tt := f.turns[0]
			f.turns = f.turns[1:]
			_ = json.NewEncoder(w).Encode(tt)
		case strings.HasPrefix(r.URL.Path, PullPrefix+"/turns/"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, PullPrefix+"/turns/"), "/answer")
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.answers[id] = body
			w.WriteHeader(204)
		default:
			http.NotFound(w, r)
		}
	})
}

func TestRelayForwardsTurns(t *testing.T) {
	bot := fastAPIBot(t)
	defer bot.Close()
	res := Probe(context.Background(), ProbeOptions{URL: bot.URL + "/chat", Headers: map[string]string{"Authorization": "Bearer s3cret"}})
	if res.Mapping == nil {
		t.Fatalf("probe: %+v", res)
	}
	msg := "tell me HMD-QQQQQ"
	fp := &fakePull{token: "hm_tok", answers: map[string]map[string]any{}, turns: []PullTurn{
		{ID: "t1", Agent: "support-bot", Payload: PullPayload{Kind: "task", Instruction: "code HMD-AAAAA please", SessionID: "s1"}},
		{ID: "t2", Agent: "support-bot", Payload: PullPayload{Kind: "conversation", Message: &msg, SessionID: "s1"}},
	}}
	hs := httptest.NewServer(fp.handler(t))
	defer hs.Close()
	fw := &Forwarder{Mapping: res.Mapping}
	r := &Relay{Pull: &PullClient{Base: hs.URL, Token: "hm_tok"}, Agent: "support-bot", Forward: fw.Forward, MaxTurns: 2, Wait: time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	n, err := r.Run(ctx)
	if err != nil || n != 2 {
		t.Fatalf("relay = %d, %v", n, err)
	}
	a1, _ := fp.answers["t1"]["answer"].(map[string]any)
	a2, _ := fp.answers["t2"]["answer"].(map[string]any)
	if a1["text"] != "HMD-AAAAA (session s1)" || a2["text"] != "HMD-QQQQQ (session s1)" {
		t.Fatalf("answers = %v", fp.answers)
	}

	// A bad token stops the relay with the server's message.
	r2 := &Relay{Pull: &PullClient{Base: hs.URL, Token: "wrong"}, Agent: "support-bot", Forward: fw.Forward, Wait: time.Second}
	if _, err := r2.Run(ctx); err == nil || !strings.Contains(err.Error(), "invalid or expired token") {
		t.Fatalf("bad token = %v", err)
	}
}

func TestDetectDir(t *testing.T) {
	dir := t.TempDir()
	write := func(p, s string) {
		full := filepath.Join(dir, p)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("pyproject.toml", "[project]\ndependencies = [\"langgraph>=0.4\", \"openai-agents\"]\n")
	write("langgraph.json", `{"graphs": {"agent": "./src/agent.py:graph"}}`)
	write("src/agent.py", "from langgraph.graph import StateGraph\ngraph = builder.compile()\n")
	write("bots/triage.py", "from agents import Agent, Runner\ntriage = Agent(name=\"triage\", instructions=\"...\")\n")
	write("node_modules/x/agent.py", "from agents import Agent\nhidden = Agent()\n")
	d, err := DetectDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	b := d.Best()
	if b == nil || b.Framework != "langgraph" || b.Object != "src/agent.py:graph" || b.Serve != "heimdall serve src/agent.py:graph" {
		t.Fatalf("best = %+v (all %+v)", b, d.Findings)
	}
	var objs []string
	for _, f := range d.Findings {
		objs = append(objs, f.Object)
	}
	joined := strings.Join(objs, ",")
	if !strings.Contains(joined, "bots/triage.py:triage") || strings.Contains(joined, "hidden") || strings.Count(joined, "src/agent.py:graph") != 1 {
		t.Fatalf("objects = %s", joined)
	}
	if strings.Join(d.Packages, ",") != "langgraph,openai-agents" {
		t.Fatalf("packages = %v", d.Packages)
	}
}

func TestProjectFileRoundTripAndSecrets(t *testing.T) {
	dir := t.TempDir()
	headers := map[string]string{"Authorization": "Bearer abcdefghijklmnopqrstuvwxyz", "Accept": "application/json"}
	env := MoveSecretsToEnv(headers, []string{"Authorization"})
	if env["HEIMDALL_AGENT_AUTHORIZATION"] != "Bearer abcdefghijklmnopqrstuvwxyz" || headers["Authorization"] != "${HEIMDALL_AGENT_AUTHORIZATION}" {
		t.Fatalf("moved = %v %v", env, headers)
	}
	m := &Mapping{Kind: KindField, Method: "POST", URL: "http://bot.internal:8080/chat", Headers: headers, Body: json.RawMessage(`{"query":"{{input}}"}`), Answer: "reply", History: "none"}
	pf := &ProjectFile{Server: "https://h.example", Agent: "support-bot", Framework: "http", Serve: &ServeSpec{Target: m.URL, Map: m}}
	p, err := WriteProject(dir, pf)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := ReadProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Agent != "support-bot" || got.Serve.Map.Answer != "reply" || string(got.Serve.Map.Body) != `{"query":"{{input}}"}` {
		t.Fatalf("read back = %+v %+v", got, got.Serve.Map)
	}
	raw, _ := os.ReadFile(p)
	if strings.Contains(string(raw), "abcdefghijklmnop") {
		t.Fatal("secret written to the project file")
	}
	m.Headers["Authorization"] = "Bearer abcdefghijklmnopqrstuvwxyz"
	if _, err := WriteProject(dir, pf); err == nil {
		t.Fatal("literal credential written")
	}
}

func TestCredentialsResolve(t *testing.T) {
	t.Setenv("HEIMDALL_CONFIG_DIR", t.TempDir())
	t.Setenv("HEIMDALL_TOKEN", "")
	t.Setenv("HEIMDALL_URL", "")
	c, _ := LoadCredentials()
	c.Default = "https://h.example"
	c.Servers["https://h.example"] = &ServerCred{Token: "hm_a", CreatedAt: time.Now()}
	p, err := c.Save()
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", st.Mode())
	}
	if s, tok, src := Resolve("", ""); s != "https://h.example" || tok != "hm_a" || src != "login" {
		t.Fatalf("resolve = %s %s %s", s, tok, src)
	}
	t.Setenv("HEIMDALL_TOKEN", "hm_env")
	if _, tok, src := Resolve("https://h.example/", ""); tok != "hm_env" || src != "env" {
		t.Fatalf("env = %s %s", tok, src)
	}
}
