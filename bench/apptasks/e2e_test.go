package apptasks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/taskchecks"
)

// TestWrittenControlsAgainstRealMock runs every written task's reference
// and controls against the real app copy (mockapi/server.py with the
// generated mock.json and seed) and the real user simulator, and grades
// them with the written checks.yaml: what readiness proves in containers,
// here with python3 on the host. The reference passes; every control fails.
func TestWrittenControlsAgainstRealMock(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	st, d, m := newDraft(t)
	a := &Assistant{Chat: &fakeChat{}, Model: Pinned{Provider: "openai", Model: "test-model"}, Budget: &Budget{MaxCalls: 50, Price: price}}
	_, policy, _ := st.Files(d)
	if err := DraftRules(context.Background(), a, d, m, policy); err != nil {
		t.Fatal(err)
	}
	_ = ApproveRules(d, m, d.Rules, "ana")
	if err := GenerateTasks(context.Background(), a, d, m, policy, GenerateOptions{}); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	dir := filepath.Join(root, d.Suite)
	written, err := WriteBenchmark(dir, st, d, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	svc := filepath.Join(dir, "services", d.App)
	for _, w := range written {
		taskDir := filepath.Join(dir, "tasks", w.ID)
		var ty struct {
			Controls struct {
				Reference string   `yaml:"reference"`
				Empty     string   `yaml:"empty"`
				Mutants   []string `yaml:"mutants"`
				Exploit   string   `yaml:"exploit"`
			} `yaml:"controls"`
		}
		raw, _ := os.ReadFile(filepath.Join(taskDir, "task.yaml"))
		if err := yaml.Unmarshal(raw, &ty); err != nil {
			t.Fatal(err)
		}
		cb, _ := os.ReadFile(filepath.Join(taskDir, "verifier", "checks.yaml"))
		spec, err := taskchecks.Parse(cb)
		if err != nil {
			t.Fatalf("%s: %v", w.ID, err)
		}
		run := func(control string) (bool, string) {
			return runControl(t, svc, taskDir, control, d.App, spec)
		}
		if ok, why := run(ty.Controls.Reference); !ok {
			t.Errorf("%s: reference fails: %s", w.ID, why)
		}
		others := append([]string{ty.Controls.Empty, ty.Controls.Exploit}, ty.Controls.Mutants...)
		for _, c := range others {
			if ok, why := run(c); ok {
				t.Errorf("%s: %s passes: %s", w.ID, c, why)
			}
		}
		if len(ty.Controls.Mutants) == 0 {
			t.Errorf("%s: no mutants", w.ID)
		}
	}
}

func runControl(t *testing.T, svc, taskDir, control, app string, spec *taskchecks.Spec) (bool, string) {
	t.Helper()
	state := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ap, cp := freePort(t), freePort(t)
	mock := exec.CommandContext(ctx, "python3", "-I", filepath.Join(svc, "server.py"))
	mock.Env = append(os.Environ(), fmt.Sprintf("PORT=%d", ap), "MOCK_PATH="+filepath.Join(svc, "mock.json"),
		"SEED_PATH="+filepath.Join(taskDir, "seed", app+".json"), "STATE_PATH="+filepath.Join(state, app, "state.json"),
		"RECORD_PATH="+filepath.Join(state, app, "calls.jsonl"))
	cust := exec.CommandContext(ctx, "python3", "-I", filepath.Join(svc, "simuser.py"), "serve", "--config", filepath.Join(taskDir, "user.json"),
		"--state", filepath.Join(state, "customer"), "--port", fmt.Sprint(cp))
	var wg sync.WaitGroup
	for _, c := range []*exec.Cmd{mock, cust} {
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func(c *exec.Cmd) { defer wg.Done(); _ = c.Wait() }(c)
	}
	defer func() { cancel(); wg.Wait() }()
	waitHealthy(t, ap)
	waitHealthy(t, cp)
	lp := &ledger{}
	as := httptest.NewServer(lp.handler(app, fmt.Sprintf("http://127.0.0.1:%d/mcp", ap)))
	defer as.Close()
	cs := httptest.NewServer(lp.handler("customer", fmt.Sprintf("http://127.0.0.1:%d/mcp", cp)))
	defer cs.Close()
	servers, _ := json.Marshal(map[string]any{app: map[string]string{"url": as.URL}, "customer": map[string]string{"url": cs.URL}})
	cmd := exec.Command("sh", filepath.Join(taskDir, control))
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "BENCH_MCP_SERVERS="+string(servers))
	if out, err := cmd.CombinedOutput(); err != nil && filepath.Base(control) == "reference.sh" {
		t.Fatalf("reference: %v\n%s", err, out)
	}
	graded := &taskchecks.Spec{Version: spec.Version, PassThreshold: spec.PassThreshold}
	for _, c := range spec.Checks {
		if c.ReplyRubric == "" {
			graded.Checks = append(graded.Checks, c)
		}
	}
	res := taskchecks.Evaluate(graded, taskchecks.Inputs{Calls: taskchecks.CallsFrom(lp.rows), State: taskchecks.NewDirState(state)})
	_, passed, invalid, reason := taskchecks.Score(graded, res)
	if invalid {
		t.Fatalf("%s: invalid: %s", control, reason)
	}
	return passed, reason
}

// ledger forwards MCP calls to a service and records tools/call rows as
// the tool proxy does.
type ledger struct {
	mu   sync.Mutex
	rows []schemas.ToolCall
}

func (l *ledger) handler(server, target string) http.Handler {
	u, _ := url.Parse(target)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var msg struct {
			Method string `json:"method"`
			Params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			} `json:"params"`
		}
		_ = json.Unmarshal(body, &msg)
		req, _ := http.NewRequest(r.Method, u.String(), bytes.NewReader(body))
		req.Header = r.Header.Clone()
		resp, err := proxyClient.Do(req)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		defer resp.Body.Close()
		out, _ := io.ReadAll(resp.Body)
		if msg.Method == "tools/call" {
			l.mu.Lock()
			l.rows = append(l.rows, schemas.ToolCall{Seq: len(l.rows) + 1, Server: server, Tool: msg.Params.Name, Arguments: string(msg.Params.Arguments),
				Protocol: "mcp", IsError: bytes.Contains(out, []byte(`"isError":true`)) || bytes.Contains(out, []byte(`"isError": true`))})
			l.mu.Unlock()
		}
		for k, v := range resp.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(out)
	})
}

// proxyClient opens a fresh connection per call: the stdlib server of the
// mock closes idle ones, which a reused connection would see as a 502.
var proxyClient = &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func waitHealthy(t *testing.T, port int) {
	for i := 0; i < 200; i++ {
		if r, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", port)); err == nil {
			r.Body.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("service on %d never came up", port)
}
