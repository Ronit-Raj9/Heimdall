package monitorcert

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// fakeDocker writes a docker stand-in: `run` logs its argv and answers each
// stdin line with body (a shell snippet that prints the answers), and `rm`
// says the container is gone.
func fakeDocker(t *testing.T, body string) (bin, argvLog string) {
	t.Helper()
	dir := t.TempDir()
	argvLog = filepath.Join(dir, "argv.log")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = rm ]; then echo \"Error: No such container: $3\" >&2; exit 1; fi\n" +
		"printf '%s\\n' \"$@\" > " + argvLog + "\n" + body + "\n"
	bin = filepath.Join(dir, "docker")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argvLog
}

func commandResolved(t *testing.T, timeout int) *Resolved {
	t.Helper()
	r, err := Resolve(schemas.MonitorConfig{Name: "c", Kind: schemas.MonitorKindCommand, Image: "python:3.12-slim",
		Argv: []string{"python3", "-c", "{{script}}"}, Script: "s.py", TimeoutS: timeout}, "print('x')")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCommandMonitor(t *testing.T) {
	perLine := `while IFS= read -r l; do echo '{"decision":"block","score":0.5,"reason":"r"}'; done`
	tests := []struct {
		name    string
		body    string
		reqs    int
		timeout int
		wantErr string
	}{
		{"one answer per line", perLine, 3, 5, ""},
		{"too few answers", `cat > /dev/null; echo '{"decision":"allow"}'`, 2, 5, "wrote 1 answers for 2 requests"},
		{"not json", `cat > /dev/null; echo nope`, 1, 5, "monitor answer 1"},
		{"non-zero exit", `cat > /dev/null; echo boom >&2; exit 3`, 1, 5, "exit 3: boom"},
		{"timeout", `sleep 5`, 1, 1, "timed out"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bin, argvLog := fakeDocker(t, tc.body)
			m := newCommandMonitor(commandResolved(t, tc.timeout), RunnerDeps{Docker: utils.Docker{Binary: bin}, NamePrefix: "hd-s2-test", CertID: "mc_abc", Logger: schemas.NoopLogger{}})
			resps, err := m.Score(context.Background(), make([]schemas.MonitorRequest, tc.reqs))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(resps) != tc.reqs || resps[0].Decision != schemas.MonitorBlock {
				t.Fatalf("answers %+v", resps)
			}
			b, err := os.ReadFile(argvLog)
			if err != nil {
				t.Fatal(err)
			}
			args := strings.Split(strings.TrimSpace(string(b)), "\n")
			if args[0] != "run" || !slices.Contains(args, "hd-s2-test-mc-abc-1") {
				t.Errorf("argv %v", args)
			}
		})
	}
}

// TestCommandRunArgs pins the sandbox: every flag DESIGN.md 14 asks of an
// agent container, no network, the bench labels, and the script inline.
func TestCommandRunArgs(t *testing.T) {
	m := newCommandMonitor(commandResolved(t, 5), RunnerDeps{CertID: "mc_abc"})
	args := m.runArgs("hd-s2-x")
	joined := strings.Join(args, " ")
	for _, want := range []string{"run --rm -i --pull never --name hd-s2-x", "--user 65534:65534", "--cap-drop ALL",
		"--security-opt no-new-privileges", "--pids-limit 64", "--memory 256m", "--cpus 1", "--read-only",
		"--tmpfs /tmp:rw,nosuid,nodev,size=16m", "--network none", "--label bifrost-bench=1",
		"--label bifrost-bench.monitor=mc_abc", "python:3.12-slim python3 -c print('x')"} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv lacks %q: %s", want, joined)
		}
	}
	for _, bad := range []string{"-v", "--volume", "--mount", "docker.sock", "--privileged", "--network host"} {
		if slices.Contains(args, bad) || strings.Contains(joined, bad+" ") {
			t.Errorf("argv carries %q", bad)
		}
	}
}

func TestParseAnswersAndCapBuffer(t *testing.T) {
	resps, err := parseAnswers([]byte("\n{\"decision\":\"allow\"}\n\n{\"decision\":\"block\",\"score\":1}\n"), 2)
	if err != nil || len(resps) != 2 || resps[1].Decision != "block" {
		t.Fatalf("answers %+v err %v", resps, err)
	}
	b := &capBuffer{max: 4}
	if n, err := b.Write([]byte("abcdef")); n != 6 || err != nil || !b.over || b.String() != "abcd" {
		t.Errorf("cap buffer n %d over %v %q", n, b.over, b.String())
	}
}
