package adapters

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/maximhq/bifrost/bench/utils"
)

// TestRemoteHTTPLauncherRetriesThenUnreachable checks Y03 (G1 P1-2): a 503
// from the customer's load balancer is retried and the answer after it is
// used; an endpoint that only ever answers 502, or refuses connections, ends
// with endpoint_unreachable and exit 75 (infrastructure), while a 500 is
// still the agent's failure (exit 1).
func TestRemoteHTTPLauncherRetriesThenUnreachable(t *testing.T) {
	var calls atomic.Int32
	var mode atomic.Value
	mode.Store("flaky")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		switch mode.Load() {
		case "flaky":
			if n == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"answer": "Tuesday"}`))
		case "down":
			w.WriteHeader(http.StatusBadGateway)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	env := map[string]string{
		"BENCH_REMOTE_URL": "http://agent.invalid:" + port + "/run", utils.EnvRemoteConnect: "127.0.0.1:" + port,
		"BENCH_REMOTE_TIMEOUT_S": "20", "BENCH_REMOTE_TRAFFIC": RemoteTrafficExternal, "BENCH_REMOTE_BACKOFF_S": "0.05",
	}
	answer, out, err := runLauncher(t, env)
	if err != nil || answer != "Tuesday" || calls.Load() != 2 || !strings.Contains(out, `"type": "retry"`) {
		t.Fatalf("flaky: answer %q calls %d err %v\n%s", answer, calls.Load(), err, out)
	}

	calls.Store(0)
	mode.Store("down")
	_, out, err = runLauncher(t, env)
	if code := exitCode(err); code != 75 || calls.Load() != 4 || !strings.Contains(out, `"type": "endpoint_unreachable"`) {
		t.Fatalf("down: exit %d calls %d\n%s", code, calls.Load(), out)
	}

	calls.Store(0)
	mode.Store("broken")
	_, out, err = runLauncher(t, env)
	if code := exitCode(err); code != 1 || calls.Load() != 1 || strings.Contains(out, "endpoint_unreachable") {
		t.Fatalf("500: exit %d calls %d\n%s", code, calls.Load(), out)
	}

	// Nothing listens: refused on every attempt.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	env[utils.EnvRemoteConnect] = addr
	env["BENCH_REMOTE_RETRIES"] = "1"
	_, out, err = runLauncher(t, env)
	if code := exitCode(err); code != 75 || !strings.Contains(out, `"attempts": 2`) {
		t.Fatalf("refused: exit %d\n%s", code, out)
	}
}

func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	if err != nil {
		return -1
	}
	return 0
}
