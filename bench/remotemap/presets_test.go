package remotemap

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// fakePlatform is one TLS server that plays an agent platform (and its
// token endpoint). handle answers each request; seen records them.
type fakePlatform struct {
	t      *testing.T
	srv    *httptest.Server
	mu     sync.Mutex
	seen   []string
	handle func(w http.ResponseWriter, r *http.Request, body map[string]any, raw []byte)
}

func newFake(t *testing.T, handle func(w http.ResponseWriter, r *http.Request, body map[string]any, raw []byte)) *fakePlatform {
	f := &fakePlatform{t: t, handle: handle}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(raw))
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.seen = append(f.seen, r.Method+" "+r.URL.Path)
		f.mu.Unlock()
		f.handle(w, r, body, raw)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakePlatform) host() string { return strings.TrimPrefix(f.srv.URL, "https://") }

func (f *fakePlatform) caller(auth *Authorizer, token, header string) *Caller {
	return &Caller{Client: func(*tls.Config) *http.Client { return f.srv.Client() }, Auth: auth, Token: token, AuthHeader: header, Sleep: noSleep}
}

// rehost points every fixed platform URL of a mapping at the fake.
func rehost(t *testing.T, m *schemas.RemoteMapping, from, to string) *schemas.RemoteMapping {
	b, _ := json.Marshal(m)
	var out schemas.RemoteMapping
	if err := json.Unmarshal([]byte(strings.ReplaceAll(string(b), from, to)), &out); err != nil {
		t.Fatal(err)
	}
	return &out
}

func fill(t *testing.T, id, url string, inputs map[string]string) (string, *schemas.RemoteMapping, *schemas.RemoteAuth) {
	t.Helper()
	p, ok := FindPreset(id)
	if !ok {
		t.Fatalf("no preset %s", id)
	}
	u, m, a, err := p.Fill(url, inputs)
	if err != nil {
		t.Fatal(err)
	}
	return u, m, a
}

func vars() *Vars {
	return &Vars{Instruction: "Reply READY", SessionID: "sess-1", SessionIndex: 1, Output: "answer", NewID: func() string { return "uuid-1" }}
}

func call(t *testing.T, c *Caller, r *schemas.RemoteAgent) *Exchange {
	t.Helper()
	if err := Check(r); err != nil {
		t.Fatalf("check: %v", err)
	}
	ex, err := c.Call(context.Background(), r, vars())
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !ex.Mapped || ex.Answer != "READY" {
		t.Fatalf("answer %q mapped %v raw %s", ex.Answer, ex.Mapped, ex.Raw)
	}
	return ex
}

func TestPresetLangGraph(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, r *http.Request, body map[string]any, _ []byte) {
		msgs := body["input"].(map[string]any)["messages"].([]any)
		if r.URL.Path != "/runs/wait" || body["assistant_id"] != "agent" || msgs[0].(map[string]any)["content"] != "Reply READY" || r.Header.Get("x-api-key") != "lsv2" {
			w.WriteHeader(400)
			return
		}
		_, _ = w.Write([]byte(`{"messages":[{"type":"human","content":"Reply READY"},{"type":"ai","content":"READY"}]}`))
	})
	u, m, _ := fill(t, "langgraph", f.srv.URL+"/runs/wait", nil)
	call(t, f.caller(nil, "lsv2", "x-api-key"), &schemas.RemoteAgent{URL: u, Mapping: m})
}

func TestPresetADKWithGoogleIDToken(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	var f *fakePlatform
	f = newFake(t, func(w http.ResponseWriter, r *http.Request, body map[string]any, raw []byte) {
		switch {
		case r.URL.Path == "/token":
			_ = r.ParseForm()
			assertion := r.PostForm.Get("assertion")
			parts := strings.Split(assertion, ".")
			if len(parts) != 3 || r.PostForm.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
				w.WriteHeader(400)
				return
			}
			sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
			sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
			if rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], sig) != nil {
				w.WriteHeader(401)
				return
			}
			cb, _ := base64.RawURLEncoding.DecodeString(parts[1])
			var claims map[string]any
			_ = json.Unmarshal(cb, &claims)
			if claims["target_audience"] != "https://my-agent.run.app" || claims["iss"] != "sa@proj.iam.gserviceaccount.com" {
				w.WriteHeader(403)
				return
			}
			_, _ = w.Write([]byte(`{"id_token":"gid-1","expires_in":3600}`))
		case r.Header.Get("Authorization") != "Bearer gid-1":
			w.WriteHeader(401)
		case r.URL.Path == "/apps/support/users/heimdall/sessions/sess-1":
			_, _ = w.Write([]byte(`{"id":"sess-1"}`))
		case r.URL.Path == "/run":
			nm := body["new_message"].(map[string]any)
			if body["session_id"] != "sess-1" || nm["parts"].([]any)[0].(map[string]any)["text"] != "Reply READY" {
				w.WriteHeader(400)
				return
			}
			_, _ = w.Write([]byte(`[{"content":{"role":"model","parts":[{"functionCall":{}}]}},{"content":{"role":"model","parts":[{"text":"READY"}]}}]`))
		default:
			w.WriteHeader(404)
		}
	})
	u, m, a := fill(t, "adk", f.srv.URL+"/run", map[string]string{"app_name": "support"})
	sa, _ := json.Marshal(map[string]string{"type": "service_account", "client_email": "sa@proj.iam.gserviceaccount.com",
		"private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))})
	a.ServiceAccountSecret, a.Audience, a.TokenURL = "GSA", "https://my-agent.run.app", f.srv.URL+"/token"
	auth, err := NewAuthorizer(a, map[string]string{"GSA": string(sa)}, f.srv.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	call(t, f.caller(auth, "", ""), &schemas.RemoteAgent{URL: u, Mapping: m, Auth: a})
	if auth.Fetches != 1 {
		t.Fatalf("token fetched %d times for two requests, want 1 (cached)", auth.Fetches)
	}
}

func TestPresetMastra(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, r *http.Request, body map[string]any, _ []byte) {
		if body["threadId"] != "sess-1" || body["messages"].([]any)[0].(map[string]any)["content"] != "Reply READY" {
			w.WriteHeader(400)
			return
		}
		_, _ = w.Write([]byte(`{"text":"READY","usage":{"totalTokens":4}}`))
	})
	u, m, _ := fill(t, "mastra", f.srv.URL+"/api/agents/support/generate", nil)
	call(t, f.caller(nil, "", ""), &schemas.RemoteAgent{URL: u, Mapping: m})
}

func TestPresetN8N(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, r *http.Request, body map[string]any, _ []byte) {
		if body["chatInput"] != "Reply READY" || body["sessionId"] != "sess-1" || r.Header.Get("Authorization") != "Bearer n8n-tok" {
			w.WriteHeader(400)
			return
		}
		_, _ = w.Write([]byte(`{"output":"READY"}`))
	})
	u, m, _ := fill(t, "n8n", f.srv.URL+"/webhook/abc", nil)
	call(t, f.caller(nil, "n8n-tok", ""), &schemas.RemoteAgent{URL: u, Mapping: m})
}

func TestPresetOpenAIResponsesStream(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, r *http.Request, body map[string]any, _ []byte) {
		if body["model"] != "gpt-5-mini" || body["input"] != "Reply READY" || body["stream"] != true {
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, ev := range []string{`{"type":"response.created"}`, `{"type":"response.output_text.delta","delta":"REA"}`, `{"type":"response.output_text.delta","delta":"DY"}`, `{"type":"response.completed"}`} {
			_, _ = io.WriteString(w, "event: x\ndata: "+ev+"\n\n")
			w.(http.Flusher).Flush()
		}
	})
	u, m, _ := fill(t, "openai_responses", f.srv.URL+"/v1/responses", map[string]string{"model": "gpt-5-mini"})
	if ex := call(t, f.caller(nil, "sk-test", ""), &schemas.RemoteAgent{URL: u, Mapping: m}); ex.Events != 4 {
		t.Fatalf("events %d", ex.Events)
	}
}

func TestPresetBedrockSigV4(t *testing.T) {
	keys := SigV4Keys{AccessKey: "AKIATEST", SecretKey: "secret/key", Region: "eu-west-1", Service: "bedrock"}
	f := newFake(t, func(w http.ResponseWriter, r *http.Request, body map[string]any, raw []byte) {
		if r.URL.Path != "/agents/AG1/agentAliases/AL1/sessions/sess-1/text" || body["inputText"] != "Reply READY" {
			w.WriteHeader(400)
			return
		}
		at, err := time.Parse("20060102T150405Z", r.Header.Get("X-Amz-Date"))
		check, _ := http.NewRequest(r.Method, "https://"+r.Host+r.URL.RequestURI(), nil)
		check.Header.Set("Content-Type", r.Header.Get("Content-Type"))
		if err != nil || SignV4(check, raw, keys, at) != nil || check.Header.Get("Authorization") != r.Header.Get("Authorization") {
			w.WriteHeader(403)
			_, _ = w.Write([]byte(`{"message":"The request signature we calculated does not match"}`))
			return
		}
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		for _, s := range []string{"REA", "DY"} {
			p, _ := json.Marshal(map[string]string{"bytes": base64.StdEncoding.EncodeToString([]byte(s))})
			_, _ = w.Write(EncodeAWSEvent(map[string]string{":event-type": "chunk", ":message-type": "event"}, p))
		}
	})
	_, m, a := fill(t, "bedrock_agent", "", map[string]string{"region": "eu-west-1", "agent_id": "AG1", "alias_id": "AL1"})
	if a.Region != "eu-west-1" || a.Service != "bedrock" {
		t.Fatalf("auth %+v", a)
	}
	a.AccessKeySecret, a.SecretKeySecret = "AWS_AK", "AWS_SK"
	auth, err := NewAuthorizer(a, map[string]string{"AWS_AK": keys.AccessKey, "AWS_SK": keys.SecretKey}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	u := f.srv.URL + "/agents/AG1/agentAliases/AL1/sessions/{{session_id}}/text"
	call(t, f.caller(auth, "", ""), &schemas.RemoteAgent{URL: u, Mapping: m, Auth: a})
}

func TestPresetAgentforceOAuth(t *testing.T) {
	ended := false
	f := newFake(t, func(w http.ResponseWriter, r *http.Request, body map[string]any, _ []byte) {
		const api = "/einstein/ai-agent/v1"
		switch {
		case r.URL.Path == "/services/oauth2/token":
			_ = r.ParseForm()
			if r.PostForm.Get("client_id") != "sf-id" || r.PostForm.Get("client_secret") != "sf-secret" {
				w.WriteHeader(400)
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"sf-tok","token_type":"Bearer"}`))
		case r.Header.Get("Authorization") != "Bearer sf-tok":
			w.WriteHeader(401)
		case r.Method == "POST" && r.URL.Path == api+"/agents/0Xx1/sessions":
			if body["externalSessionKey"] != "uuid-1" || !strings.HasPrefix(body["instanceConfig"].(map[string]any)["endpoint"].(string), "https://127.0.0.1:") {
				w.WriteHeader(400)
				return
			}
			_, _ = w.Write([]byte(`{"sessionId":"sf-sess"}`))
		case r.Method == "POST" && r.URL.Path == api+"/sessions/sf-sess/messages":
			msg := body["message"].(map[string]any)
			if msg["text"] != "Reply READY" || msg["sequenceId"] != float64(1) {
				w.WriteHeader(400)
				return
			}
			_, _ = w.Write([]byte(`{"messages":[{"type":"Inform","message":"READY"}]}`))
		case r.Method == "DELETE" && r.URL.Path == api+"/sessions/sf-sess" && r.Header.Get("x-session-end-reason") == "UserRequest":
			ended = true
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(404)
		}
	})
	u, m, a := fill(t, "agentforce", "", map[string]string{"my_domain": f.host(), "agent_id": "0Xx1"})
	u = strings.Replace(u, "https://api.salesforce.com", f.srv.URL, 1)
	m = rehost(t, m, "https://api.salesforce.com", f.srv.URL)
	a.ClientIDSecret, a.ClientSecretSecret = "SF_ID", "SF_SECRET"
	auth, err := NewAuthorizer(a, map[string]string{"SF_ID": "sf-id", "SF_SECRET": "sf-secret"}, f.srv.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	call(t, f.caller(auth, "", ""), &schemas.RemoteAgent{URL: u, Mapping: m, Auth: a})
	if !ended || auth.Fetches != 1 {
		t.Fatalf("session ended %v, token fetches %d", ended, auth.Fetches)
	}
}

func TestPresetCopilotStudioDirectLine(t *testing.T) {
	polls := 0
	f := newFake(t, func(w http.ResponseWriter, r *http.Request, body map[string]any, _ []byte) {
		const base = "/v3/directline/conversations"
		switch {
		case r.Header.Get("Authorization") != "Bearer dl-secret":
			w.WriteHeader(401)
		case r.Method == "POST" && r.URL.Path == base:
			_, _ = w.Write([]byte(`{"conversationId":"conv1","token":"t"}`))
		case r.Method == "POST" && r.URL.Path == base+"/conv1/activities":
			if body["text"] != "Reply READY" || body["type"] != "message" {
				w.WriteHeader(400)
				return
			}
			_, _ = w.Write([]byte(`{"id":"conv1|0000002"}`))
		case r.Method == "GET" && r.URL.Path == base+"/conv1/activities":
			polls++
			acts := `{"type":"message","from":{"role":"bot"},"text":"Hello, I am the bot"},{"type":"message","id":"conv1|0000002","from":{"id":"heimdall-user"},"text":"Reply READY"}`
			if polls > 1 {
				acts += `,{"type":"message","replyToId":"conv1|0000002","from":{"role":"bot"},"text":"READY"}`
			}
			_, _ = w.Write([]byte(`{"activities":[` + acts + `],"watermark":"3"}`))
		default:
			w.WriteHeader(404)
		}
	})
	u, m, _ := fill(t, "copilot_studio", "", nil)
	u = strings.Replace(u, "https://directline.botframework.com", f.srv.URL, 1)
	m = rehost(t, m, "https://directline.botframework.com", f.srv.URL)
	ex := call(t, f.caller(nil, "dl-secret", ""), &schemas.RemoteAgent{URL: u, Mapping: m, AuthSecret: "DL"})
	if ex.Polls < 2 {
		t.Fatalf("polls %d", ex.Polls)
	}
}

func TestPresetVercelAI(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, r *http.Request, body map[string]any, _ []byte) {
		parts := body["messages"].([]any)[0].(map[string]any)["parts"].([]any)
		if parts[0].(map[string]any)["text"] != "Reply READY" {
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("x-vercel-ai-ui-message-stream", "v1")
		for _, ev := range []string{`{"type":"start"}`, `{"type":"text-start","id":"t1"}`, `{"type":"text-delta","id":"t1","delta":"READ"}`, `{"type":"text-delta","id":"t1","delta":"Y"}`, `{"type":"finish"}`, `[DONE]`} {
			_, _ = io.WriteString(w, "data: "+ev+"\n\n")
		}
	})
	u, m, _ := fill(t, "vercel_ai", f.srv.URL+"/api/chat", nil)
	call(t, f.caller(nil, "", ""), &schemas.RemoteAgent{URL: u, Mapping: m})
}

func TestPresetChatCompletions(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, r *http.Request, body map[string]any, _ []byte) {
		if body["model"] != "my-agent" || body["messages"].([]any)[0].(map[string]any)["role"] != "user" {
			w.WriteHeader(400)
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"READY"}}]}`))
	})
	u, m, _ := fill(t, "chat_completions", f.srv.URL+"/v1/chat/completions", map[string]string{"model": "my-agent"})
	call(t, f.caller(nil, "", ""), &schemas.RemoteAgent{URL: u, Mapping: m})
}

func TestPresetsAllValid(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range Presets {
		if seen[p.ID] {
			t.Fatalf("duplicate preset %s", p.ID)
		}
		seen[p.ID] = true
		in := map[string]string{}
		for _, i := range p.Inputs {
			in[i.Name] = "x1"
		}
		u, m, a, err := p.Fill("https://agent.example.com/run", in)
		if err != nil {
			t.Fatalf("%s: %v", p.ID, err)
		}
		if a != nil {
			switch a.Type {
			case schemas.RemoteAuthOAuth2:
				a.ClientIDSecret, a.ClientSecretSecret = "A", "B"
			case schemas.RemoteAuthSigV4:
				a.AccessKeySecret, a.SecretKeySecret = "A", "B"
			case schemas.RemoteAuthGCPIDToken:
				a.ServiceAccountSecret = "A"
			}
		}
		if err := Check(&schemas.RemoteAgent{URL: u, Mapping: m, Auth: a}); err != nil {
			t.Fatalf("%s: %v", p.ID, err)
		}
		if _, err := Plan(&schemas.RemoteAgent{URL: u, Mapping: m, Auth: a}, sampleVars(), nil); err != nil {
			t.Fatalf("%s plan: %v", p.ID, err)
		}
	}
	if _, _, _, err := Presets[0].Fill("", map[string]string{"assistant_id": `a"b`}); err == nil {
		t.Fatal("quote in input accepted")
	}
}

func TestEntraClientCredentials(t *testing.T) {
	f := newFake(t, func(w http.ResponseWriter, r *http.Request, body map[string]any, _ []byte) {
		switch r.URL.Path {
		case "/contoso/oauth2/v2.0/token":
			_ = r.ParseForm()
			if r.PostForm.Get("scope") != "api://agent/.default" || r.PostForm.Get("grant_type") != "client_credentials" {
				w.WriteHeader(400)
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"entra-tok","expires_in":"3599"}`))
		default:
			if r.Header.Get("Authorization") != "Bearer entra-tok" {
				w.WriteHeader(401)
				return
			}
			_, _ = w.Write([]byte(`{"reply":{"text":"READY"}}`))
		}
	})
	a := &schemas.RemoteAuth{Type: schemas.RemoteAuthEntra, TenantID: "contoso", TokenURL: f.srv.URL + "/contoso/oauth2/v2.0/token",
		ClientIDSecret: "ID", ClientSecretSecret: "SEC", Scope: "api://agent/.default"}
	auth, err := NewAuthorizer(a, map[string]string{"ID": "i", "SEC": "s"}, f.srv.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	call(t, f.caller(auth, "", ""), &schemas.RemoteAgent{URL: f.srv.URL + "/agent", Auth: a,
		Mapping: &schemas.RemoteMapping{Body: `{"q":"{{instruction}}"}`, AnswerPath: "reply.text"}})
	if TokenURL(&schemas.RemoteAuth{Type: schemas.RemoteAuthEntra, TenantID: "contoso"}) != "https://login.microsoftonline.com/contoso/oauth2/v2.0/token" {
		t.Fatal("entra token URL")
	}
}

// pemCert makes a certificate signed by parent (self-signed when nil).
func pemCert(t *testing.T, cn string, parent *x509.Certificate, parentKey *ecdsa.PrivateKey, ca bool) (*x509.Certificate, *ecdsa.PrivateKey, string, string) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: ca, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	if parent == nil {
		parent, parentKey = tpl, key
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, parent, &key.PublicKey, parentKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	kder, _ := x509.MarshalECPrivateKey(key)
	return cert, key, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}))
}

func TestMutualTLS(t *testing.T) {
	ca, caKey, _, _ := pemCert(t, "test ca", nil, nil, true)
	_, _, clientPEM, clientKey := pemCert(t, "heimdall", ca, caKey, false)
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.TLS.PeerCertificates) == 0 || r.TLS.PeerCertificates[0].Subject.CommonName != "heimdall" {
			w.WriteHeader(403)
			return
		}
		_, _ = w.Write([]byte(`{"answer":"READY"}`))
	}))
	srv.TLS = &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}
	srv.StartTLS()
	defer srv.Close()
	serverPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))
	a := &schemas.RemoteAuth{ClientCertSecret: "CERT", ClientKeySecret: "KEY", CACertSecret: "CA"}
	auth, err := NewAuthorizer(a, map[string]string{"CERT": clientPEM, "KEY": clientKey, "CA": serverPEM}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &Caller{Auth: auth, Client: func(cfg *tls.Config) *http.Client {
		return &http.Client{Transport: &http.Transport{TLSClientConfig: cfg}}
	}}
	r := &schemas.RemoteAgent{URL: srv.URL + "/run", Auth: a, Mapping: &schemas.RemoteMapping{AnswerPath: "answer"}}
	ex, err := c.Call(context.Background(), r, vars())
	if err != nil || ex.Answer != "READY" {
		t.Fatalf("mtls: %v %q", err, ex.Answer)
	}
	// Without the client certificate the server refuses the handshake.
	c.Auth = nil
	if _, err := c.Call(context.Background(), &schemas.RemoteAgent{URL: srv.URL + "/run", Mapping: &schemas.RemoteMapping{AnswerPath: "answer"}}, vars()); err == nil {
		t.Fatal("call without a client certificate succeeded")
	}
}

func TestPlanMasksSecrets(t *testing.T) {
	r := &schemas.RemoteAgent{URL: "https://agent.example.com/run", AuthSecret: "TOK",
		Mapping: &schemas.RemoteMapping{Headers: map[string]string{"X-Api-Key": "{{secret.KEY}}"}, Body: `{"q":"{{instruction}}"}`}}
	sent, err := Plan(r, sampleVars(), nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(sent)
	if !strings.Contains(string(b), "[secret KEY]") || !strings.Contains(string(b), "Bearer \\u003csecret TOK\\u003e") || !strings.Contains(string(b), SampleInstruction) {
		t.Fatalf("plan %s", b)
	}
}
