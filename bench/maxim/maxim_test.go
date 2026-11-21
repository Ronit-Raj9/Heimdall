package maxim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/traceexport"
)

const testKey = "mx-live-key-0123456789"

// fakeMaxim speaks GET /v1/log-repositories and POST /v1/otel with the
// x-maxim-api-key header, as the public API documents them.
type fakeMaxim struct {
	mu     sync.Mutex
	bodies [][]byte
	repos  []string
	fail   int // status for /v1/otel when set
}

func (f *fakeMaxim) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-maxim-api-key") != testKey {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"bad key"}`)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/log-repositories":
			if r.URL.Query().Get("workspaceId") != "ws1" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			var data []map[string]string
			for _, id := range f.repos {
				if want := r.URL.Query().Get("id"); want == "" || want == id {
					data = append(data, map[string]string{"id": id, "name": "Repo " + id, "workspaceId": "ws1"})
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/otel":
			if r.Header.Get("x-maxim-repo-id") != "repo1" || r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("otel headers %v", r.Header)
			}
			if f.fail != 0 {
				w.WriteHeader(f.fail)
				_, _ = io.WriteString(w, "echo "+testKey)
				return
			}
			b, _ := io.ReadAll(r.Body)
			f.mu.Lock()
			f.bodies = append(f.bodies, b)
			f.mu.Unlock()
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func runFiles(t *testing.T) traceexport.ReadFunc {
	t.Helper()
	started := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	run := schemas.Run{RunID: "r_1", ExpID: "e_1", SlotID: "s1", Outcome: schemas.OutcomePass, Timing: schemas.RunTiming{Started: started}}
	files := map[string][]byte{"seal.json": []byte(`{"run_id":"r_1"}`)}
	b, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	files["run.json"] = b
	return func(name string) ([]byte, error) {
		if b, ok := files[name]; ok {
			return b, nil
		}
		return nil, fmt.Errorf("%s: %w", name, fs.ErrNotExist)
	}
}

func TestSettingsValidateAndLink(t *testing.T) {
	ok := Settings{WorkspaceID: "ws1", RepoID: "repo1"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := ok.Link("abc"); got != "https://app.getmaxim.ai/workspace/ws1/log-repository/repo1/logs?traceId=abc" {
		t.Fatalf("link %s", got)
	}
	custom := Settings{WorkspaceID: "ws1", RepoID: "repo1", LinkTemplate: "https://maxim.example.com/r/{repo_id}/t/{trace_id}"}
	if custom.Link("a b") != "https://maxim.example.com/r/repo1/t/a+b" {
		t.Fatalf("custom link %s", custom.Link("a b"))
	}
	for name, s := range map[string]Settings{
		"no workspace": {RepoID: "r"},
		"bad repo":     {WorkspaceID: "w", RepoID: "r/../x"},
		"http link":    {WorkspaceID: "w", RepoID: "r", LinkTemplate: "http://x.example.com/{trace_id}"},
		"no trace id":  {WorkspaceID: "w", RepoID: "r", LinkTemplate: "https://x.example.com/"},
		"script link":  {WorkspaceID: "w", RepoID: "r", LinkTemplate: "javascript:alert({trace_id})"},
	} {
		if err := s.Validate(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestStore(t *testing.T) {
	st := Open(t.TempDir())
	if s, err := st.Load(); err != nil || s != nil {
		t.Fatalf("empty load %v %v", s, err)
	}
	if err := st.Save(Settings{WorkspaceID: "ws1"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid save %v", err)
	}
	if err := st.Save(Settings{WorkspaceID: "ws1", RepoID: "repo1", AutoPush: true, KeyID: "k1"}); err != nil {
		t.Fatal(err)
	}
	s, err := st.Load()
	if err != nil || s == nil || !s.AutoPush || s.KeyID != "k1" {
		t.Fatalf("load %+v %v", s, err)
	}
	t0 := time.Now()
	for _, p := range []Push{
		{RunID: "a", Status: StatusFailed, Error: "boom\nline", At: t0},
		{RunID: "a", Status: StatusPushed, At: t0.Add(time.Second)},
		{RunID: "b", Status: StatusFailed, Error: strings.Repeat("x", 500), At: t0.Add(2 * time.Second)},
	} {
		if err := st.Record(p); err != nil {
			t.Fatal(err)
		}
	}
	pushes, pushed, failed, err := st.Pushes()
	if err != nil || pushed != 1 || failed != 1 || pushes["a"].Status != StatusPushed {
		t.Fatalf("pushes %v %d %d %v", pushes, pushed, failed, err)
	}
	if e := LastError(pushes); len(e) > maxErrorChars+3 || !strings.HasPrefix(e, "xxx") {
		t.Fatalf("last error %q", e)
	}
	if err := st.Delete(); err != nil {
		t.Fatal(err)
	}
	if s, _ := st.Load(); s != nil {
		t.Fatal("settings kept after delete")
	}
	if err := st.Delete(); err != nil {
		t.Fatalf("second delete %v", err)
	}
}

func TestClientTest(t *testing.T) {
	f := &fakeMaxim{repos: []string{"repo1"}}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	c := Client{APIBase: srv.URL}
	repo, err := c.Test(context.Background(), testKey, "ws1", "repo1")
	if err != nil || repo.Name != "Repo repo1" {
		t.Fatalf("test %+v %v", repo, err)
	}
	if _, err := c.Test(context.Background(), "wrong", "ws1", "repo1"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("bad key %v", err)
	}
	if _, err := c.Test(context.Background(), testKey, "ws1", "repo9"); err == nil || !strings.Contains(err.Error(), "repo9") {
		t.Fatalf("missing repo %v", err)
	}
	if _, err := c.Test(context.Background(), testKey, "ws2", "repo1"); err == nil {
		t.Fatal("wrong workspace accepted")
	}
}

func TestPushRun(t *testing.T) {
	f := &fakeMaxim{repos: []string{"repo1"}}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	c := Client{APIBase: srv.URL}
	st := Open(t.TempDir())
	s := Settings{WorkspaceID: "ws1", RepoID: "repo1"}
	in := RunInput{RunID: "r_1", Slot: schemas.Slot{SlotID: "s1", ExpID: "e_1", Task: "t1", Arm: "A"}, Files: runFiles(t), Auto: true}
	p, err := PushRun(context.Background(), c, st, s, testKey, in, time.Now())
	if err != nil || p.Status != StatusPushed || p.TraceID != traceexport.TraceID("r_1") || !p.Auto {
		t.Fatalf("push %+v %v", p, err)
	}
	if len(f.bodies) != 1 {
		t.Fatalf("bodies %d", len(f.bodies))
	}
	var td traceexport.TracesData
	if err := json.Unmarshal(f.bodies[0], &td); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, rs := range td.ResourceSpans {
		for _, ss := range rs.ScopeSpans {
			for _, sp := range ss.Spans {
				if v, ok := traceexport.Attr(sp.Attributes, "maxim.metadata"); ok {
					found = strings.Contains(v.String(), `"maxim-session-id":"heimdall-e_1"`)
				}
			}
		}
	}
	if !found {
		t.Fatalf("no maxim.metadata with the session on the root: %s", f.bodies[0])
	}
	// A refused push is recorded as failed, and the key never lands in the error.
	f.fail = http.StatusBadGateway
	p, err = PushRun(context.Background(), c, st, s, testKey, in, time.Now())
	if err == nil || p.Status != StatusFailed || strings.Contains(p.Error, testKey) || !strings.Contains(p.Error, "502") {
		t.Fatalf("failed push %+v %v", p, err)
	}
	pushes, pushed, failed, _ := st.Pushes()
	if pushed != 0 || failed != 1 || pushes["r_1"].Status != StatusFailed {
		t.Fatalf("record %v", pushes)
	}
	if _, err := PushRun(context.Background(), c, st, s, "", in, time.Now()); err == nil {
		t.Fatal("push without a key")
	}
}
