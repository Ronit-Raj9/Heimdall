package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/connect"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/schemas"
)

func TestAgentVerifyJobRows(t *testing.T) {
	code := regexp.MustCompile(`HMD-[A-Z0-9]{5}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-verify-1" {
			w.WriteHeader(401)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"answer":%q}`, code.FindString(fmt.Sprint(body["instruction"])))
	}))
	defer srv.Close()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	agent := `id: ep
adapter: remote_http
image: none
argv: []
env: {}
model: m
notes: {project_file: x}
budgets: {wall_s: 600, cpus: 1, mem_gb: 1, turns: 10}
gateway: {provider: openai}
remote:
  url: ` + srv.URL + `/run
  auth_env: VERIFY_TEST_TOKEN
`
	if err := os.WriteFile(filepath.Join(root, "agents", "ep.yaml"), []byte(agent), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VERIFY_TEST_TOKEN", "tok-verify-1")
	env := jobEnv{ProjectRoot: root}
	raw := json.RawMessage(`{"agent":"agents/ep.yaml"}`)
	if _, _, err := validateAgentVerify(context.Background(), env, raw); err != nil {
		t.Fatal(err)
	}
	if _, _, err := validateAgentVerify(context.Background(), env, json.RawMessage(`{"agent":"../x.yaml"}`)); err == nil {
		t.Fatal("a path outside the project must be refused")
	}
	var progress []any
	log := &opLog{}
	out, err := runAgentVerify(context.Background(), env, &jobs.Job{Params: raw}, log, func(p any) { progress = append(progress, p) })
	if err != nil {
		t.Fatalf("verify: %v\n%s", err, log.text())
	}
	res := out.(*connect.VerifyResult)
	if !res.OK || res.Row(connect.RowAnswer).Status != connect.StatusPass || res.Row(connect.RowRealTask) != nil {
		t.Fatalf("rows %+v", res.Rows)
	}
	if len(progress) < 3 || strings.Contains(log.text(), "tok-verify-1") {
		t.Fatalf("progress %d, log %s", len(progress), log.text())
	}
	if verifyScripted(&schemas.AgentConfig{Remote: &schemas.RemoteAgent{URL: "x"}}) {
		t.Fatal("an endpoint on its own model cannot use the scripted model")
	}
}
