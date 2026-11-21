package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/maxim"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/traceexport"
	"github.com/valyala/fasthttp"
)

const maximTestKey = "mx-handler-key-98765"

// maximFake answers the repository list and the OTLP ingest for one key.
func maximFake(t *testing.T, posts *int, mu *sync.Mutex) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-maxim-api-key") != maximTestKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v1/log-repositories":
			_, _ = io.WriteString(w, `{"data":[{"id":"repo1","name":"Prod agents","workspaceId":"ws1"}]}`)
		case "/v1/otel":
			mu.Lock()
			*posts++
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func maximRouter(env OrgEnv) fasthttp.RequestHandler {
	h := &maximHandler{env: env}
	r := router.New()
	r.GET(APIPrefix+"/integrations/maxim", env.R(h.get))
	r.PUT(APIPrefix+"/integrations/maxim", env.W(h.put))
	r.DELETE(APIPrefix+"/integrations/maxim", env.W(h.remove))
	r.POST(APIPrefix+"/integrations/maxim/test", env.W(h.test))
	r.GET(APIPrefix+"/runs/{id}/maxim", env.R(h.runGet))
	r.POST(APIPrefix+"/runs/{id}/maxim/push", env.W(h.runPush))
	return r.Handler
}

func TestMaximRoutes(t *testing.T) {
	var posts int
	var mu sync.Mutex
	srv := maximFake(t, &posts, &mu)
	defer srv.Close()
	old := MaximClient
	MaximClient = maxim.Client{APIBase: srv.URL}
	defer func() { MaximClient = old }()

	ks, err := keystore.Open(nil, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	runJSON, _ := json.Marshal(schemas.Run{RunID: "r1", ExpID: "e1", Outcome: schemas.OutcomePass, Timing: schemas.RunTiming{Started: time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)}})
	ix := &fakeIndex{
		manifests: map[string]*schemas.Manifest{"e1": {ExpID: "e1", HeldOutTasks: []string{"hidden"}}},
		runs: []RunView{
			{Slot: schemas.Slot{ExpID: "e1", Task: "t1"}, Run: schemas.Run{RunID: "r1"}, Sealed: true, SealHash: "abc"},
			{Slot: schemas.Slot{ExpID: "e1", Task: "t1"}, Run: schemas.Run{RunID: "r2"}, Sealed: false},
			{Slot: schemas.Slot{ExpID: "e1", Task: "hidden"}, Run: schemas.Run{RunID: "r3"}, Sealed: true},
		},
		files: map[string]map[string][]byte{"r1": {"run.json": runJSON, "seal.json": []byte(`{}`)}},
	}
	root := t.TempDir()
	env := OrgEnv{Cfg: &lib.Config{Root: root}, Mode: testMode, Services: Services{OrgID: "o1", Keys: ks, Index: ix}}
	h := maximRouter(env)
	call := func(method, path, body string) (int, string) { return secretsCall(h, method, path, body) }

	if st, body := call("GET", APIPrefix+"/integrations/maxim", ""); st != 200 || !strings.Contains(body, `"connected":false`) {
		t.Fatalf("empty: %d %s", st, body)
	}
	if st, body := call("GET", APIPrefix+"/runs/r1/maxim", ""); st != 200 || !strings.Contains(body, `"connected":false`) {
		t.Fatalf("run before connect: %d %s", st, body)
	}
	for _, bad := range []string{
		`{"workspace_id":"ws1","repo_id":"repo1"}`, // no key yet
		`{"api_key":"k","workspace_id":"","repo_id":"repo1"}`,
		`{"api_key":"k","workspace_id":"ws1","repo_id":"repo1","link_template":"http://x.example.com/{trace_id}"}`,
		`{"api_key":"k","workspace_id":"ws1","repo_id":"repo1","extra":1}`,
	} {
		if st, body := call("PUT", APIPrefix+"/integrations/maxim", bad); st != fasthttp.StatusBadRequest {
			t.Fatalf("%s: %d %s", bad, st, body)
		}
	}
	st, body := call("PUT", APIPrefix+"/integrations/maxim", `{"api_key":"`+maximTestKey+`","workspace_id":"ws1","repo_id":"repo1","auto_push":true}`)
	if st != 200 || strings.Contains(body, maximTestKey) || !strings.Contains(body, `"key_last4":"8765"`) || !strings.Contains(body, `"connected":true`) {
		t.Fatalf("save: %d %s", st, body)
	}
	// The key is the server's own: the org secrets list does not show it.
	keys, _ := ks.List(t.Context(), "o1", keystore.KindSecret)
	if len(keys) != 1 || !systemSecret(keys[0]) {
		t.Fatalf("stored keys %+v", keys)
	}
	// Saving again without a key keeps it.
	if st, body = call("PUT", APIPrefix+"/integrations/maxim", `{"workspace_id":"ws1","repo_id":"repo1","auto_push":false}`); st != 200 || !strings.Contains(body, `"key_last4":"8765"`) {
		t.Fatalf("resave: %d %s", st, body)
	}
	if st, body = call("POST", APIPrefix+"/integrations/maxim/test", ""); st != 200 || !strings.Contains(body, `"ok":true`) || !strings.Contains(body, "Prod agents") {
		t.Fatalf("test: %d %s", st, body)
	}

	// Push: a sealed run goes, an unsealed or held-out one does not.
	if st, body = call("POST", APIPrefix+"/runs/r1/maxim/push", ""); st != 200 || !strings.Contains(body, `"status":"pushed"`) {
		t.Fatalf("push: %d %s", st, body)
	}
	if posts != 1 {
		t.Fatalf("posts %d", posts)
	}
	var rm RunMaxim
	_, body = call("GET", APIPrefix+"/runs/r1/maxim", "")
	_ = json.Unmarshal([]byte(body), &rm)
	if rm.Status != maxim.StatusPushed || rm.TraceID != traceexport.TraceID("r1") || !strings.Contains(rm.URL, "/workspace/ws1/log-repository/repo1/logs?traceId="+rm.TraceID) {
		t.Fatalf("run view %+v", rm)
	}
	if st, _ := call("POST", APIPrefix+"/runs/r2/maxim/push", ""); st != fasthttp.StatusConflict {
		t.Fatalf("unsealed push: %d", st)
	}
	if st, _ := call("POST", APIPrefix+"/runs/r3/maxim/push", ""); st != fasthttp.StatusConflict {
		t.Fatalf("held-out push: %d", st)
	}
	if st, _ := call("POST", APIPrefix+"/runs/nope/maxim/push", ""); st != fasthttp.StatusNotFound {
		t.Fatalf("missing run push: %d", st)
	}
	if st, _ := call("GET", APIPrefix+"/runs/nope/maxim", ""); st != fasthttp.StatusNotFound {
		t.Fatalf("missing run view: %d", st)
	}
	// The seal hook sends only when automatic sending is on (it is off now).
	if p, err := MaximPushSealed(t.Context(), env.Services, root, "r1", true); p != nil || err != nil {
		t.Fatalf("auto push while off: %+v %v", p, err)
	}

	// A wrong key: the test says so, the push is recorded as failed.
	if st, _ = call("PUT", APIPrefix+"/integrations/maxim", `{"api_key":"wrong-key-1","workspace_id":"ws1","repo_id":"repo1","auto_push":true}`); st != 200 {
		t.Fatalf("rotate: %d", st)
	}
	if _, body = call("POST", APIPrefix+"/integrations/maxim/test", ""); !strings.Contains(body, `"ok":false`) {
		t.Fatalf("bad key test: %s", body)
	}
	if p, err := MaximPushSealed(t.Context(), env.Services, root, "r1", true); err == nil || p == nil || p.Status != maxim.StatusFailed {
		t.Fatalf("auto push with a bad key: %+v %v", p, err)
	}
	if _, body = call("GET", APIPrefix+"/integrations/maxim", ""); !strings.Contains(body, `"failed":1`) || !strings.Contains(body, "last_error") {
		t.Fatalf("counts: %s", body)
	}

	if st, body = call("DELETE", APIPrefix+"/integrations/maxim", ""); st != 200 || !strings.Contains(body, `"connected":false`) {
		t.Fatalf("delete: %d %s", st, body)
	}
	if keys, _ := ks.List(t.Context(), "o1", keystore.KindSecret); len(keys) != 0 {
		t.Fatalf("key kept after disconnect: %+v", keys)
	}
	if st, _ := call("POST", APIPrefix+"/integrations/maxim/test", ""); st != fasthttp.StatusConflict {
		t.Fatalf("test when disconnected: %d", st)
	}
}

func TestMaximNoKeyStore(t *testing.T) {
	h := maximRouter(OrgEnv{Cfg: &lib.Config{Root: t.TempDir()}, Mode: testMode})
	if st, body := secretsCall(h, "GET", APIPrefix+"/integrations/maxim", ""); st != fasthttp.StatusServiceUnavailable || !strings.Contains(body, "master key") {
		t.Fatalf("no key store: %d %s", st, body)
	}
}
