//go:build integration

package tau2

// Real-docker test of the service image and the verifier on real upstream
// data. It needs a clone at bench/datasets/tau2-bench (see
// docs/importing-tau2.md) and builds the service image, which needs network
// the first time. Every container, network and image it makes is named
// hd-tau-* and removed at the end.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/importers"
)

const testImage = "hd-tau-service:test"

func docker(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func randHex(t *testing.T) string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

// realImport imports a few real tasks from the local clone.
func realImport(t *testing.T, ids []string) string {
	t.Helper()
	from, err := filepath.Abs(filepath.Join("..", "..", "datasets", "tau2-bench"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(from, "data", "tau2", "domains")); err != nil {
		t.Skipf("no tau2-bench clone at %s", from)
	}
	root := t.TempDir()
	im := &importer{now: time.Now}
	res, err := im.Import(context.Background(), importers.Options{BenchRoot: root, From: from, SuiteID: "tau2-it", IDs: ids,
		Extra: map[string]string{SetNL: NLUnscored, SetTaskSet: TaskSetAll}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tasks) != len(ids) {
		t.Fatalf("imported %v, skipped %+v", res.Tasks, res.Skipped)
	}
	return res.SuiteDir
}

// fixture is one service container on its own network, reachable from the
// host on a random port.
type fixture struct {
	t        *testing.T
	net, svc string
	url      string
}

func startService(t *testing.T, taskDir string) *fixture {
	t.Helper()
	id := randHex(t)
	f := &fixture{t: t, net: "hd-tau-net-" + id, svc: "hd-tau-svc-" + id}
	docker(t, "network", "create", "--label", "bifrost-bench=1", f.net)
	t.Cleanup(func() {
		exec.Command("docker", "rm", "-f", f.svc).Run()
		exec.Command("docker", "network", "rm", f.net).Run()
	})
	docker(t, "create", "--name", f.svc, "--label", "bifrost-bench=1", "--network", f.net, "--network-alias", "tau2",
		"-p", "127.0.0.1::8000", testImage,
		"python", "/opt/tau2svc/tau2svc.py", "serve", "--scenario", "/task/scenario.json", "--state", "/state", "--port", "8000")
	docker(t, "cp", filepath.Join(taskDir, "scenario", "scenario.json"), f.svc+":/task/scenario.json")
	docker(t, "start", f.svc)
	port := docker(t, "port", f.svc, "8000/tcp")
	f.url = "http://" + port
	deadline := time.Now().Add(120 * time.Second)
	for {
		resp, err := http.Get(f.url + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return f
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("service not ready: %v\n%s", err, docker(t, "logs", f.svc))
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func (f *fixture) rpc(method string, params any) map[string]any {
	f.t.Helper()
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		f.t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, f.url+"/mcp", bytes.NewReader(body))
	if err != nil {
		f.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		f.t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		f.t.Fatalf("%s: %v: %s", method, err, b)
	}
	if doc["error"] != nil {
		f.t.Fatalf("%s: %v", method, doc["error"])
	}
	return doc["result"].(map[string]any)
}

func (f *fixture) call(name string, args map[string]any) (string, bool) {
	res := f.rpc("tools/call", map[string]any{"name": name, "arguments": args})
	var text strings.Builder
	for _, c := range res["content"].([]any) {
		text.WriteString(c.(map[string]any)["text"].(string))
	}
	return text.String(), res["isError"].(bool)
}

// runControl runs a control script in a plain Python container on the
// service's network, as the bench runs it in the agent's place.
func (f *fixture) runControl(script string) string {
	f.t.Helper()
	name := "hd-tau-ctl-" + randHex(f.t)
	defer exec.Command("docker", "rm", "-f", name).Run()
	docker(f.t, "create", "--name", name, "--label", "bifrost-bench=1", "--network", f.net,
		"-e", `BENCH_MCP_SERVERS={"tau2":{"url":"http://tau2:8000/mcp"}}`, "python:3.12-slim", "sh", "/control.sh")
	docker(f.t, "cp", script, name+":/control.sh")
	out, err := exec.Command("docker", "start", "-a", name).CombinedOutput()
	if err != nil {
		f.t.Fatalf("control %s: %v\n%s", filepath.Base(script), err, out)
	}
	return string(out)
}

// verify copies the service's state out and runs the verifier in the same
// image with no network, returning reward.json.
func (f *fixture) verify(taskDir string) map[string]any {
	f.t.Helper()
	state := f.t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, serviceID), 0o755); err != nil {
		f.t.Fatal(err)
	}
	for _, n := range []string{"db.json", "transcript.json"} {
		docker(f.t, "cp", f.svc+":/state/"+n, filepath.Join(state, serviceID, n))
	}
	name := "hd-tau-ver-" + randHex(f.t)
	defer exec.Command("docker", "rm", "-f", name).Run()
	docker(f.t, "create", "--name", name, "--label", "bifrost-bench=1", "--network", "none", testImage,
		"python", "/opt/tau2svc/tau2svc.py", "verify", "--tests", "/grader/tests", "--state", "/grader/state/tau2", "--out", "/grader/out")
	// docker cp creates a missing destination folder from the source's content.
	docker(f.t, "cp", filepath.Join(taskDir, "verifier", "tests"), name+":/grader/tests")
	docker(f.t, "cp", state, name+":/grader/state")
	if out, err := exec.Command("docker", "start", "-a", name).CombinedOutput(); err != nil {
		f.t.Fatalf("verify: %v\n%s", err, out)
	}
	out := f.t.TempDir()
	docker(f.t, "cp", name+":/grader/out/reward.json", filepath.Join(out, "reward.json"))
	docker(f.t, "cp", name+":/grader/out/details.json", filepath.Join(out, "details.json"))
	if d, err := os.ReadFile(filepath.Join(out, "details.json")); err != nil || strings.Contains(string(d), "differs") {
		f.t.Fatalf("details: %v\n%s", err, d)
	}
	b, err := os.ReadFile(filepath.Join(out, "reward.json"))
	if err != nil {
		f.t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		f.t.Fatal(err)
	}
	return doc
}

func buildImage(t *testing.T, taskDir string) {
	t.Helper()
	docker(t, "build", "-q", "-t", testImage, filepath.Join(taskDir, "service"))
	t.Cleanup(func() {
		if os.Getenv("HD_TAU_KEEP_IMAGE") == "" {
			exec.Command("docker", "rmi", testImage).Run()
		}
	})
}

func TestServiceRawClient(t *testing.T) {
	dir := realImport(t, []string{"airline-7"})
	task := filepath.Join(dir, "tasks", "airline-7")
	buildImage(t, task)
	f := startService(t, task)

	init := f.rpc("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "raw", "version": "1"}})
	if init["protocolVersion"] != "2025-06-18" {
		t.Fatalf("initialize = %v", init)
	}
	tools := f.rpc("tools/list", map[string]any{})["tools"].([]any)
	names := map[string]bool{}
	for _, tl := range tools {
		m := tl.(map[string]any)
		names[m["name"].(string)] = true
		if _, ok := m["inputSchema"].(map[string]any); !ok {
			t.Fatalf("tool %v has no inputSchema", m["name"])
		}
	}
	for _, want := range []string{"respond_to_user", "get_user_details", "get_reservation_details", "cancel_reservation", "update_reservation_flights"} {
		if !names[want] {
			t.Fatalf("tools/list lacks %s: %v", want, names)
		}
	}
	reply, isErr := f.call("respond_to_user", map[string]any{"message": "Hi! How can I help you today?"})
	if isErr || !strings.Contains(reply, "Why I am contacting you") {
		t.Fatalf("opening reply = %q (error %v)", reply, isErr)
	}
	text, isErr := f.call("get_reservation_details", map[string]any{"reservation_id": "NOPE00"})
	if !isErr || !strings.Contains(text, "Error") {
		t.Fatalf("bad reservation = %q (error %v)", text, isErr)
	}
	text, isErr = f.call("no_such_tool", map[string]any{})
	if !isErr {
		t.Fatalf("unknown tool = %q, want an error", text)
	}
	reply, _ = f.call("respond_to_user", map[string]any{"message": "Is there anything else I can help you with?"})
	if !strings.Contains(reply, "###STOP###") {
		t.Fatalf("closing reply = %q", reply)
	}
	reply, _ = f.call("respond_to_user", map[string]any{"message": "Hello?"})
	if !strings.Contains(reply, "left the conversation") {
		t.Fatalf("after stop = %q", reply)
	}
	// As upstream, nothing runs once the customer stopped.
	text, isErr = f.call("get_user_details", map[string]any{"user_id": "mia_li_3668"})
	if !isErr || !strings.Contains(text, "conversation has ended") {
		t.Fatalf("tool after stop = %q (error %v)", text, isErr)
	}
	// Export: the state files exist and the transcript has the conversation.
	state := t.TempDir()
	docker(t, "cp", f.svc+":/state/transcript.json", filepath.Join(state, "transcript.json"))
	b, err := os.ReadFile(filepath.Join(state, "transcript.json"))
	if err != nil {
		t.Fatal(err)
	}
	var tr struct {
		EndedBy string           `json:"ended_by"`
		Events  []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(b, &tr); err != nil {
		t.Fatal(err)
	}
	if tr.EndedBy != "###STOP###" || len(tr.Events) != 8 {
		t.Fatalf("transcript ended_by %q with %d events: %s", tr.EndedBy, len(tr.Events), b)
	}
}

func TestControlsAndVerifier(t *testing.T) {
	// airline-7 changes the database and must communicate a refund; airline-3
	// only communicates; retail-5 changes the database (retail scores DB and,
	// when it has them, natural-language assertions, which stay unscored).
	ids := []string{"airline-7", "airline-3", "retail-5"}
	dir := realImport(t, ids)
	buildImage(t, filepath.Join(dir, "tasks", ids[0]))
	for _, id := range ids {
		task := filepath.Join(dir, "tasks", id)
		controls := []struct {
			name string
			want float64
		}{{"reference.sh", 1}, {"reference.sh", 1}, {"empty.sh", 0}, {"mutant1.sh", 0}, {"exploit.sh", 0}}
		for i, c := range controls {
			t.Run(fmt.Sprintf("%s/%d-%s", id, i, c.name), func(t *testing.T) {
				f := startService(t, task)
				out := f.runControl(filepath.Join(task, "controls", c.name))
				rw := f.verify(task)
				if rw["reward"].(float64) != c.want {
					t.Fatalf("reward = %v, want %v\ncontrol output:\n%s", rw, c.want, out)
				}
			})
		}
	}
}

func TestPythonUnit(t *testing.T) {
	name := "hd-tau-pyunit-" + randHex(t)
	defer exec.Command("docker", "rm", "-f", name).Run()
	docker(t, "create", "--name", name, "--label", "bifrost-bench=1", "--network", "none", "python:3.12-slim",
		"sh", "-c", "cd /t && python -B -m unittest -v tau2svc_test")
	docker(t, "cp", "image", name+":/t")
	if out, err := exec.Command("docker", "start", "-a", name).CombinedOutput(); err != nil {
		t.Fatalf("python unit tests: %v\n%s", err, out)
	}
}
