package agentplatform

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// fake is one httptest server standing in for every platform host: the
// importer's Rewrite sends each request here and keeps the platform's
// host in the Host header, so routes are keyed by host and path.
type fake struct {
	t      *testing.T
	srv    *httptest.Server
	mu     sync.Mutex
	routes map[string]http.HandlerFunc // "METHOD host/path"
	seen   []string
}

func newFake(t *testing.T) *fake {
	f := &fake{t: t, routes: map[string]http.HandlerFunc{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		k := r.Method + " " + r.Host + r.URL.Path
		f.mu.Lock()
		f.seen = append(f.seen, k)
		h := f.routes[k]
		f.mu.Unlock()
		if h == nil {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"message":"no route `+k+`"}`)
			return
		}
		h(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fake) on(key string, h http.HandlerFunc) { f.routes[key] = h }

func (f *fake) client() *Client {
	target, _ := url.Parse(f.srv.URL)
	return &Client{HTTP: f.srv.Client(), Now: func() time.Time { return time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC) },
		Rewrite: func(u *url.URL) { u.Scheme, u.Host = target.Scheme, target.Host }}
}

func sendJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func wantHeader(t *testing.T, r *http.Request, name, want string) bool {
	t.Helper()
	if got := r.Header.Get(name); got != want {
		t.Errorf("%s %s: header %s = %q, want %q", r.Method, r.URL.Path, name, got, want)
		return false
	}
	return true
}

func mustImport(t *testing.T, c *Client, req Request, secrets map[string]string) *Result {
	t.Helper()
	res, err := c.Import(context.Background(), req, secrets)
	if err != nil {
		t.Fatalf("import %s: %v", req.Platform, err)
	}
	for _, v := range secrets {
		b, _ := json.Marshal(res)
		if strings.Contains(string(b), v) {
			t.Fatalf("import %s: result holds a secret value", req.Platform)
		}
		y, err := AgentYAML("imp", res, AgentDefaults{})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(y), v) {
			t.Fatalf("import %s: agent YAML holds a secret value", req.Platform)
		}
	}
	return res
}

func TestVapiImport(t *testing.T) {
	f := newFake(t)
	f.on("GET api.vapi.ai/assistant/asst-1", func(w http.ResponseWriter, r *http.Request) {
		if !wantHeader(t, r, "Authorization", "Bearer vapi-private-123") {
			w.WriteHeader(401)
			return
		}
		sendJSON(w, map[string]any{"id": "asst-1", "name": "Support line", "firstMessage": "Hi, this is Acme support.",
			"model": map[string]any{"provider": "openai", "model": "gpt-4.1", "messages": []any{map[string]any{"role": "system", "content": "You are Acme's support agent."}},
				"tools": []any{map[string]any{"type": "function", "function": map[string]any{"name": "lookup_order", "description": "Find an order"}}}, "toolIds": []any{"tool-9"}},
			"voice":       map[string]any{"provider": "11labs", "voiceId": "rachel", "stability": 0.5},
			"transcriber": map[string]any{"provider": "deepgram", "language": "en"}})
	})
	f.on("GET api.vapi.ai/tool/tool-9", func(w http.ResponseWriter, r *http.Request) {
		sendJSON(w, map[string]any{"id": "tool-9", "type": "transferCall", "function": map[string]any{"name": "transfer_to_human"}})
	})
	res := mustImport(t, f.client(), Request{Platform: Vapi, AgentID: "asst-1", Secrets: map[string]string{SecretAPIKey: "VAPI_KEY"}},
		map[string]string{"VAPI_KEY": "vapi-private-123"})
	d := res.Definition
	if d.Name != "Support line" || d.FirstMessage != "Hi, this is Acme support." || d.Prompt != "You are Acme's support agent." || d.Model != "openai/gpt-4.1" || d.Language != "en" {
		t.Fatalf("definition: %+v", d)
	}
	if len(d.Tools) != 2 || d.Tools[0].Name != "lookup_order" || d.Tools[1].Name != "transfer_to_human" {
		t.Fatalf("tools: %+v", d.Tools)
	}
	if d.Voice == nil || d.Voice.VoiceID != "rachel" || d.Voice.Settings["stability"] != 0.5 {
		t.Fatalf("voice: %+v", d.Voice)
	}
	if res.Adapter != "voice_platform" || res.Block["platform"] != "vapi" || res.Block["agent_id"] != "asst-1" || res.Block["api_key_secret"] != "VAPI_KEY" {
		t.Fatalf("block: %+v", res.Block)
	}
	if strings.Join(res.CapabilityHints, ",") != "voice,sessions,tools" {
		t.Fatalf("hints: %v", res.CapabilityHints)
	}
}

func TestVapiRefusedKey(t *testing.T) {
	f := newFake(t)
	f.on("GET api.vapi.ai/assistant/asst-1", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		io.WriteString(w, `{"message":"Invalid key vapi-bad-999"}`)
	})
	_, err := f.client().Import(context.Background(), Request{Platform: Vapi, AgentID: "asst-1", Secrets: map[string]string{SecretAPIKey: "VAPI_KEY"}},
		map[string]string{"VAPI_KEY": "vapi-bad-999"})
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "vapi-bad-999") {
		t.Fatalf("error leaks the key: %v", err)
	}
}

func TestRetellImportChatThenVoiceAgent(t *testing.T) {
	f := newFake(t)
	f.on("GET api.retellai.com/get-agent/agent_abc", func(w http.ResponseWriter, r *http.Request) {
		wantHeader(t, r, "Authorization", "Bearer key_retell")
		sendJSON(w, map[string]any{"agent_id": "agent_abc", "agent_name": "Clinic booking", "language": "en-US", "voice_id": "11labs-Adrian",
			"voice_speed": 1.1, "response_engine": map[string]any{"type": "retell-llm", "llm_id": "llm_77"}})
	})
	f.on("GET api.retellai.com/get-retell-llm/llm_77", func(w http.ResponseWriter, r *http.Request) {
		sendJSON(w, map[string]any{"llm_id": "llm_77", "general_prompt": "Book appointments.", "begin_message": "Hello, clinic here.", "model": "gpt-4.1-mini",
			"general_tools": []any{map[string]any{"type": "end_call", "name": "end_call", "description": "Hang up"}}})
	})
	res := mustImport(t, f.client(), Request{Platform: Retell, AgentID: "agent_abc", Secrets: map[string]string{SecretAPIKey: "RETELL_KEY"}},
		map[string]string{"RETELL_KEY": "key_retell"})
	d := res.Definition
	if d.Name != "Clinic booking" || d.Prompt != "Book appointments." || d.FirstMessage != "Hello, clinic here." || d.Model != "gpt-4.1-mini" || len(d.Tools) != 1 {
		t.Fatalf("definition: %+v", d)
	}
	if d.Voice == nil || d.Voice.VoiceID != "11labs-Adrian" {
		t.Fatalf("voice: %+v", d.Voice)
	}
	if !strings.Contains(strings.Join(res.Warnings, " "), "voice agent") {
		t.Fatalf("want the chat agent warning, got %v", res.Warnings)
	}
	// The chat agent route answered 404 first.
	if f.seen[0] != "GET api.retellai.com/get-chat-agent/agent_abc" {
		t.Fatalf("first call: %v", f.seen)
	}
}

func TestElevenLabsImport(t *testing.T) {
	f := newFake(t)
	f.on("GET api.elevenlabs.io/v1/convai/agents/agent_01", func(w http.ResponseWriter, r *http.Request) {
		wantHeader(t, r, "xi-api-key", "xi-secret")
		sendJSON(w, map[string]any{"agent_id": "agent_01", "name": "Concierge", "conversation_config": map[string]any{
			"agent": map[string]any{"first_message": "Welcome.", "language": "en", "prompt": map[string]any{"prompt": "Help guests.", "llm": "gemini-2.5-flash",
				"tool_ids": []any{"tool_a"}, "built_in_tools": map[string]any{"end_call": map[string]any{"description": "End"}, "transfer_to_agent": nil}}},
			"tts": map[string]any{"voice_id": "voice_x", "model_id": "eleven_flash_v2", "stability": 0.4, "similarity_boost": 0.8}}})
	})
	f.on("GET api.elevenlabs.io/v1/convai/tools/tool_a", func(w http.ResponseWriter, r *http.Request) {
		sendJSON(w, map[string]any{"id": "tool_a", "tool_config": map[string]any{"name": "check_room", "type": "webhook", "description": "Room status"}})
	})
	res := mustImport(t, f.client(), Request{Platform: ElevenLabs, AgentID: "agent_01", Secrets: map[string]string{SecretAPIKey: "XI"}}, map[string]string{"XI": "xi-secret"})
	d := res.Definition
	if d.Name != "Concierge" || d.Prompt != "Help guests." || d.FirstMessage != "Welcome." || d.Model != "gemini-2.5-flash" {
		t.Fatalf("definition: %+v", d)
	}
	if len(d.Tools) != 2 || d.Tools[0].Name != "end_call" || d.Tools[1].Name != "check_room" {
		t.Fatalf("tools: %+v", d.Tools)
	}
	if d.Voice == nil || d.Voice.Model != "eleven_flash_v2" || d.Voice.Settings["similarity_boost"] != 0.8 {
		t.Fatalf("voice: %+v", d.Voice)
	}
	if res.Block["platform"] != "elevenlabs" {
		t.Fatalf("block: %+v", res.Block)
	}
}

func TestAgentCoreImport(t *testing.T) {
	f := newFake(t)
	arn := "arn:aws:bedrock-agentcore:us-west-2:123456789012:runtime/support_agent-AbCdEf1234"
	f.on("GET bedrock-agentcore-control.us-west-2.amazonaws.com/runtimes/support_agent-AbCdEf1234/", func(w http.ResponseWriter, r *http.Request) {
		a := r.Header.Get("Authorization")
		if !strings.HasPrefix(a, "AWS4-HMAC-SHA256 Credential=AKIATEST/20261122/us-west-2/bedrock-agentcore/aws4_request, SignedHeaders=") ||
			!strings.Contains(a, "host;") || r.Header.Get("X-Amz-Security-Token") != "tok-session" || r.Header.Get("X-Amz-Date") != "20261122T100000Z" {
			t.Errorf("signature headers: %q %q", a, r.Header.Get("X-Amz-Security-Token"))
			w.WriteHeader(403)
			return
		}
		sendJSON(w, map[string]any{"agentRuntimeArn": arn, "agentRuntimeName": "support_agent", "agentRuntimeId": "support_agent-AbCdEf1234",
			"agentRuntimeVersion": "3", "status": "READY", "description": "Tier 1 support", "protocolConfiguration": map[string]any{"serverProtocol": "HTTP"}})
	})
	res := mustImport(t, f.client(), Request{Platform: AgentCore, AgentID: arn,
		Secrets: map[string]string{SecretAccessKey: "AK", SecretSecretKey: "SK", SecretSessionToken: "ST"}},
		map[string]string{"AK": "AKIATEST", "SK": "secret-key-value", "ST": "tok-session"})
	if res.Definition.Name != "support_agent" || res.Definition.Version != "3" || res.Definition.Protocol != "HTTP" {
		t.Fatalf("definition: %+v", res.Definition)
	}
	u := res.Block["url"].(string)
	if u != "https://bedrock-agentcore.us-west-2.amazonaws.com/runtimes/"+url.PathEscape(arn)+"/invocations?qualifier=DEFAULT" {
		t.Fatalf("url %s", u)
	}
	auth := res.Block["auth"].(map[string]any)
	if auth["type"] != "aws_sigv4" || auth["service"] != "bedrock-agentcore" || auth["region"] != "us-west-2" || auth["access_key_secret"] != "AK" || auth["session_token_secret"] != "ST" {
		t.Fatalf("auth %+v", auth)
	}
	if res.Preset != "agentcore" || res.Adapter != "remote_http" {
		t.Fatalf("preset %s adapter %s", res.Preset, res.Adapter)
	}
}

func TestAgentCoreMCPRefused(t *testing.T) {
	f := newFake(t)
	f.on("GET bedrock-agentcore-control.us-east-1.amazonaws.com/runtimes/tools_srv-AbCdEf1234/", func(w http.ResponseWriter, r *http.Request) {
		sendJSON(w, map[string]any{"agentRuntimeArn": "arn:aws:bedrock-agentcore:us-east-1:123456789012:runtime/tools_srv-AbCdEf1234", "protocolConfiguration": map[string]any{"serverProtocol": "MCP"}})
	})
	_, err := f.client().Import(context.Background(), Request{Platform: AgentCore, AgentID: "tools_srv-AbCdEf1234", Inputs: map[string]string{"region": "us-east-1"},
		Secrets: map[string]string{SecretAccessKey: "AK", SecretSecretKey: "SK"}}, map[string]string{"AK": "a1234", "SK": "s1234"})
	if err == nil || !strings.Contains(err.Error(), "tool server") {
		t.Fatalf("err = %v", err)
	}
}

func TestSigV4KnownVector(t *testing.T) {
	// AWS SigV4 test suite "get-vanilla".
	req, _ := http.NewRequest("GET", "https://example.amazonaws.com/", nil)
	sigV4(req, nil, "AKIDEXAMPLE", "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", "", "us-east-1", "service", time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC))
	got := req.Header.Get("Authorization")
	if !strings.HasPrefix(got, "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=") {
		t.Fatalf("authorization %q", got)
	}
	// Without the extra header the published get-vanilla signature must come out.
	req2, _ := http.NewRequest("GET", "https://example.amazonaws.com/", nil)
	signV4(req2, nil, false, "AKIDEXAMPLE", "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", "", "us-east-1", "service", time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC))
	if s := req2.Header.Get("Authorization"); s != "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, SignedHeaders=host;x-amz-date, Signature=5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31" {
		t.Fatalf("get-vanilla: %q", s)
	}
}

func TestSigV4PathIsDoubleEncoded(t *testing.T) {
	req, _ := http.NewRequest("POST", "https://bedrock-agentcore.us-east-1.amazonaws.com/runtimes/"+url.PathEscape("arn:aws:x/y")+"/invocations", nil)
	sigV4(req, []byte("{}"), "AK", "SK", "", "us-east-1", "bedrock-agentcore", time.Unix(0, 0))
	if req.URL.RawPath != "/runtimes/arn%3Aaws%3Ax%2Fy/invocations" {
		t.Fatalf("wire path %q", req.URL.RawPath)
	}
}

func TestAgentforceImport(t *testing.T) {
	f := newFake(t)
	f.on("POST acme.my.salesforce.com/services/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("client_id") != "cid" || r.Form.Get("client_secret") != "csec" {
			w.WriteHeader(400)
			return
		}
		sendJSON(w, map[string]any{"access_token": "sf-token", "instance_url": "https://acme.my.salesforce.com"})
	})
	f.on("GET acme.my.salesforce.com/services/data/v62.0/query", func(w http.ResponseWriter, r *http.Request) {
		wantHeader(t, r, "Authorization", "Bearer sf-token")
		if !strings.Contains(r.URL.Query().Get("q"), "FROM BotDefinition WHERE Id = '0XxHn000000x9F1KAI'") {
			t.Errorf("query %q", r.URL.Query().Get("q"))
		}
		sendJSON(w, map[string]any{"records": []any{map[string]any{"Id": "0XxHn000000x9F1KAI", "DeveloperName": "Service_Agent", "MasterLabel": "Service Agent", "Description": "Cases"}}})
	})
	res := mustImport(t, f.client(), Request{Platform: Agentforce, AgentID: "0XxHn000000x9F1KAI", Inputs: map[string]string{"my_domain": "acme.my.salesforce.com"},
		Secrets: map[string]string{SecretClientID: "SF_ID", SecretClientSecret: "SF_SECRET"}}, map[string]string{"SF_ID": "cid", "SF_SECRET": "csec"})
	if res.Definition.Name != "Service Agent" || res.Definition.Description != "Cases" {
		t.Fatalf("definition %+v", res.Definition)
	}
	m := res.Block["mapping"].(map[string]any)
	start := m["start"].(map[string]any)
	if start["url"] != AgentforceAPI+"/agents/0XxHn000000x9F1KAI/sessions" || !strings.Contains(start["body"].(string), "https://acme.my.salesforce.com") {
		t.Fatalf("start %+v", start)
	}
	auth := res.Block["auth"].(map[string]any)
	if auth["token_url"] != "https://acme.my.salesforce.com/services/oauth2/token" || auth["client_id_secret"] != "SF_ID" {
		t.Fatalf("auth %+v", auth)
	}
}

func TestAgentforceRefusesBadIDs(t *testing.T) {
	c := &Client{}
	for _, tc := range []Request{
		{Platform: Agentforce, AgentID: "0XxHn000000x9F1' OR Id != '", Inputs: map[string]string{"my_domain": "acme.my.salesforce.com"}, Secrets: map[string]string{SecretClientID: "A", SecretClientSecret: "B"}},
		{Platform: Agentforce, AgentID: "0XxHn000000x9F1KAI", Inputs: map[string]string{"my_domain": "evil.example.com"}, Secrets: map[string]string{SecretClientID: "A", SecretClientSecret: "B"}},
	} {
		if _, err := c.Import(context.Background(), tc, map[string]string{"A": "aaaa", "B": "bbbb"}); err == nil {
			t.Fatalf("want refusal for %+v", tc)
		}
	}
}

func TestCopilotStudioImport(t *testing.T) {
	f := newFake(t)
	f.on("POST directline.botframework.com/v3/directline/tokens/generate", func(w http.ResponseWriter, r *http.Request) {
		wantHeader(t, r, "Authorization", "Bearer dl-secret")
		sendJSON(w, map[string]any{"conversationId": "c1", "token": "t1", "expires_in": 3600})
	})
	f.on("POST login.microsoftonline.com/11111111-2222-3333-4444-555555555555/oauth2/v2.0/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("scope") != "https://contoso.crm.dynamics.com/.default" {
			t.Errorf("scope %q", r.Form.Get("scope"))
		}
		sendJSON(w, map[string]any{"access_token": "entra-token"})
	})
	f.on("GET contoso.crm.dynamics.com/api/data/v9.2/bots", func(w http.ResponseWriter, r *http.Request) {
		wantHeader(t, r, "Authorization", "Bearer entra-token")
		if r.URL.Query().Get("$filter") != "schemaname eq 'cr4a1_supportAgent'" {
			t.Errorf("filter %q", r.URL.Query().Get("$filter"))
		}
		sendJSON(w, map[string]any{"value": []any{map[string]any{"botid": "b-1", "name": "Support Copilot", "schemaname": "cr4a1_supportAgent", "language": 1033, "statecode": 0}}})
	})
	res := mustImport(t, f.client(), Request{Platform: CopilotStudio, AgentID: "cr4a1_supportAgent",
		Inputs:  map[string]string{"environment_url": "https://contoso.crm.dynamics.com", "tenant_id": "11111111-2222-3333-4444-555555555555"},
		Secrets: map[string]string{SecretDirectLine: "DL", SecretClientID: "EID", SecretClientSecret: "ESEC"}},
		map[string]string{"DL": "dl-secret", "EID": "entra-id", "ESEC": "entra-secret"})
	if res.Definition.Name != "Support Copilot" || res.Definition.Language != "1033" {
		t.Fatalf("definition %+v", res.Definition)
	}
	if res.Block["auth_secret"] != "DL" || res.Preset != "copilot_studio" {
		t.Fatalf("block %+v", res.Block)
	}
	if res.Block["mapping"].(map[string]any)["poll"] == nil {
		t.Fatal("want a poll step")
	}
}

func TestCopilotStudioSecretOnly(t *testing.T) {
	f := newFake(t)
	f.on("POST directline.botframework.com/v3/directline/tokens/generate", func(w http.ResponseWriter, r *http.Request) {
		sendJSON(w, map[string]any{"token": "t1"})
	})
	res := mustImport(t, f.client(), Request{Platform: CopilotStudio, AgentID: "cr4a1_x", Secrets: map[string]string{SecretDirectLine: "DL"}}, map[string]string{"DL": "dl-secret"})
	if res.Definition.Name != "cr4a1_x" || len(res.Warnings) == 0 {
		t.Fatalf("res %+v", res)
	}
}

func testServiceAccount(t *testing.T) (string, *rsa.PublicKey) {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(k)
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	b, _ := json.Marshal(map[string]string{"type": "service_account", "client_email": "bench@my-project.iam.gserviceaccount.com",
		"private_key": string(pemKey), "private_key_id": "kid1", "project_id": "my-project"})
	return string(b), &k.PublicKey
}

func googleTokenRoute(t *testing.T, pub *rsa.PublicKey) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		parts := strings.Split(r.Form.Get("assertion"), ".")
		if len(parts) != 3 {
			w.WriteHeader(400)
			return
		}
		sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
		sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
			t.Errorf("assertion signature: %v", err)
			w.WriteHeader(400)
			return
		}
		cb, _ := base64.RawURLEncoding.DecodeString(parts[1])
		var claims map[string]any
		json.Unmarshal(cb, &claims)
		if claims["scope"] != CloudPlatformScope || claims["aud"] != GoogleTokenURL {
			t.Errorf("claims %v", claims)
		}
		sendJSON(w, map[string]any{"access_token": "ya29.test", "expires_in": 3599})
	}
}

func TestVertexImportADK(t *testing.T) {
	f := newFake(t)
	sa, pub := testServiceAccount(t)
	f.on("POST oauth2.googleapis.com/token", googleTokenRoute(t, pub))
	f.on("GET us-central1-aiplatform.googleapis.com/v1/projects/my-project/locations/us-central1/reasoningEngines/987", func(w http.ResponseWriter, r *http.Request) {
		wantHeader(t, r, "Authorization", "Bearer ya29.test")
		sendJSON(w, map[string]any{"name": "projects/1/locations/us-central1/reasoningEngines/987", "displayName": "Travel planner", "description": "ADK agent",
			"spec": map[string]any{"agentFramework": "google-adk", "classMethods": []any{
				map[string]any{"name": "stream_query", "api_mode": "stream"}, map[string]any{"name": "create_session", "api_mode": ""}}}})
	})
	res := mustImport(t, f.client(), Request{Platform: Vertex, AgentID: "987", Inputs: map[string]string{"region": "us-central1"},
		Secrets: map[string]string{SecretServiceAccount: "GSA"}}, map[string]string{"GSA": sa})
	if res.Definition.Name != "Travel planner" || res.Definition.Protocol != "google-adk" {
		t.Fatalf("definition %+v", res.Definition)
	}
	if !strings.HasSuffix(res.Block["url"].(string), "/reasoningEngines/987:streamQuery?alt=sse") {
		t.Fatalf("url %v", res.Block["url"])
	}
	m := res.Block["mapping"].(map[string]any)
	if m["stream"] != "sse" || !strings.Contains(m["body"].(string), `"class_method":"stream_query"`) {
		t.Fatalf("mapping %+v", m)
	}
}

func TestVertexImportQuery(t *testing.T) {
	f := newFake(t)
	sa, pub := testServiceAccount(t)
	f.on("POST oauth2.googleapis.com/token", googleTokenRoute(t, pub))
	f.on("GET europe-west4-aiplatform.googleapis.com/v1/projects/p-12345/locations/europe-west4/reasoningEngines/55", func(w http.ResponseWriter, r *http.Request) {
		sendJSON(w, map[string]any{"displayName": "LangChain agent", "spec": map[string]any{"agentFramework": "langchain", "classMethods": []any{map[string]any{"name": "query", "api_mode": ""}}}})
	})
	res := mustImport(t, f.client(), Request{Platform: Vertex, AgentID: "projects/p-12345/locations/europe-west4/reasoningEngines/55",
		Secrets: map[string]string{SecretServiceAccount: "GSA"}}, map[string]string{"GSA": sa})
	if !strings.HasSuffix(res.Block["url"].(string), ":query") || res.Block["mapping"].(map[string]any)["answer_path"] != "output.output" {
		t.Fatalf("block %+v", res.Block)
	}
}

func TestLangGraphImportByGraphID(t *testing.T) {
	f := newFake(t)
	f.on("GET my-dep.us.langgraph.app/assistants/agent", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(422)
		io.WriteString(w, `{"detail":"invalid uuid"}`)
	})
	f.on("POST my-dep.us.langgraph.app/assistants/search", func(w http.ResponseWriter, r *http.Request) {
		wantHeader(t, r, "X-Api-Key", "lsv2_key")
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["graph_id"] != "agent" {
			t.Errorf("search body %v", body)
		}
		sendJSON(w, []any{map[string]any{"assistant_id": "fe096781-5601-53d2-b2f6-0d3403f7e9ca", "graph_id": "agent", "name": "agent", "version": 2}})
	})
	f.on("GET my-dep.us.langgraph.app/assistants/fe096781-5601-53d2-b2f6-0d3403f7e9ca/schemas", func(w http.ResponseWriter, r *http.Request) {
		sendJSON(w, map[string]any{"input_schema": map[string]any{"properties": map[string]any{"messages": map[string]any{}, "user_id": map[string]any{}}}})
	})
	res := mustImport(t, f.client(), Request{Platform: LangGraph, AgentID: "agent", Inputs: map[string]string{"deployment_url": "https://my-dep.us.langgraph.app/runs/wait"},
		Secrets: map[string]string{SecretAPIKey: "LS"}}, map[string]string{"LS": "lsv2_key"})
	if res.Block["url"] != "https://my-dep.us.langgraph.app/runs/wait" || res.Block["auth_header"] != "x-api-key" || res.Block["auth_secret"] != "LS" {
		t.Fatalf("block %+v", res.Block)
	}
	if !strings.Contains(res.Block["mapping"].(map[string]any)["body"].(string), `"assistant_id": "fe096781-5601-53d2-b2f6-0d3403f7e9ca"`) {
		t.Fatalf("mapping %+v", res.Block["mapping"])
	}
	if res.Definition.Fields["input_fields"] != "messages, user_id" || len(res.Warnings) != 0 {
		t.Fatalf("definition %+v warnings %v", res.Definition, res.Warnings)
	}
}

func TestLangGraphUserURLChecked(t *testing.T) {
	c := &Client{CheckURL: func(string) error { return io.ErrUnexpectedEOF }}
	_, err := c.Import(context.Background(), Request{Platform: LangGraph, AgentID: "agent", Inputs: map[string]string{"deployment_url": "http://10.0.0.1"}}, nil)
	if err == nil || err.Error() != io.ErrUnexpectedEOF.Error() {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckRequest(t *testing.T) {
	for _, tc := range []struct {
		req  Request
		want string
	}{
		{Request{Platform: "nope", AgentID: "x"}, "unknown platform"},
		{Request{Platform: Vapi}, "needs the assistant id"},
		{Request{Platform: Vapi, AgentID: "a"}, "needs the Vapi private key"},
		{Request{Platform: Vapi, AgentID: "a", Secrets: map[string]string{SecretAPIKey: "bad name"}}, "must be letters"},
		{Request{Platform: Vapi, AgentID: "a", Secrets: map[string]string{SecretAPIKey: "K", "other": "X"}}, "takes no secret"},
		{Request{Platform: LangGraph, AgentID: "a"}, "needs deployment url"},
		{Request{Platform: Vapi, AgentID: "a", Inputs: map[string]string{"x": "1"}, Secrets: map[string]string{SecretAPIKey: "K"}}, "takes no input"},
	} {
		_, err := Check(tc.req)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: err = %v, want %q", tc.req, err, tc.want)
		}
	}
	if _, err := (&Client{}).Import(context.Background(), Request{Platform: Vapi, AgentID: "a", Secrets: map[string]string{SecretAPIKey: "K"}}, nil); err == nil || !strings.Contains(err.Error(), "secret K is not set") {
		t.Fatalf("missing secret: %v", err)
	}
}

func TestAgentYAMLShapes(t *testing.T) {
	res := &Result{Platform: Vapi, AgentID: "asst-1", Adapter: "voice_platform", FetchedAt: "2026-11-22T10:00:00Z",
		Definition: Definition{Name: "Support"}, Block: map[string]any{"platform": "vapi", "agent_id": "asst-1", "api_key_secret": "K"}}
	y, err := AgentYAML("support-line", res, AgentDefaults{Model: "gpt-5-mini", Provider: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(y), "# Imported from Vapi asst-1 (Support) on 2026-11-22.\n") {
		t.Fatalf("head %q", y)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(y, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["id"] != "support-line" || doc["adapter"] != "voice_platform" || doc["voice_platform"].(map[string]any)["agent_id"] != "asst-1" || doc["model"] != "gpt-5-mini" {
		t.Fatalf("doc %v", doc)
	}
	side, err := SidecarJSON(res)
	if err != nil || !strings.Contains(string(side), `"platform": "vapi"`) {
		t.Fatalf("sidecar %s %v", side, err)
	}
}

func TestEveryPlatformHasAnImporter(t *testing.T) {
	for _, p := range Platforms {
		if importers[p.ID] == nil {
			t.Errorf("platform %s has no importer", p.ID)
		}
		if len(p.Secrets) == 0 || p.IDLabel == "" || p.Docs == "" {
			t.Errorf("platform %s is missing form fields", p.ID)
		}
		for _, s := range p.Secrets {
			if !secretNameRe.MatchString(s.Suggested) {
				t.Errorf("platform %s secret %s suggests %q", p.ID, s.Role, s.Suggested)
			}
		}
	}
}
