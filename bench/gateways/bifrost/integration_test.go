//go:build integration

package bifrost

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// TestIntegrationSpikeStack runs the gateway against the spike stack
// (bench/spike/compose.yaml: Bifrost v2.5.1, Postgres 18, the spike fault
// proxy in scripted mode) with the deploy Bifrost config mounted in place of
// the spike one, so the deploy config is booted too. It uses its own compose
// project and free loopback ports, and removes every container, volume and
// network it created. Set BIFROST_CAPTURE_FIXTURES=<dir> to save the detail
// rows it fetched.
func TestIntegrationSpikeStack(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	st := startSpikeStack(ctx, t)
	g, err := New(Config{
		URL: st.url, SetupToken: st.token, FaultProxyURL: "http://faultproxy:9100", Model: testModel,
		Slots: []Slot{{Name: "slot-a", Value: "slot-a"}, {Name: "slot-b", Value: "slot-b"}}, MaxRetries: 1,
		EvalNetwork: st.project + "_evalnet", HelperImage: helperImage(ctx, t), Logger: testLogger{t},
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := g.Setup(ctx); err != nil {
			t.Fatalf("Setup %d: %v", i, err)
		}
	}
	checkPreflight(ctx, t, g)

	key, err := g.MintKey(ctx, runContext(utils.NewID("r", 10), testModel))
	if err != nil {
		t.Fatalf("MintKey: %v", err)
	}
	other, err := g.MintKey(ctx, runContext(utils.NewID("r", 10), testModel))
	if err != nil || other.KeySlot != "slot-b" {
		t.Fatalf("second MintKey: %v %+v", err, other)
	}
	if _, err := g.MintKey(ctx, runContext(utils.NewID("r", 10), testModel)); err == nil {
		t.Fatal("third MintKey succeeded with two slots")
	}
	if err := g.RevokeKey(ctx, other); err != nil {
		t.Fatalf("RevokeKey: %v", err)
	}

	st.sendTraffic(ctx, t, key)
	ledger := st.ledger(t, key.KeySlot)
	log, err := g.FetchRequests(ctx, key, schemas.FetchExpectation{LedgerEntries: ledger, PollEvery: 300 * time.Millisecond})
	if err != nil {
		t.Fatalf("FetchRequests: %v", err)
	}
	checkLog(t, log)
	captureFixtures(t, log)

	bogus := append(append([]schemas.LedgerEntry{}, ledger...), schemas.LedgerEntry{Slot: key.KeySlot, Kind: "message", Action: "pass", Status: 200, ResponseID: "msg_missing", ReqSHA256: "00"})
	partial, err := g.FetchRequests(ctx, key, schemas.FetchExpectation{LedgerEntries: bogus, Timeout: 2 * time.Second, PollEvery: 300 * time.Millisecond})
	if err != nil || partial.Complete || !strings.Contains(partial.Reason, "1 of 8 ledger entries have no row") || len(partial.Requests) != len(log.Requests) {
		t.Errorf("incomplete ledger: err %v complete %t reason %q", err, partial.Complete, partial.Reason)
	}

	for i := 0; i < 2; i++ {
		if err := g.RevokeKey(ctx, key); err != nil {
			t.Fatalf("RevokeKey %d: %v", i, err)
		}
	}
	stale, err := g.MintKey(ctx, runContext(utils.NewID("r", 10), testModel))
	if err != nil {
		t.Fatalf("MintKey after revoke: %v", err)
	}
	if n, err := g.RevokeStale(ctx, map[string]bool{}); err != nil || n != 1 {
		t.Errorf("RevokeStale = %d, %v", n, err)
	}
	if left, err := g.listVirtualKeys(ctx); err != nil || len(left) != 0 {
		t.Errorf("virtual keys left: %+v %v (stale %s)", left, err, stale.Name)
	}
}

func checkPreflight(ctx context.Context, t *testing.T, g *Gateway) {
	t.Helper()
	m, err := g.Preflight(ctx)
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	t.Logf("preflight: version %s config %s plugins %v settings %v problems %q", m.Version, m.ConfigSHA256[:12], m.Plugins, m.Settings, m.Problems)
	if m.Version != "v2.5.1" || len(m.Problems) != 0 || m.Settings["egress_probe"] != "blocked" || m.Settings["max_retries"] != "1" {
		t.Errorf("preflight manifest = %+v", m)
	}
}

func checkLog(t *testing.T, log *schemas.RequestLog) {
	t.Helper()
	if !log.Complete || log.Reason != "" || log.Dropped == nil {
		t.Fatalf("log complete %t reason %q dropped %v", log.Complete, log.Reason, log.Dropped)
	}
	kinds := map[string]int{}
	attempts := map[int]int{}
	tools := 0
	for _, r := range log.Requests {
		kinds[r.Kind]++
		if r.Kind != "message" {
			continue
		}
		attempts[len(r.Attempts)]++
		if r.Status == "success" && (r.Tokens == nil || r.ResponseID == "" || r.BifrostUSD == nil) {
			t.Errorf("success row %s lacks tokens, response id or cost", r.ID)
		}
		for _, tc := range r.ToolCalls {
			if tc.ID != "" && len(tc.ArgsHash) == 64 {
				tools++
			}
		}
	}
	if kinds["message"] != 4 || kinds["count_tokens"] != 1 || kinds["models"] != 1 || len(log.RowIDs) != 6 {
		t.Errorf("kinds = %v rows %d", kinds, len(log.RowIDs))
	}
	if attempts[1] != 2 || attempts[2] != 2 || tools < 1 {
		t.Errorf("attempt counts %v, tool calls %d", attempts, tools)
	}
}

func captureFixtures(t *testing.T, log *schemas.RequestLog) {
	t.Helper()
	dir := os.Getenv("BIFROST_CAPTURE_FIXTURES")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, r := range log.Requests {
		var buf bytes.Buffer
		if err := json.Indent(&buf, r.Raw, "", "  "); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, r.ID+".json"), buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// spikeStack is one running copy of the spike compose project.
type spikeStack struct {
	project, dir, token, url, control string
}

func startSpikeStack(ctx context.Context, t *testing.T) *spikeStack {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	st := &spikeStack{project: "bbgwit" + randHex(t, 4), dir: t.TempDir(), token: "it-" + randHex(t, 16)}
	for _, d := range []string{"out/ledger"} {
		if err := os.MkdirAll(filepath.Join(st.dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	build := exec.CommandContext(ctx, "go", "build", "-o", filepath.Join(st.dir, "out", "faultproxy"), "./spike/proxy")
	build.Dir, build.Env = root, append(os.Environ(), "GOWORK=off", "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build spike proxy: %v\n%s", err, out)
	}
	apiPort, ctlPort := freePort(t), freePort(t)
	st.url, st.control = fmt.Sprintf("http://127.0.0.1:%d", apiPort), fmt.Sprintf("http://127.0.0.1:%d", ctlPort)
	override := fmt.Sprintf(`services:
  bifrost:
    environment:
      BIFROST_PG_PASSWORD: bifrost-spike-pw
    volumes:
      - %[1]s/deploy/bifrost/config.json:/app/data/config.json:ro
      - %[1]s/deploy/bifrost/datasheet:/app/datasheet:ro
    ports: !override
      - "127.0.0.1:%[2]d:8080"
  faultproxy:
    ports: !override
      - "127.0.0.1:%[3]d:9101"
`, root, apiPort, ctlPort)
	writeFile(t, filepath.Join(st.dir, "override.yaml"), override)
	writeFile(t, filepath.Join(st.dir, "stack.env"), fmt.Sprintf("BIFROST_SETUP_TOKEN=%s\nSPIKE_UID=%d\nSPIKE_GID=%d\n", st.token, os.Getuid(), os.Getgid()))
	compose := func(ctx context.Context, args ...string) ([]byte, error) {
		base := []string{"compose", "-p", st.project, "-f", filepath.Join(root, "spike", "compose.yaml"),
			"-f", filepath.Join(st.dir, "override.yaml"), "--project-directory", st.dir, "--env-file", filepath.Join(st.dir, "stack.env")}
		return exec.CommandContext(ctx, "docker", append(base, args...)...).CombinedOutput()
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer ccancel()
		if out, err := compose(cctx, "down", "-v", "--remove-orphans"); err != nil {
			t.Errorf("compose down: %v\n%s", err, out)
		}
	})
	if out, err := compose(ctx, "up", "-d"); err != nil {
		t.Fatalf("compose up: %v\n%s", err, out)
	}
	if err := waitHealth(ctx, st.url, 2*time.Minute); err != nil {
		logs, _ := compose(ctx, "logs", "--tail", "40", "bifrost")
		t.Fatalf("bifrost health: %v\n%s", err, logs)
	}
	return st
}

// sendTraffic makes the run's requests through Bifrost with the run key:
// JSON, a stream with tools, token counting, a model list, one request that
// is retried after an injected 503 and one that fails on both attempts.
func (st *spikeStack) sendTraffic(ctx context.Context, t *testing.T, key *schemas.RunKey) {
	t.Helper()
	msg := func(text string, extra map[string]any) map[string]any {
		b := map[string]any{"model": testModel, "max_tokens": 64, "messages": []any{map[string]any{"role": "user", "content": text}}}
		for k, v := range extra {
			b[k] = v
		}
		return b
	}
	tools := []any{map[string]any{"name": "Read", "description": "Read a file", "input_schema": map[string]any{"type": "object", "properties": map[string]any{"file_path": map[string]any{"type": "string"}}}}}
	st.infer(ctx, t, key, http.MethodPost, "/anthropic/v1/messages", msg("integration json", nil), 200)
	st.infer(ctx, t, key, http.MethodPost, "/anthropic/v1/messages", msg("integration stream", map[string]any{"stream": true, "tools": tools}), 200)
	st.infer(ctx, t, key, http.MethodPost, "/anthropic/v1/messages/count_tokens", map[string]any{"model": testModel, "messages": []any{map[string]any{"role": "user", "content": "count"}}}, 200)
	st.infer(ctx, t, key, http.MethodGet, "/anthropic/v1/models", nil, 200)
	st.plan(ctx, t, key.KeySlot, `{"mode":"once","call":1,"status":503}`)
	st.infer(ctx, t, key, http.MethodPost, "/anthropic/v1/messages", msg("integration retry ok", nil), 200)
	st.plan(ctx, t, key.KeySlot, `{"mode":"persistent","from_call":1,"status":503}`)
	st.infer(ctx, t, key, http.MethodPost, "/anthropic/v1/messages", msg("integration retry fail", nil), 503)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, st.control+"/plans/"+key.KeySlot, nil)
	if err != nil {
		t.Fatal(err)
	}
	doOK(t, req)
}

func (st *spikeStack) infer(ctx context.Context, t *testing.T, key *schemas.RunKey, method, path string, body map[string]any, want int) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, st.url+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("x-api-key", key.Value)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != want {
		t.Fatalf("%s %s: status %d, want %d: %s", method, path, resp.StatusCode, want, snippet(out))
	}
}

func (st *spikeStack) plan(ctx context.Context, t *testing.T, slot, body string) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, st.control+"/plans/"+slot, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	doOK(t, req)
}

// ledger reads the spike proxy's ledger for slot as schemas.LedgerEntry. The
// spike format has "counted" and "ts" where the bench proxy has "kind" and
// "at".
func (st *spikeStack) ledger(t *testing.T, slot string) []schemas.LedgerEntry {
	t.Helper()
	f, err := os.Open(filepath.Join(st.dir, "out", "ledger", "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []schemas.LedgerEntry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var l struct {
			TS         time.Time `json:"ts"`
			Slot       string    `json:"slot"`
			Call       int       `json:"call"`
			Counted    bool      `json:"counted"`
			Method     string    `json:"method"`
			Path       string    `json:"path"`
			Action     string    `json:"action"`
			Status     int       `json:"status"`
			Stream     bool      `json:"stream"`
			ReqSHA256  string    `json:"req_sha256"`
			ResponseID string    `json:"response_id"`
			Nonce      string    `json:"nonce"`
		}
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatal(err)
		}
		if l.Slot != slot {
			continue
		}
		kind := "other"
		switch {
		case l.Counted:
			kind = "message"
		case l.Path == "/v1/messages/count_tokens":
			kind = "count_tokens"
		case strings.HasPrefix(l.Path, "/v1/models"):
			kind = "models"
		}
		out = append(out, schemas.LedgerEntry{Slot: l.Slot, Call: l.Call, Kind: kind, Path: l.Path, Stream: l.Stream,
			Action: l.Action, Status: l.Status, ReqSHA256: l.ReqSHA256, ResponseID: l.ResponseID, Nonce: l.Nonce, At: l.TS})
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// helperImage is the egress probe image: the bench base image when built,
// else python:3.12-slim. Both have getent and python3.
func helperImage(ctx context.Context, t *testing.T) string {
	t.Helper()
	for _, img := range []string{"bench/coding-core-base:1", "python:3.12-slim"} {
		if exec.CommandContext(ctx, "docker", "image", "inspect", img).Run() == nil {
			return img
		}
	}
	t.Skip("no helper image with getent and python3 (bench/coding-core-base:1 or python:3.12-slim)")
	return ""
}

func waitHealth(ctx context.Context, url string, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/health", nil)
		if err != nil {
			return err
		}
		if resp, err := http.DefaultClient.Do(req); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("not healthy after %s", limit)
}

func doOK(t *testing.T, req *http.Request) {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("%s %s: %d %s", req.Method, req.URL, resp.StatusCode, b)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func randHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// testLogger sends gateway logs to the test log.
type testLogger struct{ t *testing.T }

func (l testLogger) Debug(msg string, args ...any) { l.t.Log(append([]any{"DEBUG", msg}, args...)...) }
func (l testLogger) Info(msg string, args ...any)  { l.t.Log(append([]any{"INFO", msg}, args...)...) }
func (l testLogger) Warn(msg string, args ...any)  { l.t.Log(append([]any{"WARN", msg}, args...)...) }
func (l testLogger) Error(msg string, args ...any) { l.t.Log(append([]any{"ERROR", msg}, args...)...) }
