package openmodel

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/sandbox"
)

// TestModalLive serves a real open model on a real Modal account, end to
// end: deploy, first answer, the registered provider key, one chat
// completion with tool calling on, then the stop, and checks no serving app
// is left. It costs real money (an L4 for a few minutes), so it runs only
// with HEIMDALL_MODAL_LIVE=1 and HEIMDALL_MODAL_LIVE_ENV naming a file with
// MODAL_TOKEN_ID and MODAL_TOKEN_SECRET (the token is never printed).
// HEIMDALL_MODAL_PYTHON names a Python with the modal package.
func TestModalLive(t *testing.T) {
	if os.Getenv("HEIMDALL_MODAL_LIVE") != "1" {
		t.Skip("set HEIMDALL_MODAL_LIVE=1 and HEIMDALL_MODAL_LIVE_ENV to run on a real Modal account")
	}
	env := readEnvFile(t, os.Getenv("HEIMDALL_MODAL_LIVE_ENV"))
	tok := env["MODAL_TOKEN_ID"] + ":" + env["MODAL_TOKEN_SECRET"]
	if env["MODAL_TOKEN_ID"] == "" || env["MODAL_TOKEN_SECRET"] == "" {
		t.Fatal("the env file has no MODAL_TOKEN_ID and MODAL_TOKEN_SECRET")
	}
	creds := liveCreds{c: sandbox.Credentials{Token: tok}}
	dep := ModalDeployer{}
	keys := keystore.NewMemory()
	s := &Service{Keys: keys, Platforms: creds, Deployer: dep, Store: NewMemoryStore(), PollEvery: 10 * time.Second,
		ReadyWithin: 12 * time.Minute, HTTP: &http.Client{Timeout: 150 * time.Second}}
	ctx := context.Background()
	begin := time.Now()
	d, err := s.Start(ctx, "org_live", "user:live-test", StartRequest{Model: "Qwen/Qwen2.5-0.5B-Instruct", GPU: "L4", MaxHours: 0.25})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		// Whatever happened, stop the app and list what is left.
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		_ = dep.Stop(sctx, creds.c, d.AppName)
		apps, err := dep.List(sctx, creds.c)
		if err != nil {
			t.Errorf("list apps after the test: %v", err)
			return
		}
		for _, a := range apps {
			if a.State == "deployed" || a.State == "running" || a.State == "deploying" {
				t.Errorf("app left on Modal: %s (%s)", a.Name, a.State)
			}
		}
		t.Logf("serving apps of this package on Modal after the test: %d listed, none deployed or running (total %s)", len(apps), time.Since(begin).Round(time.Second))
	}()
	s.Wait()
	got, _ := s.Get(ctx, "org_live", d.ID)
	t.Logf("state %s after %s: %s", got.State, time.Since(begin).Round(time.Second), got.Detail)
	if got.State != StateReady {
		t.Fatalf("not ready: %+v", got)
	}
	readyAt := time.Now()
	key, err := keys.Reveal(ctx, "org_live", got.KeyID)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"model":"Qwen/Qwen2.5-0.5B-Instruct","max_tokens":40,"messages":[{"role":"user","content":"Reply with the single word: pong"}],
"tools":[{"type":"function","function":{"name":"noop","description":"does nothing","parameters":{"type":"object","properties":{}}}}]}`
	req, _ := http.NewRequest(http.MethodPost, got.BaseURL+"/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(raw), `"choices"`) {
		t.Fatalf("chat completion: HTTP %d %s", resp.StatusCode, raw)
	}
	t.Logf("chat completion OK in %s: %s", time.Since(readyAt).Round(time.Millisecond), trimTo(string(raw), 300))
	bad, _ := http.NewRequest(http.MethodGet, got.BaseURL+"/models", nil)
	bad.Header.Set("Authorization", "Bearer wrong-key")
	if r, err := http.DefaultClient.Do(bad); err == nil {
		r.Body.Close()
		if r.StatusCode != http.StatusUnauthorized {
			t.Errorf("a wrong key got HTTP %d, want 401", r.StatusCode)
		}
	}
	stopped, err := s.Stop(ctx, "org_live", d.ID, "user:live-test")
	if err != nil || stopped.State != StateStopped {
		t.Fatalf("stop: %+v %v", stopped, err)
	}
	if _, err := keys.Get(ctx, "org_live", got.KeyID); err == nil {
		t.Error("the provider key is still there after the stop")
	}
	t.Logf("GPU time bound: ready %s, stopped %s after start", got.ReadyAt.Sub(got.CreatedAt).Round(time.Second), stopped.StoppedAt.Sub(got.CreatedAt).Round(time.Second))
}

type liveCreds struct{ c sandbox.Credentials }

func (l liveCreds) CredentialsOf(context.Context, string, string) (sandbox.Credentials, error) {
	return l.c, nil
}

func readEnvFile(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open the Modal env file: %v", err)
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if ok && !strings.HasPrefix(k, "#") {
			out[strings.TrimSpace(strings.TrimPrefix(k, "export "))] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return out
}

func trimTo(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
