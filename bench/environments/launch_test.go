package environments

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestBuildLaunchPlain(t *testing.T) {
	plan := &schemas.RunPlan{
		Argv: []string{"claude", "-p", "fix it; rm -rf / $(whoami)"},
		Env:  map[string]string{"TOKEN": "s3cr3t", "SPACED": " a b "},
	}
	l, err := buildLaunch(plan, "/opt/bench/shim:/usr/bin", "/home/agent")
	if err != nil {
		t.Fatalf("buildLaunch: %v", err)
	}
	if l.Entrypoint != "claude" || !reflect.DeepEqual(l.Args, []string{"-p", "fix it; rm -rf / $(whoami)"}) {
		t.Fatalf("entrypoint %q args %q", l.Entrypoint, l.Args)
	}
	want := []string{"HOME=/home/agent", "PATH=/opt/bench/shim:/usr/bin", "SPACED= a b ", "TOKEN=s3cr3t"}
	if !reflect.DeepEqual(l.Env, want) {
		t.Fatalf("env = %q, want %q", l.Env, want)
	}
}

func TestBuildLaunchErrors(t *testing.T) {
	tests := []struct {
		name string
		plan schemas.RunPlan
		want string
	}{
		{"reserved prefix", schemas.RunPlan{Argv: []string{"x"}, Env: map[string]string{"BENCH_WRAP_X": "1"}}, "reserved prefix"},
		{"bad env name", schemas.RunPlan{Argv: []string{"x"}, Env: map[string]string{"A-B": "1"}}, "invalid variable name"},
		{"nul in env", schemas.RunPlan{Argv: []string{"x"}, Env: map[string]string{"A": "a\x00b"}}, "NUL"},
		{"relative file", schemas.RunPlan{Argv: []string{"x"}, Files: map[string]string{"tmp/x": "1"}}, "absolute and clean"},
		{"unclean file", schemas.RunPlan{Argv: []string{"x"}, Files: map[string]string{"/tmp/../etc/x": "1"}}, "absolute and clean"},
		{"files without argv", schemas.RunPlan{Files: map[string]string{"/tmp/x": "1"}}, "no argv"},
		{"too large", schemas.RunPlan{Argv: []string{"x"}, Files: map[string]string{"/tmp/x": strings.Repeat("a", maxWrapBytes+1)}}, "over the"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := buildLaunch(&tt.plan, "/usr/bin", "/home/agent")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestBuildLaunchWrapperEncoding(t *testing.T) {
	big := strings.Repeat("0123456789", 7000) // over one env chunk once encoded
	plan := &schemas.RunPlan{
		Argv: []string{"agent", "--flag", "it's \"quoted\" $HOME"},
		Env:  map[string]string{"HEADERS": "x-a: 1\nx-b: 2\n", "PLAIN": "p"},
		Files: map[string]string{
			"/tmp/cc/settings.json": "{\"a\": 1}\n",
			"/tmp/cc/big.txt":       big,
			"/tmp/empty":            "",
		},
	}
	l, err := buildLaunch(plan, "/usr/bin:/bin", "/home/agent")
	if err != nil {
		t.Fatalf("buildLaunch: %v", err)
	}
	if l.Entrypoint != wrapShell || l.Args[0] != "-c" || l.Args[2] != wrapName {
		t.Fatalf("not wrapped: entrypoint %q args %q", l.Entrypoint, l.Args[:3])
	}
	if !reflect.DeepEqual(l.Args[3:], plan.Argv) {
		t.Fatalf("argv after wrapper = %q, want %q", l.Args[3:], plan.Argv)
	}
	script := l.Args[1]
	for _, leaked := range []string{"settings.json", "x-a", "quoted", "/tmp/cc"} {
		if strings.Contains(script, leaked) {
			t.Fatalf("script contains plan content %q:\n%s", leaked, script)
		}
	}
	for _, line := range l.Env {
		if len(line) > 64<<10 || strings.ContainsAny(line, "\n\r") {
			t.Fatalf("env line unusable in an env file: %d bytes", len(line))
		}
	}
	if !strings.Contains(script, "exec \"$@\"") || !strings.Contains(script, "export HEADERS=") {
		t.Fatalf("script:\n%s", script)
	}
	runWrapper(t, l, plan)
}

// runWrapper executes the generated script with the host's sh, writing the
// plan files under a temp root, and checks the decoded files and env.
func runWrapper(t *testing.T, l *launch, plan *schemas.RunPlan) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on this host")
	}
	if _, err := exec.LookPath("base64"); err != nil {
		t.Skip("no base64 on this host")
	}
	root := t.TempDir()
	var env []string
	for _, kv := range l.Env {
		k, v, _ := strings.Cut(kv, "=")
		if strings.HasSuffix(k, "_PATH") && strings.HasPrefix(k, wrapPrefix) {
			v = filepath.Join(root, v)
		}
		if k == "PATH" {
			v = os.Getenv("PATH")
		}
		env = append(env, k+"="+v)
	}
	out := filepath.Join(root, "env.out")
	cmd := exec.Command("sh", "-c", l.Args[1], wrapName, "sh", "-c", `env > "$0"; printf '%s|' "$@" >> "$0"`, out, "a b", "$HOME")
	cmd.Env = env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("wrapper: %v: %s", err, stderr.String())
	}
	for p, want := range plan.Files {
		got, err := os.ReadFile(filepath.Join(root, p))
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		if string(got) != want {
			t.Fatalf("file %s: %d bytes, want %d", p, len(got), len(want))
		}
	}
	dump, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read env dump: %v", err)
	}
	if !strings.Contains(string(dump), "HEADERS=x-a: 1\nx-b: 2\n\n") {
		t.Fatalf("multi-line env not restored exactly:\n%s", dump)
	}
	if strings.Contains(string(dump), wrapPrefix) {
		t.Fatalf("wrapper variables leaked into the agent env:\n%s", dump)
	}
	if !strings.HasSuffix(string(dump), "a b|$HOME|") {
		t.Fatalf("argv not passed through verbatim: %q", dump[len(dump)-20:])
	}
}

func TestCappedWriter(t *testing.T) {
	tests := []struct {
		name      string
		limit     int64
		writes    []string
		want      string
		truncated bool
	}{
		{"under", 10, []string{"abc", "def"}, "abcdef", false},
		{"exact", 6, []string{"abc", "def"}, "abcdef", false},
		{"over", 4, []string{"abc", "def", "ghi"}, "abcd", true},
		{"zero", 0, []string{"abc"}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			c := &cappedWriter{w: &buf, limit: tt.limit}
			for _, w := range tt.writes {
				n, err := c.Write([]byte(w))
				if err != nil || n != len(w) {
					t.Fatalf("Write = %d, %v", n, err)
				}
			}
			if buf.String() != tt.want || c.truncated != tt.truncated {
				t.Fatalf("got %q truncated %v, want %q %v", buf.String(), c.truncated, tt.want, tt.truncated)
			}
		})
	}
}

func TestWriteEnvFile(t *testing.T) {
	p, err := writeEnvFile([]string{"A=1", "B=two words"})
	if err != nil {
		t.Fatalf("writeEnvFile: %v", err)
	}
	defer os.Remove(p)
	st, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("env file mode %v is readable by others", st.Mode())
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "A=1\nB=two words\n" {
		t.Fatalf("content %q", got)
	}
}
