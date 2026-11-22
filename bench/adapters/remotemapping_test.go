package adapters

// Lane Y10: the launcher (remotehttp.py) runs every platform preset and
// auth type against local fakes, with the environment the adapter builds,
// so the Python twin of remotemap stays in step with the Go one.

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/remotemap"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// platformZoo is one TLS server that plays every preset's platform.
type platformZoo struct {
	srv     *httptest.Server
	gcpKey  *rsa.PrivateKey
	aws     remotemap.SigV4Keys
	mu      sync.Mutex
	polls   int
	ended   bool
	fetches map[string]int
}

func newZoo(t *testing.T) *platformZoo {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	z := &platformZoo{gcpKey: key, fetches: map[string]int{},
		aws: remotemap.SigV4Keys{AccessKey: "AKIATEST", SecretKey: "aws/secret", Region: "eu-west-1", Service: "bedrock"}}
	z.srv = httptest.NewTLSServer(http.HandlerFunc(z.serve))
	t.Cleanup(z.srv.Close)
	return z
}

func (z *platformZoo) serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(raw))
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	z.mu.Lock()
	defer z.mu.Unlock()
	bad := func() { w.WriteHeader(400); _, _ = w.Write([]byte(`{"error":"bad request"}`)) }
	str := func(v any) string { s, _ := v.(string); return s }
	switch p := r.URL.Path; {
	case p == "/runs/wait":
		msgs, _ := body["input"].(map[string]any)["messages"].([]any)
		if r.Header.Get("x-api-key") != "lsv2-key" || len(msgs) != 1 || body["assistant_id"] != "agent" {
			bad()
			return
		}
		_, _ = w.Write([]byte(`{"messages":[{"content":"q"},{"content":"READY"}]}`))
	case p == "/gcp/token":
		_ = r.ParseForm()
		parts := strings.Split(r.PostForm.Get("assertion"), ".")
		if len(parts) != 3 {
			bad()
			return
		}
		sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
		sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		cb, _ := base64.RawURLEncoding.DecodeString(parts[1])
		var claims map[string]any
		_ = json.Unmarshal(cb, &claims)
		if rsa.VerifyPKCS1v15(&z.gcpKey.PublicKey, crypto.SHA256, sum[:], sig) != nil || claims["target_audience"] != "https://agent.run.app" {
			w.WriteHeader(401)
			return
		}
		z.fetches["gcp"]++
		_, _ = w.Write([]byte(`{"id_token":"gid-1","expires_in":3600}`))
	case strings.HasPrefix(p, "/apps/support/users/heimdall/sessions/"):
		if r.Header.Get("Authorization") != "Bearer gid-1" {
			w.WriteHeader(401)
			return
		}
		_, _ = w.Write([]byte(`{"id":"s1"}`))
	case p == "/run":
		nm, _ := body["new_message"].(map[string]any)
		if r.Header.Get("Authorization") != "Bearer gid-1" || nm == nil || body["session_id"] != "s1" {
			bad()
			return
		}
		_, _ = w.Write([]byte(`[{"content":{"role":"model","parts":[{"text":"READY"}]}}]`))
	case p == "/api/agents/support/generate":
		if body["threadId"] != "s1" {
			bad()
			return
		}
		_, _ = w.Write([]byte(`{"text":"READY"}`))
	case p == "/webhook/abc":
		if str(body["chatInput"]) == "" || body["sessionId"] != "s1" {
			bad()
			return
		}
		_, _ = w.Write([]byte(`{"output":"READY"}`))
	case p == "/v1/responses":
		w.Header().Set("Content-Type", "text/event-stream")
		for _, ev := range []string{`{"type":"response.created"}`, `{"type":"response.output_text.delta","delta":"REA"}`, `{"type":"response.output_text.delta","delta":"DY"}`} {
			_, _ = io.WriteString(w, "event: e\ndata: "+ev+"\n\n")
		}
	case p == "/agents/AG1/agentAliases/AL1/sessions/s1/text":
		at, err := time.Parse("20060102T150405Z", r.Header.Get("X-Amz-Date"))
		check, _ := http.NewRequest(r.Method, "https://"+r.Host+r.URL.RequestURI(), nil)
		check.Header.Set("Content-Type", r.Header.Get("Content-Type"))
		if err != nil || remotemap.SignV4(check, raw, z.aws, at) != nil || check.Header.Get("Authorization") != r.Header.Get("Authorization") {
			w.WriteHeader(403)
			_, _ = w.Write([]byte(`{"message":"signature mismatch"}`))
			return
		}
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		for _, s := range []string{"REA", "DY"} {
			pl, _ := json.Marshal(map[string]string{"bytes": base64.StdEncoding.EncodeToString([]byte(s))})
			_, _ = w.Write(remotemap.EncodeAWSEvent(map[string]string{":event-type": "chunk", ":message-type": "event"}, pl))
		}
	case p == "/services/oauth2/token":
		_ = r.ParseForm()
		if r.PostForm.Get("client_id") != "sf-id" || r.PostForm.Get("client_secret") != "sf-secret" {
			w.WriteHeader(401)
			return
		}
		z.fetches["sf"]++
		_, _ = w.Write([]byte(`{"access_token":"sf-tok"}`))
	case strings.HasPrefix(p, "/einstein/ai-agent/v1/"):
		if r.Header.Get("Authorization") != "Bearer sf-tok" {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.Method == "POST" && p == "/einstein/ai-agent/v1/agents/0Xx1/sessions":
			_, _ = w.Write([]byte(`{"sessionId":"sf-sess"}`))
		case r.Method == "POST" && p == "/einstein/ai-agent/v1/sessions/sf-sess/messages":
			_, _ = w.Write([]byte(`{"messages":[{"type":"Inform","message":"READY"}]}`))
		case r.Method == "DELETE" && p == "/einstein/ai-agent/v1/sessions/sf-sess":
			z.ended = true
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(404)
		}
	case strings.HasPrefix(p, "/v3/directline/conversations"):
		if r.Header.Get("Authorization") != "Bearer dl-secret" {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.Method == "POST" && p == "/v3/directline/conversations":
			_, _ = w.Write([]byte(`{"conversationId":"conv1"}`))
		case r.Method == "POST":
			_, _ = w.Write([]byte(`{"id":"conv1|0002"}`))
		default:
			z.polls++
			acts := `{"from":{"role":"bot"},"text":"Hi there"}`
			if z.polls > 1 {
				acts += `,{"replyToId":"conv1|0002","from":{"role":"bot"},"text":"READY"}`
			}
			_, _ = w.Write([]byte(`{"activities":[` + acts + `]}`))
		}
	case p == "/api/chat":
		w.Header().Set("Content-Type", "text/event-stream")
		for _, ev := range []string{`{"type":"start"}`, `{"type":"text-delta","id":"t","delta":"READ"}`, `{"type":"text-delta","id":"t","delta":"Y"}`, `[DONE]`} {
			_, _ = io.WriteString(w, "data: "+ev+"\n\n")
		}
	case p == "/v1/chat/completions":
		if body["model"] != "my-agent" {
			bad()
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"READY"}}]}`))
	default:
		w.WriteHeader(404)
	}
}

// launchMapped builds the launcher env the adapter would (remoteMappingEnv),
// expands its secret references as the engine does and runs the launcher.
func launchMapped(t *testing.T, z *platformZoo, spec *schemas.RemoteAgent, secrets map[string]string) (string, string) {
	t.Helper()
	if err := utils.CheckRemoteAgent(spec); err != nil {
		t.Fatalf("check: %v", err)
	}
	egress, err := remotemap.EgressTargets(spec)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"BENCH_REMOTE_URL": spec.URL, "BENCH_REMOTE_TIMEOUT_S": "20", "BENCH_REMOTE_SESSION_ID": "s1", "BENCH_REMOTE_SESSION_INDEX": "1"}
	if spec.AuthSecret != "" {
		env["BENCH_REMOTE_TOKEN"] = "{{secret." + spec.AuthSecret + "}}"
	}
	if spec.AuthHeader != "" {
		env["BENCH_REMOTE_AUTH_HEADER"] = spec.AuthHeader
	}
	rc := &schemas.RunContext{Run: &schemas.Run{RunID: "run1"}, Agent: &schemas.AgentConfig{Model: "my-model"}}
	if err := remoteMappingEnv(rc, spec, egress, env); err != nil {
		t.Fatal(err)
	}
	// The run's forwarder ports: on the host, straight to the fake.
	cm := map[string]string{}
	for _, e := range egress {
		cm[e] = strings.TrimPrefix(z.srv.URL, "https://")
	}
	b, _ := json.Marshal(cm)
	env["BENCH_REMOTE_CONNECT_MAP"] = string(b)
	for k, v := range env {
		out, missing := utils.ExpandSecretRefs(v, secrets)
		if len(missing) > 0 {
			t.Fatalf("env %s names missing secrets %v", k, missing)
		}
		env[k] = out
	}
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: z.srv.Certificate().Raw}), 0o644); err != nil {
		t.Fatal(err)
	}
	env["SSL_CERT_FILE"] = ca
	answer, out, err := runLauncher(t, env)
	if err != nil {
		t.Fatalf("launcher: %v\n%s", err, out)
	}
	for _, v := range secrets {
		if len(v) > 6 && strings.Contains(out, v) {
			t.Fatalf("the launcher printed a secret value\n%s", out)
		}
	}
	return answer, out
}

func zooPreset(t *testing.T, z *platformZoo, id, url string, inputs map[string]string, hosts ...string) (string, *schemas.RemoteMapping, *schemas.RemoteAuth) {
	t.Helper()
	p, _ := remotemap.FindPreset(id)
	u, m, a, err := p.Fill(url, inputs)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hosts {
		u = strings.ReplaceAll(u, h, z.srv.URL)
		b, _ := json.Marshal(m)
		var mm schemas.RemoteMapping
		_ = json.Unmarshal([]byte(strings.ReplaceAll(string(b), h, z.srv.URL)), &mm)
		m = &mm
	}
	return u, m, a
}

func TestLauncherPresets(t *testing.T) {
	z := newZoo(t)
	host := strings.TrimPrefix(z.srv.URL, "https://")
	der, _ := x509.MarshalPKCS8PrivateKey(z.gcpKey)
	sa, _ := json.Marshal(map[string]string{"client_email": "sa@p.iam.gserviceaccount.com", "private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))})
	secrets := map[string]string{"LS_KEY": "lsv2-key", "GSA": string(sa), "N8N": "n8n-token", "OPENAI": "sk-test-token",
		"AWS_AK": z.aws.AccessKey, "AWS_SK": z.aws.SecretKey, "SF_ID": "sf-id", "SF_SECRET": "sf-secret", "DL": "dl-secret"}
	cases := []struct {
		id   string
		spec func() *schemas.RemoteAgent
	}{
		{"langgraph", func() *schemas.RemoteAgent {
			u, m, _ := zooPreset(t, z, "langgraph", z.srv.URL+"/runs/wait", nil)
			return &schemas.RemoteAgent{URL: u, Mapping: m, AuthSecret: "LS_KEY", AuthHeader: "x-api-key"}
		}},
		{"adk", func() *schemas.RemoteAgent {
			u, m, a := zooPreset(t, z, "adk", z.srv.URL+"/run", map[string]string{"app_name": "support"})
			a.ServiceAccountSecret, a.Audience, a.TokenURL = "GSA", "https://agent.run.app", z.srv.URL+"/gcp/token"
			return &schemas.RemoteAgent{URL: u, Mapping: m, Auth: a}
		}},
		{"mastra", func() *schemas.RemoteAgent {
			u, m, _ := zooPreset(t, z, "mastra", z.srv.URL+"/api/agents/support/generate", nil)
			return &schemas.RemoteAgent{URL: u, Mapping: m}
		}},
		{"n8n", func() *schemas.RemoteAgent {
			u, m, _ := zooPreset(t, z, "n8n", z.srv.URL+"/webhook/abc", nil)
			return &schemas.RemoteAgent{URL: u, Mapping: m, AuthSecret: "N8N"}
		}},
		{"openai_responses", func() *schemas.RemoteAgent {
			u, m, _ := zooPreset(t, z, "openai_responses", z.srv.URL+"/v1/responses", map[string]string{"model": "gpt-5-mini"})
			return &schemas.RemoteAgent{URL: u, Mapping: m, AuthSecret: "OPENAI"}
		}},
		{"bedrock_agent", func() *schemas.RemoteAgent {
			_, m, a := zooPreset(t, z, "bedrock_agent", "", map[string]string{"region": "eu-west-1", "agent_id": "AG1", "alias_id": "AL1"})
			a.AccessKeySecret, a.SecretKeySecret = "AWS_AK", "AWS_SK"
			return &schemas.RemoteAgent{URL: z.srv.URL + "/agents/AG1/agentAliases/AL1/sessions/{{session_id}}/text", Mapping: m, Auth: a}
		}},
		{"agentforce", func() *schemas.RemoteAgent {
			u, m, a := zooPreset(t, z, "agentforce", "", map[string]string{"my_domain": host, "agent_id": "0Xx1"}, "https://api.salesforce.com")
			a.ClientIDSecret, a.ClientSecretSecret = "SF_ID", "SF_SECRET"
			return &schemas.RemoteAgent{URL: u, Mapping: m, Auth: a}
		}},
		{"copilot_studio", func() *schemas.RemoteAgent {
			u, m, _ := zooPreset(t, z, "copilot_studio", "", nil, "https://directline.botframework.com")
			return &schemas.RemoteAgent{URL: u, Mapping: m, AuthSecret: "DL"}
		}},
		{"vercel_ai", func() *schemas.RemoteAgent {
			u, m, _ := zooPreset(t, z, "vercel_ai", z.srv.URL+"/api/chat", nil)
			return &schemas.RemoteAgent{URL: u, Mapping: m}
		}},
		{"chat_completions", func() *schemas.RemoteAgent {
			u, m, _ := zooPreset(t, z, "chat_completions", z.srv.URL+"/v1/chat/completions", map[string]string{"model": "my-agent"})
			return &schemas.RemoteAgent{URL: u, Mapping: m}
		}},
	}
	if len(cases) != len(remotemap.Presets) {
		t.Fatalf("%d cases for %d presets", len(cases), len(remotemap.Presets))
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			answer, out := launchMapped(t, z, tc.spec(), secrets)
			if answer != "READY" {
				t.Fatalf("answer %q\n%s", answer, out)
			}
		})
	}
	if !z.ended || z.fetches["sf"] != 1 || z.fetches["gcp"] != 1 {
		t.Fatalf("agentforce ended %v, token fetches %v (want one per run)", z.ended, z.fetches)
	}
}

func TestLauncherMutualTLS(t *testing.T) {
	z := newZoo(t) // only for the CA file helper
	caKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	caTpl := &x509.Certificate{SerialNumber: bigOne(), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	ca, _ := x509.ParseCertificate(caDER)
	cliKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	cliTpl := &x509.Certificate{SerialNumber: bigOne(), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, KeyUsage: x509.KeyUsageDigitalSignature,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	cliDER, _ := x509.CreateCertificate(rand.Reader, cliTpl, ca, &cliKey.PublicKey, caKey)
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.TLS.PeerCertificates) == 0 {
			w.WriteHeader(403)
			return
		}
		_, _ = w.Write([]byte(`{"result":{"text":"READY"}}`))
	}))
	srv.TLS = &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}
	srv.StartTLS()
	defer srv.Close()
	z.srv = srv
	keyDER := x509.MarshalPKCS1PrivateKey(cliKey)
	secrets := map[string]string{
		"CERT": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cliDER})),
		"KEY":  string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: keyDER})),
		"CA":   string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})),
	}
	spec := &schemas.RemoteAgent{URL: srv.URL + "/agent", Auth: &schemas.RemoteAuth{ClientCertSecret: "CERT", ClientKeySecret: "KEY", CACertSecret: "CA"},
		Mapping: &schemas.RemoteMapping{Body: `{"q":"{{instruction}}"}`, AnswerPath: "result.text"}}
	if answer, out := launchMapped(t, z, spec, secrets); answer != "READY" {
		t.Fatalf("answer %q\n%s", answer, out)
	}
}

func TestRemoteHTTPPlanMapped(t *testing.T) {
	agent := remoteAgent(RemoteTrafficExternal)
	agent.Remote.URL = "https://agent.example.com/v1/chat"
	agent.Remote.Mapping = &schemas.RemoteMapping{Headers: map[string]string{"X-Api-Key": "{{secret.AGENT_KEY}}"}, Body: `{"q":"{{instruction}}"}`, AnswerPath: "a"}
	agent.Remote.Auth = &schemas.RemoteAuth{Type: schemas.RemoteAuthOAuth2, TokenURL: "https://login.example.com/oauth/token", ClientIDSecret: "CID", ClientSecretSecret: "CSEC"}
	rc, cfg := remoteFixture(t, agent)
	plan, err := newRemoteHTTPAdapter(cfg).Plan(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Egress) != 2 || plan.Egress[1] != "login.example.com:443" {
		t.Fatalf("egress %v", plan.Egress)
	}
	if !strings.Contains(plan.Env["BENCH_REMOTE_MAPPING"], "{{env_secret.AGENT_KEY}}") || strings.Contains(plan.Env["BENCH_REMOTE_MAPPING"], "{{secret.") {
		t.Fatalf("mapping env %s", plan.Env["BENCH_REMOTE_MAPPING"])
	}
	for _, n := range []string{"AGENT_KEY", "CID", "CSEC"} {
		if plan.Env["BENCH_REMOTE_SECRET_"+n] != "{{secret."+n+"}}" {
			t.Fatalf("secret env %s: %q", n, plan.Env["BENCH_REMOTE_SECRET_"+n])
		}
	}
	names := strings.Join(utils.RunSecretNames(rc), ",")
	if !strings.Contains(names, "AGENT_KEY") || !strings.Contains(names, "CSEC") {
		t.Fatalf("run secret names %s", names)
	}
	agent.Remote.Mapping.Start = &schemas.RemoteStep{URL: "https://evil.example.com/x"}
	if _, err := newRemoteHTTPAdapter(cfg).Plan(context.Background(), rc); err == nil || !strings.Contains(err.Error(), "host") {
		t.Fatalf("off-host step accepted: %v", err)
	}
}

func bigOne() *big.Int { return big.NewInt(time.Now().UnixNano()) }
