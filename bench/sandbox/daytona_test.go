package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const testToken = "secret-token-1234567890"

// fakeDaytona is the Daytona API and toolbox on one server.
type fakeDaytona struct {
	mu      sync.Mutex
	srv     *httptest.Server
	created daytonaCreate
	reads   int
	files   map[string][]byte
	execs   []map[string]any
	deleted int
}

func newFakeDaytona(t *testing.T) *fakeDaytona {
	f := &fakeDaytona{files: map[string][]byte{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			http.Error(w, `{"message":"bad key `+r.Header.Get("Authorization")+`"}`, http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/sandbox":
			_, _ = io.WriteString(w, `[]`)
		case r.Method == http.MethodPost && r.URL.Path == "/sandbox":
			if err := json.NewDecoder(r.Body).Decode(&f.created); err != nil {
				t.Errorf("create body: %v", err)
			}
			_, _ = io.WriteString(w, `{"id":"sb1","state":"creating"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/sandbox/sb1":
			f.reads++
			state := "pending_build"
			if f.reads > 1 {
				state = "started"
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "sb1", "state": state, "toolboxProxyUrl": f.srv.URL + "/toolbox"})
		case r.Method == http.MethodDelete && r.URL.Path == "/sandbox/sb1":
			f.deleted++
			if f.deleted > 1 {
				http.Error(w, "gone", http.StatusNotFound)
			}
		case r.URL.Path == "/toolbox/sb1/process/execute":
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			f.execs = append(f.execs, in)
			_, _ = io.WriteString(w, `{"exitCode":3,"result":"hello\n"}`)
		case r.URL.Path == "/toolbox/sb1/files/upload-v2":
			b, _ := io.ReadAll(r.Body)
			f.files[r.URL.Query().Get("path")] = b
			_, _ = io.WriteString(w, `{}`)
		case r.URL.Path == "/toolbox/sb1/files/download":
			b, ok := f.files[r.URL.Query().Get("path")]
			if !ok {
				http.Error(w, "no file", http.StatusNotFound)
				return
			}
			_, _ = w.Write(b)
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusTeapot)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func TestDaytonaLifecycle(t *testing.T) {
	f := newFakeDaytona(t)
	p, err := New(PlatformDaytona, Credentials{Token: testToken, Endpoint: f.srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	d := p.(*Daytona)
	ctx := context.Background()
	if err := d.Check(ctx); err != nil {
		t.Fatal(err)
	}
	old := daytonaPollEvery
	daytonaPollEvery = 0
	defer func() { daytonaPollEvery = old }()
	box, err := d.Create(ctx, Spec{Name: "heimdall-r1", Image: "registry.example/agent@sha256:abc", CPUs: 1.5, MemGB: 2,
		Egress: Egress{AllowHosts: []string{"gw.example.com"}}, Labels: map[string]string{"heimdall.run": "r1"}})
	if err != nil {
		t.Fatal(err)
	}
	c := f.created
	if c.BuildInfo == nil || c.BuildInfo.DockerfileContent != "FROM registry.example/agent@sha256:abc\n" || c.User != "root" ||
		c.CPU != 2 || c.Memory != 2 || c.NetworkBlockAll || c.DomainAllowList != "gw.example.com" || c.AutoDeleteInterval != 0 || c.AutoStopInterval < 5 {
		t.Fatalf("create body = %+v", c)
	}
	res, err := d.Exec(ctx, box, Command{Argv: []string{"/bin/sh", "-c", "echo 'it''s'"}, Env: map[string]string{"A": "1"}})
	if err != nil || res.ExitCode != 3 || string(res.Output) != "hello\n" {
		t.Fatalf("exec = %+v, %v", res, err)
	}
	if got := f.execs[0]["command"]; got != `/bin/sh -c 'echo '\''it'\'''\''s'\'''` {
		t.Fatalf("command = %v", got)
	}
	if err := d.Upload(ctx, box, "/tmp/x", []byte("data")); err != nil {
		t.Fatal(err)
	}
	if b, err := d.Download(ctx, box, "/tmp/x", 10); err != nil || string(b) != "data" {
		t.Fatalf("download = %q, %v", b, err)
	}
	if _, err := d.Download(ctx, box, "/tmp/x", 2); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("download over max: %v", err)
	}
	if err := d.Destroy(ctx, box); err != nil {
		t.Fatal(err)
	}
	if err := d.Destroy(ctx, box); err != nil {
		t.Fatalf("second destroy: %v", err)
	}
}

func TestDaytonaBlockAllAndBadToken(t *testing.T) {
	if b := daytonaBody(Spec{Image: "x", Egress: Egress{BlockAll: true}}); !b.NetworkBlockAll || b.DomainAllowList != "" {
		t.Fatalf("block all body = %+v", b)
	}
	if b := daytonaBody(Spec{Image: "x"}); !b.NetworkBlockAll {
		t.Fatal("an empty allow list must block everything")
	}
	f := newFakeDaytona(t)
	bad := "wrong-token-abcdefgh"
	d := NewDaytona(Credentials{Token: bad, Endpoint: f.srv.URL}, nil)
	err := d.Check(context.Background())
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("bad token: %v", err)
	}
	if strings.Contains(err.Error(), bad) {
		t.Fatalf("error leaks the token: %v", err)
	}
}
