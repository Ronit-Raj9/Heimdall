package templates

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRender(t *testing.T) {
	for _, name := range Names {
		t.Run(name, func(t *testing.T) {
			files, err := Render(name, "acme-bot", "acme/agent-bot:2")
			if err != nil {
				t.Fatal(err)
			}
			paths := map[string]File{}
			for _, f := range files {
				paths[f.Path] = f
				for _, m := range []string{markerID, markerTag, markerLauncher} {
					if strings.Contains(string(f.Content), m) {
						t.Errorf("%s still holds %s", f.Path, m)
					}
				}
			}
			y, ok := paths["agents/acme-bot.yaml"]
			df, okD := paths["agents/images/acme-bot/Dockerfile"]
			if !ok || !okD {
				t.Fatalf("files = %v", keys(paths))
			}
			var cfg struct {
				ID    string `yaml:"id"`
				Image string `yaml:"image"`
				Build struct {
					Dockerfile string `yaml:"dockerfile"`
					Tag        string `yaml:"tag"`
					BaseArg    string `yaml:"base_arg"`
				} `yaml:"build"`
				Env   map[string]string `yaml:"env"`
				Files map[string]string `yaml:"files"`
			}
			if err := yaml.Unmarshal(y.Content, &cfg); err != nil {
				t.Fatalf("agent yaml: %v", err)
			}
			if cfg.ID != "acme-bot" || cfg.Image != "acme/agent-bot:2" || cfg.Build.Tag != cfg.Image ||
				cfg.Build.Dockerfile != "agents/images/acme-bot/Dockerfile" || cfg.Build.BaseArg != "BASE_IMAGE" {
				t.Errorf("config = %+v", cfg)
			}
			if !strings.Contains(string(df.Content), "ARG BASE_IMAGE=") || !strings.Contains(string(df.Content), "FROM ${BASE_IMAGE}") ||
				!strings.Contains(string(df.Content), "USER 1000") {
				t.Errorf("Dockerfile does not build on the suite base as uid 1000:\n%s", df.Content)
			}
			// Every template runs python3; a base image without it must fail
			// the build with a message, not with a bare exit code 127.
			if !strings.Contains(string(df.Content), "command -v python3") || !strings.Contains(string(df.Content), "has none") {
				t.Errorf("Dockerfile does not check for python3:\n%s", df.Content)
			}
			for k, v := range cfg.Env {
				if strings.Contains(v, "{{run.key}}") && strings.Contains(k, "SESSION") {
					t.Errorf("env %s carries the run key", k)
				}
			}
			if name == "http-service" {
				launcher, err := fs.ReadFile(files0(), "http-service/launcher.py")
				if err != nil {
					t.Fatal(err)
				}
				if cfg.Files["/tmp/bench/launcher.py"] != string(launcher) {
					t.Error("launcher in files differs from launcher.py")
				}
			}
		})
	}
}

func TestRenderRejects(t *testing.T) {
	for _, tc := range []struct{ name, tmpl, id, tag string }{
		{"unknown template", "rust", "x", "x:1"},
		{"bad id", "python", "Bad Id", "x:1"},
		{"empty tag", "python", "x", ""},
		{"tag with space", "python", "x", "x 1"},
	} {
		if _, err := Render(tc.tmpl, tc.id, tc.tag); err == nil {
			t.Errorf("%s: rendered", tc.name)
		}
	}
}

func TestTemplatePythonCompiles(t *testing.T) {
	py := python(t)
	err := fs.WalkDir(files, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".py") {
			return err
		}
		src, err := fs.ReadFile(files, p)
		if err != nil {
			return err
		}
		cmd := exec.Command(py, "-I", "-B", "-c", "import ast, sys; ast.parse(sys.stdin.read())")
		cmd.Stdin = strings.NewReader(string(src))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s: %v\n%s", p, err, out)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestHTTPServiceTemplate runs the http-service launcher with the example
// server against a fake OpenAI-compatible gateway: the server must make the
// model call with the run key and session id, apply the tool call, and the
// launcher must print the final message last and exit 0. A server that
// fails makes the launcher exit 1.
func TestHTTPServiceTemplate(t *testing.T) {
	py := python(t)
	var mu sync.Mutex
	var seen []http.Header
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []map[string]any `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		seen = append(seen, r.Header.Clone())
		mu.Unlock()
		msg := map[string]any{"role": "assistant", "content": "Done. Wrote hello.txt."}
		if len(req.Messages) == 1 {
			msg = map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{"id": "c1", "type": "function",
				"function": map[string]any{"name": "write_file", "arguments": `{"path":"hello.txt","content":"hi"}`}}}}
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": msg}}}); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer gw.Close()
	dir := t.TempDir()
	launcher := filepath.Join(dir, "launcher.py")
	server := filepath.Join(dir, "server.py")
	copyFile(t, "http-service/launcher.py", launcher)
	copyFile(t, "http-service/image/server.py", server)
	ws, instr := t.TempDir(), filepath.Join(dir, "instruction.md")
	if err := os.WriteFile(instr, []byte("write hello.txt"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(serverCmd string) (string, string, int) {
		port := freePort(t)
		cmd := exec.Command(py, "-I", "-B", launcher)
		cmd.Env = []string{
			"PATH=" + os.Getenv("PATH"),
			fmt.Sprintf("AGENT_SERVER_CMD=%s %s --port %d", py, serverCmd, port),
			fmt.Sprintf("AGENT_HEALTH_URL=http://127.0.0.1:%d/health", port),
			fmt.Sprintf("AGENT_RUN_URL=http://127.0.0.1:%d/run", port),
			"AGENT_STARTUP_TIMEOUT_S=20", "AGENT_RUN_TIMEOUT_S=60",
			"BENCH_BASE_URL=" + gw.URL + "/openai/v1", "BENCH_API_KEY=sk-bf-runkey", "BENCH_MODEL=anthropic/claude-x",
			"BENCH_INSTRUCTION_FILE=" + instr, "BENCH_WORKSPACE=" + ws, "BENCH_SESSION_ID=r_abc",
		}
		var stdout, stderr strings.Builder
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return stdout.String(), stderr.String(), code
	}
	stdout, stderr, code := run(server)
	if code != 0 || strings.TrimSpace(stdout) != "Done. Wrote hello.txt." {
		t.Fatalf("exit %d stdout %q\nstderr %s", code, stdout, stderr)
	}
	if b, err := os.ReadFile(filepath.Join(ws, "hello.txt")); err != nil || string(b) != "hi" {
		t.Fatalf("workspace file: %q %v", b, err)
	}
	mu.Lock()
	if len(seen) != 2 || seen[0].Get("Authorization") != "Bearer sk-bf-runkey" || seen[0].Get("x-bf-session-id") != "r_abc" {
		t.Errorf("gateway saw %v", seen)
	}
	mu.Unlock()
	broken := filepath.Join(dir, "broken.py")
	if err := os.WriteFile(broken, []byte("import sys\nsys.exit(2)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if stdout, _, code := run(broken); code != 1 || !strings.Contains(stdout, "server exited with code 2") {
		t.Fatalf("broken server: exit %d stdout %q", code, stdout)
	}
}

// helperScript runs only a template's report_event (its imports, the
// EVENTS_* settings and the helper functions, never the agent itself) and
// prints the id of the first event it reports.
const helperScript = `import ast, sys
tree = ast.parse(open(sys.argv[1]).read())
def keep(n):
    if isinstance(n, (ast.Import, ast.ImportFrom)):
        return True
    if isinstance(n, ast.FunctionDef):
        return n.name in ("report_event", "now_rfc3339")
    return isinstance(n, ast.Assign) and any(getattr(t, "id", "") in ("EVENTS_URL", "EVENTS_FILE") for t in n.targets)
ns = {}
exec(compile(ast.Module(body=[n for n in tree.body if keep(n)], type_ignores=[]), sys.argv[1], "exec"), ns)
first = ns["report_event"]("plan", "p", output={"steps": 2})
ns["report_event"]("tool_result", "t", parent_id=first, output="x" * 9000, status="ok")
print(first or "")
`

// TestTemplateEventHelpers checks the agent events helpers of the python and
// http-service templates: nothing is reported without BENCH_EVENTS_URL or
// BENCH_EVENTS_FILE, events go to the URL when it answers and to the file
// otherwise. The command template prints prefixed stdout lines only when the
// bench collects events.
func TestTemplateEventHelpers(t *testing.T) {
	py := python(t)
	run := func(t *testing.T, file string, env ...string) (string, string) {
		t.Helper()
		src := filepath.Join(t.TempDir(), filepath.Base(file))
		copyFile(t, file, src)
		cmd := exec.Command(py, "-I", "-B", "-c", helperScript, src)
		cmd.Env = append([]string{"PATH=" + os.Getenv("PATH")}, env...)
		var stdout, stderr strings.Builder
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("%s: %v\n%s", file, err, stderr.String())
		}
		return strings.TrimSpace(stdout.String()), stderr.String()
	}
	readEvents := func(t *testing.T, body string) []map[string]any {
		t.Helper()
		var out []map[string]any
		for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
			var ev map[string]any
			if err := json.Unmarshal([]byte(line), &ev); err != nil {
				t.Fatalf("event line %q: %v", line, err)
			}
			out = append(out, ev)
		}
		return out
	}
	check := func(t *testing.T, evs []map[string]any, first string) {
		t.Helper()
		if len(evs) != 2 || evs[0]["type"] != "plan" || evs[0]["id"] != first || evs[0]["name"] != "p" ||
			evs[1]["parent_id"] != first || evs[1]["status"] != "ok" {
			t.Fatalf("events = %v (first id %s)", evs, first)
		}
		if start, _ := evs[0]["start_time"].(string); len(start) != len("2026-01-02T03:04:05.678Z") || !strings.HasSuffix(start, "Z") {
			t.Errorf("start_time %q is not RFC 3339 UTC with milliseconds", start)
		}
		if out, _ := evs[1]["output"].(string); len(out) > 4000 {
			t.Errorf("output not capped: %d bytes", len(out))
		}
	}
	for _, file := range []string{"python/image/agent.py", "http-service/launcher.py"} {
		t.Run(file, func(t *testing.T) {
			dir := t.TempDir()
			events := filepath.Join(dir, "events.jsonl")
			if first, _ := run(t, file); len(first) != 32 {
				t.Errorf("no-op returned id %q", first)
			}
			if _, err := os.Stat(events); !os.IsNotExist(err) {
				t.Fatalf("events file written without the variables: %v", err)
			}

			first, _ := run(t, file, "BENCH_EVENTS_FILE="+events)
			b, err := os.ReadFile(events)
			if err != nil {
				t.Fatal(err)
			}
			check(t, readEvents(t, string(b)), first)

			var mu sync.Mutex
			var posted strings.Builder
			status := http.StatusAccepted
			col := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				mu.Lock()
				defer mu.Unlock()
				if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/x-ndjson" {
					t.Errorf("%s with content type %q", r.Method, r.Header.Get("Content-Type"))
				}
				posted.Write(body)
				w.WriteHeader(status)
				fmt.Fprint(w, `{"accepted":1,"dropped":0}`)
			}))
			defer col.Close()
			urlFile := filepath.Join(dir, "url.jsonl")
			first, _ = run(t, file, "BENCH_EVENTS_URL="+col.URL+"/events", "BENCH_EVENTS_FILE="+urlFile)
			mu.Lock()
			check(t, readEvents(t, posted.String()), first)
			posted.Reset()
			status = http.StatusInternalServerError
			mu.Unlock()
			if _, err := os.Stat(urlFile); !os.IsNotExist(err) {
				t.Errorf("delivered events also written to the file: %v", err)
			}
			first, _ = run(t, file, "BENCH_EVENTS_URL="+col.URL+"/events", "BENCH_EVENTS_FILE="+urlFile)
			if b, err = os.ReadFile(urlFile); err != nil {
				t.Fatalf("failed POST did not fall back to the file: %v", err)
			}
			check(t, readEvents(t, string(b)), first)
		})
	}
	t.Run("command/image/example-cli.py", func(t *testing.T) {
		if out, _ := run(t, "command/image/example-cli.py"); out != "" {
			t.Errorf("stdout without the variables: %q", out)
		}
		out, _ := run(t, "command/image/example-cli.py", "BENCH_EVENTS_FILE=/opt/bench/events/events.jsonl")
		var evs []map[string]any
		for _, line := range strings.Split(out, "\n") {
			if rest, ok := strings.CutPrefix(line, "@@heimdall-event "); ok {
				evs = append(evs, readEvents(t, rest)...)
			}
		}
		if len(evs) != 2 || evs[0]["type"] != "plan" || evs[1]["type"] != "tool_result" || evs[1]["status"] != "ok" {
			t.Errorf("stdout events = %v\n%s", evs, out)
		}
	})
}

func files0() fs.FS { return files }

func python(t *testing.T) string {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	return py
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	f, err := files.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	out, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if _, err := io.Copy(out, f); err != nil {
		t.Fatal(err)
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

func keys(m map[string]File) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
