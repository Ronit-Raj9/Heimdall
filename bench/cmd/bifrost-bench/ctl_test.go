package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/runctl"
)

func TestCtlUsage(t *testing.T) {
	var out, errw bytes.Buffer
	if code := ctlMain([]string{"exp"}, &out, &errw); code != exitUsage {
		t.Fatalf("code %d", code)
	}
	if code := ctlMain([]string{"run", "cancel", "e_1"}, &out, &errw); code != exitUsage {
		t.Fatalf("run cancel with one id: code %d", code)
	}
	o := &ctlOpts{set: map[string]bool{}}
	if _, err := o.patch(); err == nil {
		t.Fatal("empty set accepted")
	}
	o = &ctlOpts{set: map[string]bool{"pause-models": true, "concurrency": true}, models: "-", concurrency: 4}
	p, err := o.patch()
	if err != nil || *p.Concurrency != 4 || p.PausedModels == nil || len(*p.PausedModels) != 0 {
		t.Fatalf("patch %+v %v", p, err)
	}
}

// With -server, ctl is a thin client over the REST routes.
func TestCtlRemoteRoutes(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		got = append(got, r.Method+" "+r.URL.Path+" "+strings.TrimSpace(func() string { b, _ := json.Marshal(body); return string(b) }()))
		_ = json.NewEncoder(w).Encode(handlers.RunControls{ExpID: "e_1", State: &runctl.State{Settings: runctl.Settings{Mode: runctl.ModePaused, PausedBy: "ana"}}, Paused: true, InFlight: 1, LogVerified: true})
	}))
	defer srv.Close()
	run := func(args ...string) string {
		var out, errw bytes.Buffer
		if code := ctlMain(append(args, "-server", srv.URL), &out, &errw); code != exitOK {
			t.Fatalf("%v: code %d: %s", args, code, errw.String())
		}
		return out.String()
	}
	if out := run("exp", "pause", "e_1", "-reason", "outage"); !strings.Contains(out, "paused by ana") {
		t.Fatalf("pause output %q", out)
	}
	run("exp", "set", "e_1", "-concurrency", "3", "-total-usd", "20")
	run("run", "requeue", "e_1", "r_9")
	want := []string{
		`POST /api/bench/experiments/e_1/pause {"reason":"outage"}`,
		`PATCH /api/bench/experiments/e_1/controls {"concurrency":3,"total_usd":20}`,
		`POST /api/bench/experiments/e_1/runs/r_9/requeue {}`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestSlotOfRun(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "e_1", "slots", "s_7", "runs", "r_1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if s, err := slotOfRun(root, "e_1", "r_1"); err != nil || s != "s_7" {
		t.Fatalf("%q %v", s, err)
	}
	if _, err := slotOfRun(root, "e_1", "../x"); err == nil {
		t.Fatal("path id accepted")
	}
}
