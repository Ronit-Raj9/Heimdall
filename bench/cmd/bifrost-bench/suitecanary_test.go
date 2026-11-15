package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/utils"
)

// TestSuiteCanary runs `suite canary` on a fresh memory suite (two sessions,
// the first reusing the task instruction): without -stamp only suite.yaml
// changes; with -stamp every instruction file gains one canary line, and a
// second run changes nothing.
func TestSuiteCanary(t *testing.T) {
	dir := t.TempDir()
	if code := runInit([]string{dir}); code != exitOK {
		t.Fatalf("init exit %d", code)
	}
	config := filepath.Join(dir, "deploy", "bench.yaml")
	if code := runSuite([]string{"init", "mem", "-template", "memory", "-config", config}); code != exitOK {
		t.Fatalf("suite init exit %d", code)
	}
	suiteDir := filepath.Join(dir, "benchmarks", "mem")
	instr := []string{
		filepath.Join(suiteDir, "tasks", "launch-date", "sessions", "learn.md"),
		filepath.Join(suiteDir, "tasks", "launch-date", "sessions", "recall.md"),
	}
	before := readAll(t, instr...)
	yamlBefore := readAll(t, filepath.Join(suiteDir, "suite.yaml"))[0]

	if code := runSuite([]string{"canary", "mem", "-config", config}); code != exitOK {
		t.Fatalf("suite canary exit %d", code)
	}
	yamlAfter := readAll(t, filepath.Join(suiteDir, "suite.yaml"))[0]
	guid := utils.SuiteYAMLCanary([]byte(yamlAfter))
	if !utils.ValidCanary(guid) {
		t.Fatalf("suite.yaml has no valid canary:\n%s", yamlAfter)
	}
	if yamlAfter != yamlBefore+"canary: "+guid+"\n" {
		t.Errorf("suite.yaml changed beyond the canary line:\n%s", yamlAfter)
	}
	if got := readAll(t, instr...); strings.Join(got, "") != strings.Join(before, "") {
		t.Error("suite canary without -stamp changed an instruction file")
	}

	if code := runSuite([]string{"canary", "-stamp", "mem", "-config", config}); code != exitOK {
		t.Fatalf("suite canary -stamp exit %d", code)
	}
	stamped := readAll(t, instr...)
	for i, s := range stamped {
		if strings.Count(s, guid) != 1 || !strings.HasPrefix(s, before[i]) {
			t.Errorf("%s not stamped once after its text:\n%s", instr[i], s)
		}
	}
	if code := runSuite([]string{"canary", "-stamp", "mem", "-config", config}); code != exitOK {
		t.Fatalf("second suite canary -stamp exit %d", code)
	}
	if got := readAll(t, instr...); strings.Join(got, "") != strings.Join(stamped, "") {
		t.Error("second suite canary -stamp changed an instruction file")
	}
	if got := readAll(t, filepath.Join(suiteDir, "suite.yaml"))[0]; got != yamlAfter {
		t.Error("second suite canary changed suite.yaml")
	}
	// The canary key passes the suite schema and the stamped suite still
	// loads and lints.
	if code := runSuite([]string{"lint", "mem", "-config", config}); code != exitOK {
		t.Errorf("suite lint after stamping: exit %d", code)
	}
	for _, args := range [][]string{
		{"canary", "-config", config},
		{"canary", "a", "b", "-config", config},
	} {
		if code := runSuite(args); code != exitUsage {
			t.Errorf("suite %v: exit %d, want %d", args, code, exitUsage)
		}
	}
	if code := runSuite([]string{"canary", "missing", "-config", config}); code != exitInfra {
		t.Errorf("suite canary on a missing suite: exit %d, want %d", code, exitInfra)
	}
}

func TestSuiteCanaryCheck(t *testing.T) {
	root := t.TempDir()
	const guid = "0f6c1a2b-3c4d-4e5f-8a9b-0c1d2e3f4a5b"
	if err := os.MkdirAll(filepath.Join(root, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "alpha", "suite.yaml"), []byte("id: alpha\ncanary: "+guid+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	texts := t.TempDir()
	write := func(name, data string) string {
		t.Helper()
		p := filepath.Join(texts, name)
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := []struct {
		name  string
		args  []string
		stdin string
		want  int
	}{
		{"clean text", []string{"-text", write("clean.txt", "The launch is on 3 March.\n")}, "", exitOK},
		{"known guid", []string{"-text", write("known.txt", "as seen in "+strings.ToUpper(guid)+"\n")}, "", canaryCheckFound},
		{"unknown canary line", []string{"-json", "-text", write("unknown.txt", utils.CanaryText("12345678-1234-4234-8234-123456789abc"))}, "", canaryCheckFound},
		{"stdin clean", []string{"-text", "-"}, "nothing here", exitOK},
		{"stdin known", []string{"-text", "-"}, "<!-- " + utils.CanaryText(guid) + " -->", canaryCheckFound},
		{"no text flag", []string{"-json"}, "", canaryCheckUsage},
		{"stray argument", []string{"-text", "-", "extra"}, "", canaryCheckUsage},
		{"missing file", []string{"-text", filepath.Join(texts, "nope.txt")}, "", exitInfra},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.stdin != "" {
				p := write("stdin-"+strings.ReplaceAll(c.name, " ", "-"), c.stdin)
				f, err := os.Open(p)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				old := os.Stdin
				os.Stdin = f
				defer func() { os.Stdin = old }()
			}
			args := append([]string{"canary-check", "-root", root}, c.args...)
			if code := runSuite(args); code != c.want {
				t.Errorf("suite %v: exit %d, want %d", args, code, c.want)
			}
		})
	}
	if code := runSuite([]string{"canary-check", "-root", filepath.Join(root, "missing"), "-text", "-"}); code != exitInfra {
		t.Errorf("canary-check on a missing benchmarks folder: exit %d, want %d", code, exitInfra)
	}
}

// readAll reads each file as a string.
func readAll(t *testing.T, paths ...string) []string {
	t.Helper()
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, string(b))
	}
	return out
}
