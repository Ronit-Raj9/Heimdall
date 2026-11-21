package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
)

type cliTestLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *cliTestLog) add(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, s)
}
func (l *cliTestLog) Printf(f string, a ...any) { l.add(fmt.Sprintf(f, a...)) }
func (l *cliTestLog) Warnf(f string, a ...any)  { l.add(fmt.Sprintf(f, a...)) }
func (l *cliTestLog) Errorf(f string, a ...any) { l.add(fmt.Sprintf(f, a...)) }
func (l *cliTestLog) Write(p []byte) (int, error) {
	l.add(string(p))
	return len(p), nil
}
func (l *cliTestLog) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func withShell(t *testing.T) {
	t.Helper()
	old := cliExecutable
	cliExecutable = func() (string, error) { return "/bin/sh", nil }
	t.Cleanup(func() { cliExecutable = old })
}

func TestRunCLIResultAndEnv(t *testing.T) {
	withShell(t)
	t.Setenv("HEIMDALL_DATABASE_URL", "postgres://u:p@h/db")
	t.Setenv("HEIMDALL_MASTER_KEY", "c2VjcmV0")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "abc")
	log := &cliTestLog{}
	res, err := runCLI(context.Background(), jobEnv{ProjectRoot: t.TempDir()},
		[]string{"-c", `echo "db=[${HEIMDALL_DATABASE_URL}] mk=[${HEIMDALL_MASTER_KEY}] s3=[${AWS_SECRET_ACCESS_KEY}]"; echo '{"tasks":3}'; echo done`}, log)
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := res.(map[string]any); !ok || m["tasks"] != float64(3) {
		t.Fatalf("result = %#v", res)
	}
	if !strings.Contains(log.text(), "db=[] mk=[] s3=[]") {
		t.Fatalf("environment leaked into the subprocess:\n%s", log.text())
	}
}

func TestRunCLIExitCode(t *testing.T) {
	withShell(t)
	_, err := runCLI(context.Background(), jobEnv{ProjectRoot: t.TempDir()}, []string{"-c", "echo oops; exit 3"}, &cliTestLog{})
	if err == nil || !strings.Contains(err.Error(), "code 3") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunCLICancelStopsProcess(t *testing.T) {
	withShell(t)
	old := cliKillGrace
	cliKillGrace = 300 * time.Millisecond
	t.Cleanup(func() { cliKillGrace = old })
	for _, script := range []string{"echo started; sleep 30", "trap '' TERM; echo started; sleep 30 & wait; sleep 30"} {
		ctx, cancel := context.WithCancel(context.Background())
		log := &cliTestLog{}
		done := make(chan error, 1)
		start := time.Now()
		go func() {
			_, err := runCLI(ctx, jobEnv{ProjectRoot: t.TempDir()}, []string{"-c", script}, log)
			done <- err
		}()
		for i := 0; i < 100 && !strings.Contains(log.text(), "started"); i++ {
			time.Sleep(20 * time.Millisecond)
		}
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("%q: err = %v", script, err)
			}
			if time.Since(start) > 5*time.Second {
				t.Fatalf("%q: took %s", script, time.Since(start))
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%q: cancel did not stop the process", script)
		}
	}
}

func TestRunRedactedMasksLogErrorResult(t *testing.T) {
	spec := jobKindSpec{Kind: "t.x", Run: func(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
		log.Printf("using key sk-abcdefghijklmnopqrstuvwx")
		_, _ = log.Write([]byte("partial api_key=zzzzzzzzzz"))
		return map[string]any{"note": "token=yyyyyyyyyy"}, errors.New("auth failed for sk-abcdefghijklmnopqrstuvwx")
	}}
	log := &cliTestLog{}
	res, err := runRedacted(context.Background(), spec, jobEnv{}, &jobs.Job{}, log, func(any) {})
	raw, _ := json.Marshal(res)
	all := log.text() + string(raw) + err.Error()
	for _, leak := range []string{"sk-abcdefghijklmnopqrstuvwx", "zzzzzzzzzz", "yyyyyyyyyy"} {
		if strings.Contains(all, leak) {
			t.Fatalf("leaked %q: %s", leak, all)
		}
	}
}

func TestWriteJobConfigDropsCredentials(t *testing.T) {
	root := t.TempDir()
	cfg := &lib.Config{RunsRoot: "runs", BenchmarksRoot: "benchmarks", Root: root,
		Server: lib.ServerConfig{Listen: "127.0.0.1:0", Mode: schemas.ModeSynthetic}}
	cfg.Database.DSNEnv = "SECRETPW_DSN"
	env := jobEnv{App: &app{cfg: cfg}, ProjectRoot: root}
	path, cleanup, err := writeJobConfig(env)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "SECRETPW_DSN") {
		t.Fatalf("config copy names the database DSN:\n%s", data)
	}
	got, err := lib.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Root != root || got.RunsRoot != filepath.Join(root, "runs") {
		t.Fatalf("root %q runs %q", got.Root, got.RunsRoot)
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("config copy not removed: %v", err)
	}
}

func TestCLIArgBuilders(t *testing.T) {
	ok := []struct {
		f    cliArgs
		raw  string
		want string
	}{
		{tasksMineArgs, `{"suite":"core","limit":5,"dry_run":true}`, "tasks mine -config {{config}} -suite core -repo . -limit 5 -dry-run"},
		{calibrateRunArgs, `{"suite":"qa","yes":true}`, "judge calibrate qa -config {{config}} -yes"},
		{exportRunArgs, `{"format":"csv","exp_id":"exp_1"}`, "export csv -config {{config}} -exp exp_1 -out exports/exp_1.csv"},
		{scanSkillArgs, `{"path":"agents/a/skills/s"}`, "skill scan -config {{config}} -json agents/a/skills/s"},
		{scanMCPArgs, `{"path":"agents/a/mcp.yaml"}`, "mcp scan -config {{config}} -json agents/a/mcp.yaml"},
	}
	for _, c := range ok {
		argv, err := c.f(json.RawMessage(c.raw))
		if err != nil {
			t.Fatalf("%s: %v", c.raw, err)
		}
		if got := strings.Join(argv, " "); got != c.want {
			t.Fatalf("%s: got %q want %q", c.raw, got, c.want)
		}
	}
	bad := []struct {
		f   cliArgs
		raw string
	}{
		{tasksMineArgs, `{}`},
		{tasksMineArgs, `{"suite":"core","repo":"../other"}`},
		{tasksMineArgs, `{"suite":"core","typo":1}`},
		{calibrateRunArgs, `{"suite":"qa"}`},
		{exportRunArgs, `{"format":"pdf","exp_id":"e"}`},
		{exportRunArgs, `{"format":"csv","exp_id":"../x"}`},
		{scanSkillArgs, `{"path":"/etc/passwd"}`},
		{scanSkillArgs, `{"path":".git/config"}`},
		{importRunArgs, `{"format":"nope","from":"uploads/x","suite":"s"}`},
	}
	for _, c := range bad {
		if _, err := c.f(json.RawMessage(c.raw)); err == nil {
			t.Fatalf("%s: want an error", c.raw)
		}
	}
}
