package connect

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

var codeRe = regexp.MustCompile(`HMD-[A-Z0-9]{5}`)

func TestClassify(t *testing.T) {
	cases := map[string]Class{
		`curl -X POST https://bot.acme.com/chat -d '{"message":"hi"}'`:       ClassCurl,
		"https://agent.example.com/run":                                      ClassURL,
		"agent.example.com/run":                                              ClassURL,
		"https://github.com/acme/support-bot":                                ClassRepository,
		"git@github.com:acme/bot.git":                                        ClassRepository,
		"ghcr.io/acme/agent:1.2":                                             ClassImage,
		"acme/agent@sha256:" + strings.Repeat("a", 64):                       ClassImage,
		"arn:aws:bedrock-agentcore:us-east-1:123456789012:runtime/support-x": ClassPlatform,
		"wss://voice.example.com/socket":                                     ClassSocket,
		"+1 415 555 0100":                                                    ClassPhone,
		"sip:bot@example.com":                                                ClassPhone,
		"app.agent:support_bot":                                              ClassPullObject,
		"agent.py:graph":                                                     ClassPullObject,
		"":                                                                   ClassUnknown,
		"hello there":                                                        ClassUnknown,
	}
	for in, want := range cases {
		if got := Classify(in); got != want {
			t.Errorf("Classify(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestParseCurlAndCandidate(t *testing.T) {
	c, err := ParseCurl(`curl -X POST https://bot.acme.com/chat -H "X-API-Key: sk_live_123" -H 'Content-Type: application/json' \
  -d '{"message":"hi","user_id":"u1","session_id":"s1"}'`)
	if err != nil {
		t.Fatal(err)
	}
	if c.Method != "POST" || c.URL != "https://bot.acme.com/chat" || c.Headers["X-API-Key"] != "sk_live_123" {
		t.Fatalf("parsed %+v", c)
	}
	cand, secrets := curlCandidate(c)
	if cand.Params["auth_header"] != "X-API-Key" || cand.Params["auth_secret"] != "BOT_ACME_KEY" || secrets["BOT_ACME_KEY"] != "sk_live_123" {
		t.Fatalf("auth not moved into a secret: %+v %v", cand.Params, secrets)
	}
	if cand.Mapping == nil || !strings.Contains(cand.Mapping.Body, `"message":"{{instruction}}"`) || !strings.Contains(cand.Mapping.Body, `"session_id":"{{session_id}}"`) {
		t.Fatalf("body template %+v", cand.Mapping)
	}
	if strings.Contains(cand.Mapping.Body, "sk_live") {
		t.Fatal("the token leaked into the mapping")
	}
	if len(cand.Questions) != 1 || cand.Questions[0].Prefill != "sk_live_123" {
		t.Fatalf("questions %+v", cand.Questions)
	}
	if _, err := ParseCurl("curl -d @body.json https://x.example.com"); err == nil {
		t.Fatal("a body from a file should be refused")
	}
	b, err := ParseCurl(`curl https://api.example.com/v1/chat -u user:pw --json '{"messages":[{"role":"user","content":"hi"}]}'`)
	if err != nil {
		t.Fatal(err)
	}
	bc, bs := curlCandidate(b)
	if bc.Mapping.Headers["Authorization"] != "Basic {{secret.EXAMPLE_BASIC}}" || bs["EXAMPLE_BASIC"] == "" {
		t.Fatalf("basic auth %+v %v", bc.Mapping.Headers, bs)
	}
	if !strings.Contains(bc.Mapping.Body, `"messages":"{{messages}}"`) {
		t.Fatalf("messages template %s", bc.Mapping.Body)
	}
}

func TestPathAndLocate(t *testing.T) {
	var doc any
	_ = json.Unmarshal([]byte(`{"messages":[{"role":"user","content":"Reply HMD-AAAAA please"},{"role":"assistant","content":"HMD-AAAAA"}],"meta":{"id":"x"}}`), &doc)
	path, echo, _ := LocateNonce(doc, "HMD-AAAAA", "Reply HMD-AAAAA please")
	if path != "messages[-1].content" || echo {
		t.Fatalf("path %q echo %v", path, echo)
	}
	if txt, ok := PathText(doc, path); !ok || txt != "HMD-AAAAA" {
		t.Fatalf("eval %q %v", txt, ok)
	}
	var out any
	_ = json.Unmarshal([]byte(`{"output":[{"type":"reasoning"},{"type":"message","content":[{"text":"a"},{"text":"b"}]}]}`), &out)
	if txt, _ := PathText(out, "output[?(@.type=='message')].content[*].text"); txt != "a\nb" {
		t.Fatalf("filter path gave %q", txt)
	}
	_, echo, _ = LocateNonce(map[string]any{"echo": "Reply HMD-AAAAA please"}, "HMD-AAAAA", "Reply HMD-AAAAA please")
	if !echo {
		t.Fatal("echo not seen")
	}
}

// fakeAgent answers the nonce and keeps memory per session when server is
// set, or reads the conversation from messages when client is set.
type fakeAgent struct {
	mu      sync.Mutex
	mem     map[string]string
	global  string // leaks across sessions
	server  bool
	client  bool
	leak    bool
	field   string // the field it wants; "" takes Heimdall's own shape
	answer  func(text string) any
	posts   int
	version string
}

func (f *fakeAgent) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	f.mu.Lock()
	f.posts++
	f.mu.Unlock()
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	field := f.field
	if field == "" {
		field = "instruction"
	}
	instr, _ := body[field].(string)
	if instr == "" {
		w.WriteHeader(422)
		fmt.Fprintf(w, `{"detail":[{"loc":["body","%s"],"msg":"Field required","type":"missing"}]}`, field)
		return
	}
	sess, _ := body["session_id"].(string)
	reply := "OK"
	f.mu.Lock()
	if f.mem == nil {
		f.mem = map[string]string{}
	}
	switch {
	case strings.HasPrefix(instr, "Remember this code"):
		code := codeRe.FindString(instr)
		if f.server {
			f.mem[sess] = code
		}
		if f.leak {
			f.global = code
		}
	case strings.HasPrefix(instr, "What code"):
		reply = "I do not know"
		if c := f.mem[sess]; c != "" {
			reply = c
		} else if f.leak && f.global != "" {
			reply = f.global
		} else if f.client {
			if msgs, ok := body["messages"].([]any); ok {
				for _, m := range msgs {
					if mm, ok := m.(map[string]any); ok {
						if c := codeRe.FindString(fmt.Sprint(mm["content"])); c != "" {
							reply = c
						}
					}
				}
			}
		}
	default:
		reply = codeRe.FindString(instr)
	}
	f.mu.Unlock()
	if f.version != "" {
		w.Header().Set("X-Agent-Version", f.version)
	}
	w.Header().Set("Content-Type", "application/json")
	if f.answer != nil {
		_ = json.NewEncoder(w).Encode(f.answer(reply))
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"answer": reply})
}

func plainClient() *http.Client { return &http.Client{} }

func TestDetectOwnShape(t *testing.T) {
	fa := &fakeAgent{}
	srv := httptest.NewServer(http.HandlerFunc(fa.handle))
	defer srv.Close()
	prop := Detect(context.Background(), Input{Raw: srv.URL + "/run"}, Options{Client: plainClient()})
	b := prop.Best()
	if b == nil || !b.NonceFound || b.Mapping != nil || b.Template != TemplateHTTP {
		t.Fatalf("own shape not found: %+v failures %+v probes %+v", b, prop.Failures, prop.Probes)
	}
	if len(prop.Probes) > DefaultMaxRequests {
		t.Fatalf("%d probes", len(prop.Probes))
	}
}

func TestDetectFieldFrom422(t *testing.T) {
	fa := &fakeAgent{field: "query", answer: func(s string) any { return map[string]any{"reply": map[string]any{"text": s}, "id": "r1"} }}
	srv := httptest.NewServer(http.HandlerFunc(fa.handle))
	defer srv.Close()
	prop := Detect(context.Background(), Input{Raw: srv.URL + "/ask"}, Options{Client: plainClient()})
	b := prop.Best()
	if b == nil || !b.NonceFound || b.Mapping == nil || b.Mapping.Body != `{"query": "{{instruction}}"}` || b.Mapping.AnswerPath != "reply.text" {
		t.Fatalf("got %+v mapping %+v failures %+v", b, b.Mapping, prop.Failures)
	}
	if fa.posts > DefaultMaxPOSTs {
		t.Fatalf("%d POSTs", fa.posts)
	}
}

func TestDetectAuthMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(401)
	}))
	defer srv.Close()
	prop := Detect(context.Background(), Input{Raw: srv.URL + "/chat"}, Options{Client: plainClient()})
	if len(prop.Failures) == 0 || prop.Failures[0].Code != FailAuthMissing {
		t.Fatalf("failures %+v", prop.Failures)
	}
	if b := prop.Best(); b == nil || !hasQuestion(b, "auth_secret") || b.Params["auth_secret"] == "" {
		t.Fatalf("no secret question: %+v", b)
	}
}

func TestDetectCurlWithToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "sk_live_9" {
			w.WriteHeader(403)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":{"messages":[{"role":"assistant","text":"%s"}]}}`, codeRe.FindString(fmt.Sprint(body["message"])))
	}))
	defer srv.Close()
	raw := `curl ` + srv.URL + `/chat -H "X-API-Key: sk_live_9" -d '{"message":"hi","user_id":"u1"}'`
	prop := Detect(context.Background(), Input{Raw: raw}, Options{Client: plainClient()})
	b := prop.Best()
	if b == nil || !b.NonceFound || b.Mapping.AnswerPath != "data.messages[0].text" {
		t.Fatalf("got %+v failures %+v", b, prop.Failures)
	}
	js, _ := json.Marshal(prop)
	if strings.Contains(string(js), "sk_live_9") && !strings.Contains(string(js), `"prefill":"sk_live_9"`) {
		t.Fatal("the token appears outside the prefill")
	}
	if strings.Count(string(js), "sk_live_9") != 1 {
		t.Fatalf("token appears %d times", strings.Count(string(js), "sk_live_9"))
	}
}

func TestDetectA2AAndOpenAIAndLangGraph(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/agent-card.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"name":"Support","url":"http://x/a2a","protocolVersion":"0.3","capabilities":{"streaming":true},"skills":[{"tags":["support"]}]}`)
	})
	a2a := httptest.NewServer(mux)
	defer a2a.Close()
	prop := Detect(context.Background(), Input{Raw: a2a.URL}, Options{Client: plainClient()})
	b := prop.Best()
	if b == nil || b.Kind != KindA2A || b.Params["streaming"] != "true" || len(prop.Failures) != 1 || prop.Failures[0].Code != FailA2AVersion {
		t.Fatalf("a2a: %+v %+v", b, prop.Failures)
	}

	oai := http.NewServeMux()
	oai.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"object":"list","data":[{"id":"my-agent"}]}`)
	})
	oai.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model    string                     `json:"model"`
			Messages []struct{ Content string } `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":"%s"}}]}`, codeRe.FindString(body.Messages[len(body.Messages)-1].Content))
	})
	os := httptest.NewServer(oai)
	defer os.Close()
	prop = Detect(context.Background(), Input{Raw: os.URL + "/v1"}, Options{Client: plainClient()})
	b = prop.Best()
	if b == nil || b.Kind != KindOpenAI || !b.NonceFound || b.Params["model"] != "my-agent" || b.Template != TemplateOpenAI {
		t.Fatalf("openai: %+v %+v", b, prop.Failures)
	}

	lg := http.NewServeMux()
	lg.HandleFunc("/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"paths":{"/runs/wait":{"post":{}},"/assistants/search":{"post":{}}}}`)
	})
	lg.HandleFunc("/runs/wait", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		code := codeRe.FindString(fmt.Sprint(body["input"]))
		fmt.Fprintf(w, `{"messages":[{"type":"human","content":"x"},{"type":"ai","content":"%s"}]}`, code)
	})
	ls := httptest.NewServer(lg)
	defer ls.Close()
	prop = Detect(context.Background(), Input{Raw: ls.URL}, Options{Client: plainClient()})
	b = prop.Best()
	if b == nil || b.Preset != PresetLangGraph || !b.NonceFound || b.Mapping.AnswerPath != "messages[-1].content" {
		t.Fatalf("langgraph: %+v %+v", b, prop.Failures)
	}
}

func TestDetectSSEAndEcho(t *testing.T) {
	sse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		code := codeRe.FindString(string(raw))
		w.Header().Set("Content-Type", "text/event-stream")
		for _, piece := range []string{code[:3], code[3:]} {
			fmt.Fprintf(w, "data: {\"type\":\"delta\",\"delta\":{\"text\":\"%s\"}}\n\n", piece)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer sse.Close()
	prop := Detect(context.Background(), Input{Raw: sse.URL + "/stream"}, Options{Client: plainClient()})
	b := prop.Best()
	if b == nil || !b.NonceFound || b.Mapping.Stream != "sse" || b.Mapping.StreamPath != "delta.text" {
		t.Fatalf("sse: %+v %+v", b.Mapping, prop.Failures)
	}
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"you_sent":%s}`, raw)
	}))
	defer echo.Close()
	prop = Detect(context.Background(), Input{Raw: echo.URL + "/echo"}, Options{Client: plainClient()})
	if len(prop.Failures) == 0 || prop.Failures[len(prop.Failures)-1].Code != FailEcho {
		t.Fatalf("echo: %+v", prop.Failures)
	}
}

func TestDetectFingerprints(t *testing.T) {
	prop := Detect(context.Background(), Input{Raw: "arn:aws:bedrock-agentcore:us-east-1:123456789012:runtime/support-x"}, Options{})
	b := prop.Best()
	if b == nil || b.Auth == nil || b.Auth.Type != schemas.RemoteAuthSigV4 || b.Auth.Region != "us-east-1" || b.Auth.AccessKeySecret == "" {
		t.Fatalf("agentcore: %+v", b)
	}
	if len(prop.Probes) != 0 {
		t.Fatal("a platform ARN needs no probe")
	}
	prop = Detect(context.Background(), Input{Raw: "+14155550100"}, Options{})
	if prop.Best() != nil || !strings.Contains(prop.Candidates[0].Message, "Phone and SIP are not supported yet") {
		t.Fatalf("phone: %+v", prop.Candidates)
	}
	prop = Detect(context.Background(), Input{Platform: "agentforce", ID: "0XxHn000000x9F1KAI"}, Options{})
	if b := prop.Best(); b == nil || b.Mapping.Start == nil || !strings.Contains(b.Mapping.Start.URL, "0XxHn000000x9F1KAI") {
		t.Fatalf("agentforce: %+v", prop.Candidates)
	}
}

func remoteCfg(u string) *schemas.AgentConfig {
	return &schemas.AgentConfig{ID: "bot", Adapter: "remote_http", Remote: &schemas.RemoteAgent{URL: u}}
}

func TestVerifyServerHistoryAndLeak(t *testing.T) {
	fa := &fakeAgent{server: true, leak: true, version: "2.1.0"}
	srv := httptest.NewServer(http.HandlerFunc(fa.handle))
	defer srv.Close()
	var emitted int
	res := Verify(context.Background(), remoteCfg(srv.URL+"/run"), VerifyOptions{Client: plainClient(), Emit: func([]VerifyRow) { emitted++ }, RealTask: true})
	if !res.OK || res.Suggest.History != "server" || res.Version != "2.1.0" {
		t.Fatalf("result %+v", res)
	}
	if r := res.Row(RowIsolation); r.Status != StatusFail || r.Failure == nil || r.Failure.Code != FailMemoryLeak {
		t.Fatalf("isolation %+v", r)
	}
	if r := res.Row(RowRealTask); r == nil || r.Status != StatusPending {
		t.Fatalf("real task row %+v", r)
	}
	if emitted < 5 {
		t.Fatalf("emitted %d", emitted)
	}
}

func TestVerifyClientHistoryAndOneShot(t *testing.T) {
	fa := &fakeAgent{client: true}
	srv := httptest.NewServer(http.HandlerFunc(fa.handle))
	defer srv.Close()
	res := Verify(context.Background(), remoteCfg(srv.URL+"/run"), VerifyOptions{Client: plainClient()})
	if !res.OK || res.Suggest.History != "client" || res.Row(RowIsolation).Status != StatusPass {
		t.Fatalf("client: %+v", res.Rows)
	}
	one := httptest.NewServer(http.HandlerFunc((&fakeAgent{}).handle))
	defer one.Close()
	res = Verify(context.Background(), remoteCfg(one.URL+"/run"), VerifyOptions{Client: plainClient()})
	if res.Suggest.History != "none" || res.Row(RowState).Failure.Code != FailOneShot || *res.Suggest.Sessions {
		t.Fatalf("one shot: %+v", res.Rows)
	}
}

func TestVerifyFailures(t *testing.T) {
	res := Verify(context.Background(), remoteCfg("https://agent.invalid/run"), VerifyOptions{Client: plainClient(),
		Lookup: func(context.Context, string) ([]net.IPAddr, error) { return nil, &net.DNSError{IsNotFound: true} }})
	if r := res.Row(RowReach); res.OK || r.Status != StatusFail || r.Failure.Code != FailDNS || !strings.Contains(r.Failure.Message, "heimdall serve") {
		t.Fatalf("dns: %+v", r)
	}
	if res.Row(RowAnswer).Status != StatusSkip {
		t.Fatal("rows after a failed reach must be skipped")
	}
	res = Verify(context.Background(), remoteCfg("https://agent.example.com/run"), VerifyOptions{Client: plainClient(), Hosted: true,
		Lookup: func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("10.0.3.4")}}, nil
		}})
	if r := res.Row(RowReach); r.Failure == nil || r.Failure.Code != FailPrivateAddress || !strings.Contains(r.Failure.Message, "10.0.3.4") {
		t.Fatalf("private: %+v", r)
	}
	wrong := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		fmt.Fprint(w, "invalid api key")
	}))
	defer wrong.Close()
	cfg := remoteCfg(wrong.URL + "/run")
	cfg.Remote.AuthSecret = "BOT_TOKEN"
	res = Verify(context.Background(), cfg, VerifyOptions{Client: plainClient(), Secrets: map[string]string{"BOT_TOKEN": "tok-123456"}})
	if r := res.Row(RowAuth); r.Failure == nil || r.Failure.Code != FailAuthWrong || !strings.Contains(r.Failure.Message, "BOT_TOKEN") {
		t.Fatalf("auth wrong: %+v", r)
	}
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"hello"}`)
	}))
	defer other.Close()
	res = Verify(context.Background(), remoteCfg(other.URL), VerifyOptions{Client: plainClient()})
	if r := res.Row(RowAnswer); r.Failure == nil || r.Failure.Code != FailNoAnswer {
		t.Fatalf("no answer: %+v", r)
	}
}

func TestVerifyMappedAgent(t *testing.T) {
	fa := &fakeAgent{field: "query", server: true, answer: func(s string) any { return map[string]any{"out": []any{map[string]any{"t": s}}} }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// the mapped body names the session field session_id too
		fa.handle(w, r)
	}))
	defer srv.Close()
	cfg := remoteCfg(srv.URL)
	cfg.Remote.Mapping = &schemas.RemoteMapping{Body: `{"query": "{{instruction}}", "session_id": "{{session_id}}"}`, AnswerPath: "out[0].t"}
	res := Verify(context.Background(), cfg, VerifyOptions{Client: plainClient()})
	if !res.OK || res.Suggest.History != "server" {
		t.Fatalf("mapped: %+v", res.Rows)
	}
}

func TestFailureCatalog(t *testing.T) {
	if n := len(Catalog()); n != 24 {
		t.Fatalf("catalog has %d entries, want 24", n)
	}
	for _, e := range Catalog() {
		f := Lookup(e.Code, nil)
		if strings.ContainsAny(f.Message, "{}") || strings.ContainsRune(f.Message, 0x2014) {
			t.Errorf("%s: message %q", e.Code, f.Message)
		}
	}
	cases := map[string]string{
		"dial tcp: lookup agent.example.com: no such host":                FailDNS,
		"refused to connect to private or local address 10.0.0.5":         FailPrivateAddress,
		"x509: certificate is valid for *.internal.example, not bot.acme": FailTLS,
		"the agent answered HTTP 429: slow down":                          FailRateLimit,
		"the endpoint answered 401: unauthorized":                         FailAuthWrong,
		"the agent answered HTTP 503":                                     FailUpstream5xx,
		"context deadline exceeded (Client.Timeout exceeded)":             FailTimeout,
		`the endpoint's JSON has no "answer" field`:                       FailNoAnswer,
	}
	for in, want := range cases {
		f, ok := ExplainRunError(in)
		if !ok || f.Code != want {
			t.Errorf("ExplainRunError(%q) = %s, want %s", in, f.Code, want)
		}
	}
}

func TestPickFirstLook(t *testing.T) {
	infos := []BenchmarkInfo{
		{ID: "swe", Compatible: false, Tasks: 50},
		{ID: "tau2", Compatible: true, NeedsModel: true, Tasks: 40, EstimateS: 60, EstimateBasis: "time limit"},
		{ID: "gaia", Compatible: true, Tasks: 30, EstimateS: 120, EstimateBasis: "time limit"},
		{ID: "simpleqa", Compatible: true, Tasks: 3},
	}
	p, err := PickFirstLook(infos, false)
	if err != nil || p.Benchmark != "gaia" || p.Tasks != 5 || p.Concurrency != 5 || p.Trials != 1 {
		t.Fatalf("no key: %+v %v", p, err)
	}
	p, _ = PickFirstLook(infos, true)
	if p.Benchmark != "tau2" {
		t.Fatalf("with key: %+v", p)
	}
	if _, err := PickFirstLook([]BenchmarkInfo{{ID: "tau2", Compatible: true, NeedsModel: true, Tasks: 4}}, false); err == nil || !strings.Contains(err.Error(), "provider key") {
		t.Fatalf("judge only: %v", err)
	}
}
