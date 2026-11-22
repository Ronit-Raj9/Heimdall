package remotemap

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestPathEval(t *testing.T) {
	doc, _ := Decode([]byte(`{"output":[{"type":"reasoning","summary":[]},{"type":"message","content":[{"type":"output_text","text":"hello"},{"type":"refusal","refusal":"x"},{"type":"output_text","text":"world"}]}],
		"messages":[{"content":"a"},{"content":"b"}], "odd key": 3, "n": [1,2,3]}`))
	cases := map[string]string{
		"output[?(@.type=='message')].content[?(@.type=='output_text')].text": "hello\nworld",
		"$.messages[-1].content":            "b",
		"messages[0].content":               "a",
		"messages[*].content":               "a\nb",
		"['odd key']":                       "3",
		"n[5]":                              "",
		"output[?(@.type!='message')].type": "reasoning",
		"output[?(@.content)].type":         "message",
		"missing.deep":                      "",
	}
	for path, want := range cases {
		p, err := ParsePath(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if got := Text(p.Eval(doc), "\n"); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	obj, _ := Decode([]byte(`{"type":"text-delta","delta":"hi"}`))
	p, _ := ParsePath("[?(@.type=='text-delta')].delta")
	if got := Text(p.Eval(obj), ""); got != "hi" {
		t.Errorf("object filter = %q", got)
	}
	for _, bad := range []string{"", "a[", "a[?(@.x==)]", "a[?(x)]", "a..b", "a[x]"} {
		if _, err := ParsePath(bad); err == nil {
			t.Errorf("ParsePath(%q) accepted", bad)
		}
	}
}

func TestRenderBody(t *testing.T) {
	v := &Vars{Instruction: `say "hi"`, SessionID: "s1", SessionIndex: 2, Output: "answer", NewID: func() string { return "id1" }}
	b, err := RenderBody(`{"input":"{{instruction}}","n":"{{session_index}}","m":"{{messages}}","t":"tag {{session_id}}/{{uuid}}","meta":"{{metadata}}","g":"{{gateway}}"}`, v)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["input"] != `say "hi"` || got["n"] != float64(2) || got["t"] != "tag s1/id1" || got["g"] != nil {
		t.Fatalf("rendered %s", b)
	}
	if m := got["m"].([]any)[0].(map[string]any); m["role"] != "user" || m["content"] != `say "hi"` {
		t.Fatalf("messages %v", got["m"])
	}
	if got["meta"].(map[string]any)["session_id"] != "s1" {
		t.Fatalf("metadata %v", got["meta"])
	}
	if _, err := RenderBody(`{"x":"{{secret.TOKEN}}"}`, v); err == nil || !strings.Contains(err.Error(), "headers and query") {
		t.Fatalf("secret in body: %v", err)
	}
	if _, err := RenderBody(`{"x":"{{nope}}"}`, v); err == nil {
		t.Fatal("unknown placeholder accepted")
	}
	if _, err := RenderBody(`{"x":`, v); err == nil {
		t.Fatal("invalid JSON accepted")
	}
}

func TestCheck(t *testing.T) {
	ok := &schemas.RemoteAgent{URL: "https://agent.example.com/run", Mapping: &schemas.RemoteMapping{Body: `{"q":"{{instruction}}"}`, AnswerPath: "a.b",
		Headers: map[string]string{"X-Api-Key": "{{secret.KEY}}"}}}
	if err := Check(ok); err != nil {
		t.Fatal(err)
	}
	bad := []*schemas.RemoteAgent{
		{URL: "https://a.example.com/", Mapping: &schemas.RemoteMapping{Headers: map[string]string{"Host": "x"}}},
		{URL: "https://a.example.com/", Mapping: &schemas.RemoteMapping{Start: &schemas.RemoteStep{URL: "https://evil.example.com/s"}}},
		{URL: "https://a.example.com/", Mapping: &schemas.RemoteMapping{Body: `{"x":"{{start.id}}"}`}},
		{URL: "https://a.example.com/", Mapping: &schemas.RemoteMapping{Stream: "ws"}},
		{URL: "https://a.example.com/", Mapping: &schemas.RemoteMapping{Poll: &schemas.RemotePoll{URL: "/p"}}},
		{URL: "https://{{session_id}}.example.com/", Mapping: &schemas.RemoteMapping{}},
		{URL: "https://a.example.com/", Auth: &schemas.RemoteAuth{Type: "kerberos"}},
		{URL: "https://a.example.com/", Auth: &schemas.RemoteAuth{Type: schemas.RemoteAuthOAuth2, TokenURL: "http://login.example.com/t", ClientIDSecret: "A", ClientSecretSecret: "B"}},
		{URL: "https://a.example.com/", Auth: &schemas.RemoteAuth{Type: schemas.RemoteAuthSigV4, AccessKeySecret: "A", SecretKeySecret: "B"}},
		{URL: "https://a.example.com/", Auth: &schemas.RemoteAuth{ClientCertSecret: "C"}},
	}
	for i, r := range bad {
		if err := Check(r); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
	eg, err := EgressTargets(&schemas.RemoteAgent{URL: "https://agent.example.com/x", Auth: &schemas.RemoteAuth{Type: schemas.RemoteAuthEntra, TenantID: "contoso.onmicrosoft.com"}})
	if err != nil || len(eg) != 2 || eg[0] != "agent.example.com:443" || eg[1] != "login.microsoftonline.com:443" {
		t.Fatalf("egress %v %v", eg, err)
	}
}

func TestSecretNames(t *testing.T) {
	r := &schemas.RemoteAgent{AuthSecret: "TOK", Auth: &schemas.RemoteAuth{Type: schemas.RemoteAuthSigV4, AccessKeySecret: "AK", SecretKeySecret: "SK"},
		Mapping: &schemas.RemoteMapping{Headers: map[string]string{"X": "a {{secret.H1}} b {{ secret.H2 }}"}, Query: map[string]string{"k": "{{secret.Q}}"}}}
	got := strings.Join(r.SecretNames(), ",")
	for _, n := range []string{"TOK", "AK", "SK", "H1", "H2", "Q"} {
		if !strings.Contains(got, n) {
			t.Errorf("SecretNames %s lacks %s", got, n)
		}
	}
}

func TestReadSSE(t *testing.T) {
	body := "event: response.created\ndata: {\"type\":\"response.created\"}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"Hel\"}\n\n" +
		": keep-alive\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"lo\"}\n\n" +
		"data: [DONE]\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ignored\"}\n\n"
	p, _ := ParsePath("[?(@.type=='response.output_text.delta')].delta")
	text, n, err := ReadSSE(strings.NewReader(body), StreamReader{Path: p})
	if err != nil || text != "Hello" || n != 4 {
		t.Fatalf("text %q events %d err %v", text, n, err)
	}
	text, _, _ = ReadSSE(strings.NewReader("data: plain\n\ndata: text"), StreamReader{})
	if text != "plaintext" {
		t.Fatalf("plain = %q", text)
	}
}

func TestReadAWSEventStream(t *testing.T) {
	var buf bytes.Buffer
	for _, s := range []string{"Hello, ", "world"} {
		payload, _ := json.Marshal(map[string]string{"bytes": base64.StdEncoding.EncodeToString([]byte(s))})
		buf.Write(EncodeAWSEvent(map[string]string{":event-type": "chunk", ":message-type": "event", ":content-type": "application/json"}, payload))
	}
	buf.Write(EncodeAWSEvent(map[string]string{":event-type": "trace", ":message-type": "event"}, []byte(`{"trace":{}}`)))
	p, _ := ParsePath("bytes")
	text, n, err := ReadAWSEventStream(bytes.NewReader(buf.Bytes()), StreamReader{Path: p, Decode: "base64"})
	if err != nil || text != "Hello, world" || n != 3 {
		t.Fatalf("text %q n %d err %v", text, n, err)
	}
	exc := EncodeAWSEvent(map[string]string{":message-type": "exception", ":exception-type": "accessDeniedException"}, []byte(`{"message":"no access"}`))
	if _, _, err := ReadAWSEventStream(bytes.NewReader(exc), StreamReader{Path: p}); err == nil || !strings.Contains(err.Error(), "no access") {
		t.Fatalf("exception: %v", err)
	}
	broken := append([]byte{}, buf.Bytes()...)
	broken[20] ^= 0xff
	if _, _, err := ReadAWSEventStream(bytes.NewReader(broken), StreamReader{Path: p}); err == nil {
		t.Fatal("corrupt stream accepted")
	}
}

// TestSignV4Vector checks the signer against AWS's published example
// (IAM ListUsers, Signature Version 4 documentation).
func TestSignV4Vector(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://iam.amazonaws.com/?Action=ListUsers&Version=2010-05-08", nil)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	at := time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC)
	if err := SignV4(req, nil, SigV4Keys{AccessKey: "AKIDEXAMPLE", SecretKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", Region: "us-east-1", Service: "iam"}, at); err != nil {
		t.Fatal(err)
	}
	want := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/iam/aws4_request, SignedHeaders=content-type;host;x-amz-date, Signature=5d672d79c15b13162d9279b0855cfba6789a8edb4c82c400e06b5924a6f2b5d7"
	if got := req.Header.Get("Authorization"); got != want {
		t.Fatalf("Authorization\n got %s\nwant %s", got, want)
	}
}

func plainClient(*tls.Config) *http.Client { return &http.Client{Timeout: 10 * time.Second} }

func noSleep(context.Context, time.Duration) error { return nil }

func TestOAuth2TokenCachedAndRefreshed(t *testing.T) {
	fetches := 0
	tokSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("client_id") != "cid" || r.Form.Get("client_secret") != "csec" || r.Form.Get("scope") != "agents" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
			return
		}
		fetches++
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok" + string(rune('0'+fetches)), "expires_in": 120})
	}))
	defer tokSrv.Close()
	now := time.Unix(1000, 0)
	spec := &schemas.RemoteAuth{Type: schemas.RemoteAuthOAuth2, TokenURL: tokSrv.URL, ClientIDSecret: "CID", ClientSecretSecret: "CSEC", Scope: "agents"}
	// CheckAuth wants https; the test server is plain http, so build the
	// authorizer by hand after checking the https form.
	https := *spec
	https.TokenURL = "https://login.example.com/token"
	if err := CheckAuth(&https); err != nil {
		t.Fatal(err)
	}
	a := &Authorizer{spec: spec, secrets: map[string]string{"CID": "cid", "CSEC": "csec"}, client: http.DefaultClient, now: func() time.Time { return now }}
	for i := 0; i < 3; i++ {
		if tok, err := a.Token(context.Background()); err != nil || tok != "tok1" {
			t.Fatalf("token %q %v", tok, err)
		}
	}
	now = now.Add(61 * time.Second) // within a minute of expiry: fetch again
	if tok, _ := a.Token(context.Background()); tok != "tok2" || fetches != 2 {
		t.Fatalf("refresh: %q after %d fetches", tok, fetches)
	}
	a.secrets["CSEC"] = "wrong"
	a.token = ""
	if _, err := a.Token(context.Background()); err == nil || !strings.Contains(err.Error(), "invalid_client") || strings.Contains(err.Error(), "wrong") {
		t.Fatalf("bad secret: %v", err)
	}
}

func TestNewAuthorizerNeedsSecrets(t *testing.T) {
	spec := &schemas.RemoteAuth{Type: schemas.RemoteAuthSigV4, AccessKeySecret: "AK", SecretKeySecret: "SK", Region: "us-east-1", Service: "bedrock"}
	if _, err := NewAuthorizer(spec, map[string]string{"AK": "a"}, nil, nil); err == nil || !strings.Contains(err.Error(), "SK") {
		t.Fatalf("missing secret: %v", err)
	}
}
