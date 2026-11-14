package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// fakeProxy is a fault proxy control API that answers /health and, when
// upstreams is set, GET /upstreams with that body and status. It counts the
// upstream checks it receives.
type fakeProxy struct {
	upstreams       string // response body; empty: the route does not exist (older proxy)
	upstreamsStatus int
	token           string
	checks          atomic.Int64
}

func (f *fakeProxy) handler(t *testing.T) http.Handler {
	writeBody := func(w http.ResponseWriter, status int, body string) {
		w.WriteHeader(status)
		if _, err := w.Write([]byte(body)); err != nil {
			t.Errorf("fake proxy write: %v", err)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		writeBody(w, http.StatusOK, `{"status":"ok"}`)
	})
	if f.upstreams != "" {
		mux.HandleFunc("GET /upstreams", func(w http.ResponseWriter, r *http.Request) {
			f.checks.Add(1)
			if r.Header.Get("Authorization") != "Bearer "+f.token {
				writeBody(w, http.StatusUnauthorized, `{"error":"control token required"}`)
				return
			}
			writeBody(w, f.upstreamsStatus, f.upstreams)
		})
	}
	return mux
}

func TestFaultProxyChecks(t *testing.T) {
	const okReport = `{"checked_at":"2026-11-14T10:00:00Z","upstreams":[` +
		`{"provider":"anthropic","host":"api.anthropic.com:443","ok":true,"latency_ms":41.6},` +
		`{"provider":"openai","host":"api.openai.com:443","ok":false,"latency_ms":5000,` +
		`"error":"dial tcp: lookup api.openai.com on 127.0.0.11:53: server misbehaving"},` +
		`{"provider":"groq","host":"api.groq.com:443","ok":false,"latency_ms":0,"skipped":"upstream traffic goes through the HTTP proxy at corp:3128; not dialled"}]}`
	pass := func(id, detail string) schemas.DoctorCheck {
		return schemas.DoctorCheck{ID: id, Status: schemas.DoctorPass, Detail: detail}
	}
	row := func(id string, st schemas.DoctorStatus, detail string) schemas.DoctorCheck {
		return schemas.DoctorCheck{ID: id, Status: st, Detail: detail}
	}
	tests := []struct {
		name       string
		proxy      *fakeProxy // nil: nothing listens at the control URL
		token      string
		want       []schemas.DoctorCheck // the fault proxy row is checked by status only when its detail is "*"
		wantChecks int64
	}{
		{name: "upstream rows per provider", proxy: &fakeProxy{upstreams: okReport, upstreamsStatus: 200, token: "tok"}, token: "tok",
			wantChecks: 1, want: []schemas.DoctorCheck{
				pass("fault proxy", "*"),
				pass("upstream anthropic", "api.anthropic.com:443, 42 ms"),
				row("upstream openai", schemas.DoctorFail, "dial tcp: lookup api.openai.com on 127.0.0.11:53: server misbehaving"),
				row("upstream groq", schemas.DoctorSkip, "api.groq.com:443: upstream traffic goes through the HTTP proxy at corp:3128; not dialled"),
			}},
		{name: "older proxy warns once", proxy: &fakeProxy{}, token: "tok", want: []schemas.DoctorCheck{
			pass("fault proxy", "*"),
			row("upstreams", schemas.DoctorWarn, "proxy too old for upstream check"),
		}},
		{name: "missing token warns without calling", proxy: &fakeProxy{upstreams: okReport, upstreamsStatus: 200, token: "tok"},
			want: []schemas.DoctorCheck{
				pass("fault proxy", "*"),
				row("fault proxy token", schemas.DoctorWarn, "BENCH_FAULTPROXY_TOKEN is not set; plans cannot be armed"),
				row("upstreams", schemas.DoctorWarn, "BENCH_FAULTPROXY_TOKEN is not set; upstream check skipped"),
			}},
		{name: "wrong token fails", proxy: &fakeProxy{upstreams: okReport, upstreamsStatus: 200, token: "tok"}, token: "nope",
			wantChecks: 1, want: []schemas.DoctorCheck{
				pass("fault proxy", "*"),
				row("upstreams", schemas.DoctorFail, "fault proxy upstream check: status 401: control token required"),
			}},
		{name: "scripted only", proxy: &fakeProxy{upstreams: `{"checked_at":"2026-11-14T10:00:00Z","upstreams":[]}`, upstreamsStatus: 200, token: "tok"},
			token: "tok", wantChecks: 1, want: []schemas.DoctorCheck{
				pass("fault proxy", "*"),
				row("upstreams", schemas.DoctorSkip, "no provider has a real key in the fault proxy; the scripted model answers"),
			}},
		{name: "proxy down", token: "tok", want: []schemas.DoctorCheck{
			row("fault proxy", schemas.DoctorFail, "*"),
			row("upstreams", schemas.DoctorSkip, "fault proxy is not reachable"),
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			controlURL := "http://" + closedLoopback(t)
			if tt.proxy != nil {
				srv := httptest.NewServer(tt.proxy.handler(t))
				defer srv.Close()
				controlURL = srv.URL
			}
			got := faultProxyChecks(context.Background(), controlURL, tt.token)
			if len(got) == len(tt.want) && got[0].Status == tt.want[0].Status && tt.want[0].Detail == "*" {
				got[0].Detail = "*"
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("rows:\n got  %+v\n want %+v", got, tt.want)
			}
			if tt.proxy != nil && tt.proxy.checks.Load() != tt.wantChecks {
				t.Fatalf("upstream checks = %d, want %d", tt.proxy.checks.Load(), tt.wantChecks)
			}
		})
	}
}

// closedLoopback returns a loopback address nothing listens on.
func closedLoopback(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

func TestImageNeeds(t *testing.T) {
	root := t.TempDir()
	py := &schemas.Suite{ID: "pyapp", BaseImage: schemas.ImageSpec{Dockerfile: "base/Dockerfile", Tag: "acme/pyapp-base:1"}}
	web := &schemas.Suite{ID: "webapp", BaseImage: schemas.ImageSpec{Tag: "acme/node:22"}}
	perSuite := &schemas.AgentConfig{ID: "pybot", Image: "acme/pybot:1",
		Build: &schemas.ImageSpec{Dockerfile: "agents/images/pybot/Dockerfile", Tag: "acme/pybot:1", BaseArg: "BASE_IMAGE"}}
	broken := &schemas.AgentConfig{ID: "broken", Image: "acme/broken",
		Build: &schemas.ImageSpec{Dockerfile: "agents/images/broken/Dockerfile", Tag: "acme/broken", BaseArg: "BASE_IMAGE"}}
	agents := []doctorAgent{
		{file: "agents/oracle.yaml", cfg: &schemas.AgentConfig{ID: "oracle", Image: "acme/helper:1"}},
		{file: "agents/codex.yaml", cfg: &schemas.AgentConfig{ID: "codex", Image: "bench/agent-codex:1",
			Build: &schemas.ImageSpec{Dockerfile: "agents/images/codex/Dockerfile", Tag: "bench/agent-codex:1"}}},
		{file: "agents/pybot.yaml", cfg: perSuite},
		{file: "agents/broken.yaml", cfg: broken},
	}
	needs, rows := imageNeeds(root, "acme/helper:1", []*schemas.Suite{py, web}, agents)
	want := []imageNeed{
		{tag: "acme/helper:1", hint: "run make -C bench images"},
		{tag: "acme/pyapp-base:1", hint: "bifrost-bench suite admit pyapp builds it from its base_image.dockerfile"},
		{tag: "acme/node:22", hint: "build or pull it; suite webapp names it as base_image.tag"},
		{tag: "bench/agent-codex:1", hint: "bifrost-bench agent build agents/codex.yaml"},
		{tag: "acme/pybot:1-pyapp", hint: "bifrost-bench agent build agents/pybot.yaml -suite pyapp"},
		{tag: "acme/pybot:1-webapp", hint: "bifrost-bench agent build agents/pybot.yaml -suite webapp"},
	}
	if !reflect.DeepEqual(needs, want) {
		t.Fatalf("needs:\n got  %+v\n want %+v", needs, want)
	}
	// The tag without a tag part cannot carry a suite suffix: one warning per suite.
	if len(rows) != 2 || rows[0].ID != "agent agents/broken.yaml" || rows[0].Status != schemas.DoctorWarn {
		t.Fatalf("rows = %+v", rows)
	}

	// A project folder from `init` has the helper's Dockerfile, so the hint builds it.
	if err := os.MkdirAll(filepath.Join(root, "deploy", "helper"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "deploy", "helper", "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	needs, _ = imageNeeds(root, "acme/helper:1", nil, nil)
	if len(needs) != 1 || needs[0].hint != "docker build -t acme/helper:1 deploy/helper" {
		t.Fatalf("needs = %+v", needs)
	}
}

func TestSuiteRow(t *testing.T) {
	tasks := []*schemas.Task{{ID: "t01"}, {ID: "t02"}}
	admitted := map[string]schemas.TaskLock{"t01": {Status: schemas.TaskAdmitted}, "t02": {Status: schemas.TaskAdmitted}}
	half := map[string]schemas.TaskLock{"t01": {Status: schemas.TaskAdmitted}}
	tests := []struct {
		name string
		s    *schemas.Suite
		want schemas.DoctorCheck
	}{
		{"no lock", &schemas.Suite{ID: "web", Tasks: tasks}, schemas.DoctorCheck{ID: "suite web", Status: schemas.DoctorFail, Detail: "no suite.lock; run suite admit web"}},
		{"all admitted", &schemas.Suite{ID: "web", Tasks: tasks, Lock: &schemas.SuiteLock{Tasks: admitted}},
			schemas.DoctorCheck{ID: "suite web", Status: schemas.DoctorPass, Detail: "2 of 2 tasks admitted"}},
		{"some admitted", &schemas.Suite{ID: "web", Tasks: tasks, Lock: &schemas.SuiteLock{Tasks: half}},
			schemas.DoctorCheck{ID: "suite web", Status: schemas.DoctorWarn, Detail: "1 of 2 tasks admitted"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := suiteRow(tt.s); got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestHelperToolsResult(t *testing.T) {
	st, detail := helperToolsResult("acme/node:22", []string{"python3"})
	if st != schemas.DoctorFail || detail != "helper image acme/node:22 has no python3; set images.helper to the bench helper image" {
		t.Fatalf("got %s %q", st, detail)
	}
	st, detail = helperToolsResult("bench/coding-core-base:1", nil)
	if st != schemas.DoctorPass || detail != "sh, cp, git, python3, sleep, getent present" {
		t.Fatalf("got %s %q", st, detail)
	}
}

// TestHelperToolsCheck runs the doctor row against a fake docker that prints
// what a broken helper image prints: the row names the missing tool, the same
// self-test the egress probe runs.
func TestHelperToolsCheck(t *testing.T) {
	tests := []struct {
		name   string
		script string // body of the fake docker
		want   schemas.DoctorCheck
	}{
		{"all present", `exit 0`,
			schemas.DoctorCheck{ID: "helper tools", Status: schemas.DoctorPass, Detail: "sh, cp, git, python3, sleep, getent present"}},
		{"no getent", `echo "missing getent"`,
			schemas.DoctorCheck{ID: "helper tools", Status: schemas.DoctorFail, Detail: "helper image acme/node:22 has no getent; set images.helper to the bench helper image"}},
		{"command not found", `echo "sh: 1: python3: command not found" >&2; exit 127`,
			schemas.DoctorCheck{ID: "helper tools", Status: schemas.DoctorFail, Detail: "helper image acme/node:22 has no python3; set images.helper to the bench helper image"}},
		{"no sh", `echo 'docker: Error response from daemon: exec: "sh": executable file not found in $PATH' >&2; exit 127`,
			schemas.DoctorCheck{ID: "helper tools", Status: schemas.DoctorFail, Detail: "helper image acme/node:22 has no sh; set images.helper to the bench helper image"}},
		{"cannot run", `echo "No such image: acme/node:22" >&2; exit 125`,
			schemas.DoctorCheck{ID: "helper tools", Status: schemas.DoctorFail, Detail: "helper self-test in acme/node:22 exited 125: No such image: acme/node:22"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), "docker")
			if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+tt.script+"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			a := &app{cfg: &lib.Config{Images: lib.ImagesConfig{Helper: "acme/node:22"}}, docker: utils.Docker{Binary: bin}}
			if got := a.helperToolsCheck(context.Background()); got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
