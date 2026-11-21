package sandbox

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type fakeE2B struct {
	mu          sync.Mutex
	srv         *httptest.Server
	templates   int
	builds      int
	statusReads int
	created     map[string]any
	start       map[string]any
	files       map[string][]byte
	killed      int
}

func newFakeE2B(t *testing.T) *fakeE2B {
	f := &fakeE2B{files: map[string][]byte{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		envd := strings.HasPrefix(r.URL.Path, "/envd/")
		if envd {
			if r.Header.Get("X-Access-Token") != "envd-token-xyz" || r.Header.Get("E2b-Sandbox-Id") != "isb1" || r.Header.Get("E2b-Sandbox-Port") != "49983" {
				http.Error(w, "bad envd auth", http.StatusUnauthorized)
				return
			}
			if r.Header.Get("Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("root:")) {
				http.Error(w, "not root", http.StatusUnauthorized)
				return
			}
		} else if r.Header.Get("X-API-Key") != testToken {
			http.Error(w, "bad key", http.StatusUnauthorized)
			return
		}
		switch p := r.URL.Path; {
		case r.Method == http.MethodGet && p == "/sandboxes":
			_, _ = io.WriteString(w, `[]`)
		case r.Method == http.MethodPost && p == "/v3/templates":
			f.templates++
			_, _ = io.WriteString(w, `{"templateID":"tpl1","buildID":"b1","public":false,"aliases":[],"names":[],"tags":[]}`)
		case r.Method == http.MethodPost && p == "/v2/templates/tpl1/builds/b1":
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in["fromImage"] != "reg/agent@sha256:1" {
				t.Errorf("build body = %v", in)
			}
			f.builds++
			w.WriteHeader(http.StatusAccepted)
		case p == "/templates/tpl1/builds/b1/status":
			f.statusReads++
			st := "building"
			if f.statusReads > 1 {
				st = "ready"
			}
			_, _ = io.WriteString(w, `{"templateID":"tpl1","buildID":"b1","status":"`+st+`","logs":[],"logEntries":[]}`)
		case r.Method == http.MethodPost && p == "/v2/sandboxes":
			_ = json.NewDecoder(r.Body).Decode(&f.created)
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"sandboxID":"isb1","templateID":"tpl1","clientID":"c","envdVersion":"0.2","envdAccessToken":"envd-token-xyz"}`)
		case r.Method == http.MethodDelete && p == "/sandboxes/isb1":
			f.killed++
			if f.killed > 1 {
				http.Error(w, `{"code":404}`, http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case p == "/envd/process.Process/Start":
			if r.Header.Get("Content-Type") != "application/connect+json" || r.Header.Get("Connect-Protocol-Version") != "1" {
				t.Errorf("start headers = %v", r.Header)
			}
			body, _ := io.ReadAll(r.Body)
			if len(body) < 5 || body[0] != 0 {
				t.Errorf("start frame = %q", body)
			}
			_ = json.Unmarshal(body[5:], &f.start)
			var out bytes.Buffer
			out.Write(connectFrame(0, []byte(`{"event":{"start":{"pid":7}}}`)))
			out.Write(connectFrame(0, []byte(`{"event":{"data":{"stdout":"`+base64.StdEncoding.EncodeToString([]byte("out "))+`"}}}`)))
			out.Write(connectFrame(0, []byte(`{"event":{"keepalive":{}}}`)))
			out.Write(connectFrame(0, []byte(`{"event":{"data":{"stderr":"`+base64.StdEncoding.EncodeToString([]byte("err"))+`"}}}`)))
			out.Write(connectFrame(0, []byte(`{"event":{"end":{"exitCode":2,"exited":true,"status":"exit status 2"}}}`)))
			out.Write(connectFrame(2, []byte(`{}`)))
			w.Header().Set("Content-Type", "application/connect+json")
			_, _ = w.Write(out.Bytes())
		case p == "/envd/files" && r.URL.Query().Get("username") == "root":
			path := r.URL.Query().Get("path")
			if r.Method == http.MethodPost {
				b, _ := io.ReadAll(r.Body)
				f.files[path] = b
				_, _ = io.WriteString(w, `[{"path":"`+path+`","name":"x","type":"file"}]`)
				return
			}
			b, ok := f.files[path]
			if !ok {
				http.Error(w, "missing", http.StatusNotFound)
				return
			}
			_, _ = w.Write(b)
		default:
			http.Error(w, "unexpected "+r.Method+" "+p, http.StatusTeapot)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func TestE2BLifecycle(t *testing.T) {
	f := newFakeE2B(t)
	old := e2bPollEvery
	e2bPollEvery = 0
	defer func() { e2bPollEvery = old }()
	e := NewE2B(Credentials{Token: testToken, Endpoint: f.srv.URL}, nil)
	ctx := context.Background()
	if err := e.Check(ctx); err != nil {
		t.Fatal(err)
	}
	spec := Spec{Image: "reg/agent@sha256:1", Egress: Egress{AllowHosts: []string{"gw.example.com"}}, Env: map[string]string{"X": "1"}}
	box, err := e.Create(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	net, _ := f.created["network"].(map[string]any)
	if f.created["templateID"] != "tpl1" || f.created["allow_internet_access"] != true || net == nil {
		t.Fatalf("create body = %v", f.created)
	}
	if allow, _ := json.Marshal(net["allowOut"]); string(allow) != `["gw.example.com"]` {
		t.Fatalf("allowOut = %s", allow)
	}
	if _, err := e.Create(ctx, spec); err != nil || f.templates != 1 || f.builds != 1 {
		t.Fatalf("a second sandbox of the same image must reuse the template: %d templates, %d builds, %v", f.templates, f.builds, err)
	}
	res, err := e.Exec(ctx, box, Command{Argv: []string{"/bin/sh", "-c", "x"}, Env: map[string]string{"A": "b"}, Cwd: "/w"})
	if err != nil || res.ExitCode != 2 || string(res.Output) != "out err" {
		t.Fatalf("exec = %+v %v", res, err)
	}
	proc, _ := f.start["process"].(map[string]any)
	if proc["cmd"] != "/bin/sh" || proc["cwd"] != "/w" {
		t.Fatalf("start body = %v", f.start)
	}
	if err := e.Upload(ctx, box, "/tmp/a", []byte("abc")); err != nil {
		t.Fatal(err)
	}
	if b, err := e.Download(ctx, box, "/tmp/a", 3); err != nil || string(b) != "abc" {
		t.Fatalf("download = %q %v", b, err)
	}
	if err := e.Destroy(ctx, box); err != nil {
		t.Fatal(err)
	}
	if err := e.Destroy(ctx, box); err != nil {
		t.Fatalf("second destroy: %v", err)
	}
}

func TestE2BBlockedNetworkAndStreamError(t *testing.T) {
	internet, net := e2bNetwork(Egress{BlockAll: true})
	if internet || net["allowOut"] != nil {
		t.Fatalf("block all = %v %v", internet, net)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(connectFrame(2, []byte(`{"error":{"code":"invalid_argument","message":"no such file"}}`)))
	}))
	defer srv.Close()
	e := NewE2B(Credentials{Token: testToken, Endpoint: srv.URL}, nil)
	_, err := e.Exec(context.Background(), &Box{ID: "isb1", Data: map[string]string{}}, Command{Argv: []string{"nope"}})
	if err == nil || !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("stream error = %v", err)
	}
	bad := NewE2B(Credentials{Token: "bad-token-123456", Endpoint: newFakeE2B(t).srv.URL}, nil)
	if err := bad.Check(context.Background()); !errors.Is(err, ErrUnauthorized) || strings.Contains(err.Error(), "bad-token-123456") {
		t.Fatalf("bad token = %v", err)
	}
}
