// Package clients holds the Python and TypeScript clients of the bench API
// (docs/clients.md). This test keeps them honest: it builds bifrost-bench,
// serves a copy of the recorded bundle (testdata/recorded) in recorded mode
// with the real handlers, and runs each client's tests, its live smoke test
// and its CI gate example against it. A client language whose runtime is
// missing is skipped, never failed.
//
// Set HEIMDALL_REGENERATE=1 to rewrite the generated client types
// (python/heimdall/models.py, typescript/src/types.ts) instead of failing
// when they are stale. Set HEIMDALL_BENCH_BIN to reuse a built binary.
package clients

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// recordedExp is the real recorded experiment the CI gate examples check.
const recordedExp = "e_pilot_mixed_nc93"

// liveServer is a running bifrost-bench serve process.
type liveServer struct {
	url   string
	token string
	tmp   string
}

func TestClientsAgainstRecordedServer(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and serves bifrost-bench; skipped with -short")
	}
	python, pyErr := exec.LookPath("python3")
	node, nodeErr := exec.LookPath("node")
	if nodeErr == nil && !nodeStripsTypes(node) {
		nodeErr = fmt.Errorf("node 22.6 or newer is needed to run the TypeScript tests")
	}
	if pyErr != nil && nodeErr != nil {
		t.Skipf("neither python3 nor a recent node found: %v; %v", pyErr, nodeErr)
	}
	srv := startRecordedServer(t)
	env := []string{"HEIMDALL_URL=" + srv.url, "HEIMDALL_TOKEN=" + srv.token, "HEIMDALL_EXPECT_MODE=recorded"}
	openapi := filepath.Join(srv.tmp, "openapi.json")
	fetchTo(t, srv.url+"/api/bench/openapi.json", openapi)
	regenerate := os.Getenv("HEIMDALL_REGENERATE") == "1"

	t.Run("python", func(t *testing.T) {
		if pyErr != nil {
			t.Skipf("python3 not found: %v", pyErr)
		}
		gen := []string{"scripts/gen_models.py", "--file", openapi}
		if !regenerate {
			gen = append(gen, "--check")
		}
		run(t, "python", nil, python, gen...)
		run(t, "python", env, python, "-m", "unittest", "-v")
		gate(t, env, python, filepath.Join("examples", "ci_gate.py"))
	})
	t.Run("typescript", func(t *testing.T) {
		if nodeErr != nil {
			t.Skip(nodeErr.Error())
		}
		sync := []string{"scripts/sync-types.mjs"}
		if !regenerate {
			sync = append(sync, "--check")
		}
		run(t, "typescript", nil, node, sync...)
		run(t, "typescript", env, node, "--experimental-strip-types", "--test", "test/client.test.ts", "test/v1.test.ts", "test/live.test.ts")
		gate(t, env, node, "--experimental-strip-types", filepath.Join("examples", "ci-gate.ts"))
		tsc := filepath.Join("..", "ui", "node_modules", ".bin", "tsc")
		if _, err := os.Stat(tsc); err != nil {
			t.Logf("tsc not found at %s (bench/ui not installed): type check skipped", tsc)
			return
		}
		run(t, "typescript", nil, filepath.Join("..", tsc), "-p", "tsconfig.json", "--noEmit")
		run(t, "typescript", nil, filepath.Join("..", tsc), "-p", "tsconfig.test.json", "--noEmit")
	})
}

// gate runs a CI gate example against the recorded experiment: it passes
// with --allow-recorded and refuses (exit 3) without it.
func gate(t *testing.T, env []string, interpreter string, args ...string) {
	t.Helper()
	with := append(append([]string{}, args...), "--exp", recordedExp, "--allow-recorded")
	if code, out := runCode(t, ".", env, interpreter, with...); code != 0 {
		t.Errorf("%s --allow-recorded: exit %d, want 0\n%s", args[len(args)-1], code, out)
	}
	without := append(append([]string{}, args...), "--exp", recordedExp)
	if code, out := runCode(t, ".", env, interpreter, without...); code != 3 {
		t.Errorf("%s on a recorded server: exit %d, want 3 (refused)\n%s", args[len(args)-1], code, out)
	}
}

// startRecordedServer builds bifrost-bench, copies the recorded bundle into
// a temp runs root (the analyzer writes analysis folders; the bundle itself
// is never touched) and serves it on a free loopback port.
func startRecordedServer(t *testing.T) *liveServer {
	t.Helper()
	benchRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("bench root: %v", err)
	}
	tmp := t.TempDir()
	bin := os.Getenv("HEIMDALL_BENCH_BIN")
	if bin == "" {
		bin = filepath.Join(tmp, "bifrost-bench")
		build := exec.Command("go", "build", "-o", bin, "./cmd/bifrost-bench")
		build.Dir = benchRoot
		build.Env = append(os.Environ(), "GOWORK=off")
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build bifrost-bench: %v\n%s", err, out)
		}
	}
	runs := filepath.Join(tmp, "runs")
	if err := copyTree(filepath.Join(benchRoot, "testdata", "recorded"), runs); err != nil {
		t.Fatalf("copy recorded bundle: %v", err)
	}
	port := freePort(t)
	token := randomToken(t)
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, "serve", "-offline", "-config", filepath.Join("deploy", "bench.yaml"))
	cmd.Dir = benchRoot
	// A clean environment: no inherited BENCH_* override, no worker token.
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + tmp,
		"BENCH_SERVER_MODE=recorded", "BENCH_RUNS_ROOT=" + runs,
		"BENCH_SERVER_LISTEN=127.0.0.1:" + strconv.Itoa(port), "BENCH_WRITE_TOKEN=" + token,
	}
	logFile, err := os.Create(filepath.Join(tmp, "serve.log"))
	if err != nil {
		cancel()
		t.Fatalf("create serve log: %v", err)
	}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start bifrost-bench serve: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait() // killed by the cancelled context; the exit error is expected
		logFile.Close()
	})
	srv := &liveServer{url: "http://127.0.0.1:" + strconv.Itoa(port), token: token, tmp: tmp}
	deadline := time.Now().Add(60 * time.Second)
	for {
		resp, err := http.Get(srv.url + "/api/bench/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return srv
			}
		}
		if time.Now().After(deadline) {
			log, _ := os.ReadFile(logFile.Name())
			t.Fatalf("bifrost-bench serve did not answer /health within 60s\n%s", log)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// run runs a command in the clients/<dir> folder and fails the test, with
// its output, when it exits non-zero.
func run(t *testing.T, dir string, env []string, name string, args ...string) {
	t.Helper()
	if code, out := runCode(t, dir, env, name, args...); code != 0 {
		t.Errorf("%s %s (in clients/%s): exit %d\n%s", filepath.Base(name), strings.Join(args, " "), dir, code, out)
	}
}

// runCode runs a command and returns its exit code and combined output.
func runCode(t *testing.T, dir string, env []string, name string, args ...string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode(), string(out)
		}
		t.Fatalf("run %s: %v\n%s", name, err, out)
	}
	return 0, string(out)
}

// nodeStripsTypes reports whether node can run .ts files (22.6 or newer).
func nodeStripsTypes(node string) bool {
	out, err := exec.Command(node, "--version").Output()
	if err != nil {
		return false
	}
	m := regexp.MustCompile(`^v(\d+)\.(\d+)`).FindStringSubmatch(strings.TrimSpace(string(out)))
	if m == nil {
		return false
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return major > 22 || (major == 22 && minor >= 6)
}

func fetchTo(t *testing.T, url, path string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d, err %v", url, resp.StatusCode, err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find a free port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func randomToken(t *testing.T) string {
	t.Helper()
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("token: %v", err)
	}
	return "hd-sdk-" + hex.EncodeToString(b)
}

// copyTree copies a folder of regular files and folders.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// TestDocsWorkedCommands: QC2-30-02 and QC2-30-03. The smoke demos pass
// --allow-recorded (smoke verdicts are synthetic), and the live CI compare
// shows the budget write-only still validates.
func TestDocsWorkedCommands(t *testing.T) {
	read := func(name string) string {
		raw, err := os.ReadFile(filepath.Join("..", "docs", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	for _, line := range strings.Split(read("clients.md"), "\n") {
		if strings.Contains(line, "ci_gate") || strings.Contains(line, "ci-gate.ts") {
			if strings.Contains(line, "experiments/smoke.yaml") && !strings.Contains(line, "--allow-recorded") {
				t.Errorf("smoke gate command without --allow-recorded: %s", line)
			}
		}
	}
	if ci := read("ci.md"); !strings.Contains(ci, "--budget-usd <cap> --write-only") {
		t.Error("docs/ci.md live compare omits --budget-usd")
	}
}
