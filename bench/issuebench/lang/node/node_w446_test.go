package node

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/issuebench"
)

func writePkg(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestNodeDetect(t *testing.T) {
	cases := []struct {
		name, pkg string
		want      bool
	}{
		{"jest in devDependencies", `{"devDependencies":{"jest":"^29.0.0"}}`, true},
		{"vitest in scripts", `{"scripts":{"test":"vitest run"}}`, true},
		{"neither", `{"dependencies":{"left-pad":"1.0.0"},"scripts":{"test":"mocha"}}`, false},
	}
	for _, c := range cases {
		if got := (Node{name: "javascript"}).Detect(writePkg(t, c.pkg)); got != c.want {
			t.Errorf("%s: Detect = %v, want %v", c.name, got, c.want)
		}
	}
	if (Node{}).Detect(t.TempDir()) {
		t.Error("a folder without package.json was detected")
	}
}

func TestNodeRegistersBothNames(t *testing.T) {
	for _, n := range []string{"javascript", "typescript", "TypeScript"} {
		if issuebench.LanguageFor(n) == nil {
			t.Errorf("LanguageFor(%q) is nil", n)
		}
	}
}

func TestNodeIsTestFile(t *testing.T) {
	yes := []string{"src/a.test.ts", "b.spec.jsx", "lib/__tests__/x.js", "c.test.mjs"}
	no := []string{"src/a.ts", "node_modules/x/a.test.js", "README.md"}
	for _, p := range yes {
		if !(Node{}).IsTestFile(p) {
			t.Errorf("IsTestFile(%q) = false", p)
		}
	}
	for _, p := range no {
		if (Node{}).IsTestFile(p) {
			t.Errorf("IsTestFile(%q) = true", p)
		}
	}
}

func TestNodeTestCommandGroupsFiles(t *testing.T) {
	cmd := Node{}.TestCommand([]string{"a.test.ts::x > y", "a.test.ts::z"})
	if n := strings.Count(cmd, "a.test.ts"); n != 2 {
		t.Fatalf("a.test.ts appears %d times, want once per runner branch (2): %s", n, cmd)
	}
	if !strings.Contains(cmd, "/tmp/h-results.json a.test.ts;") {
		t.Errorf("file argument missing: %s", cmd)
	}
	if !strings.HasSuffix(cmd, "exit $rc") || !strings.Contains(cmd, "node /heimdall/results2junit.js") {
		t.Errorf("command does not convert and keep the exit code: %s", cmd)
	}
	all := Node{}.TestCommand(nil)
	if !strings.Contains(all, "--outputFile=/tmp/h-results.json; else") {
		t.Errorf("empty ids should run all tests: %s", all)
	}
	if p := (Node{}).TestCommand([]string{"{tests}"}); !strings.Contains(p, "h-results.json {tests};") {
		t.Errorf("placeholder must stay unquoted: %s", p)
	}
}

func TestNodeParseResultsJUnit(t *testing.T) {
	out, err := os.ReadFile("testdata/junit.txt")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Node{}.ParseResults(out)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]issuebench.TestStatus{
		"src/a.test.ts::math > add > sums": issuebench.TestPass,
		"src/a.test.ts::math > divides":    issuebench.TestFail,
		"src/a.test.ts::later":             issuebench.TestSkip,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if _, err := (Node{}).ParseResults([]byte("no report here")); err == nil {
		t.Error("output without a report gave no error")
	}
}

func TestResults2JUnitScript(t *testing.T) {
	nodeBin, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on PATH")
	}
	script := filepath.Join(t.TempDir(), "results2junit.js")
	if err := os.WriteFile(script, Node{}.SupportFiles()["results2junit.js"], 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		fixture string
		want    map[string]issuebench.TestStatus
	}{
		{"jest.json", map[string]issuebench.TestStatus{
			"src/a.test.ts::math > add > sums": issuebench.TestPass,
			"src/a.test.ts::math > divides":    issuebench.TestFail,
			"src/a.test.ts::later":             issuebench.TestSkip,
		}},
		{"vitest.json", map[string]issuebench.TestStatus{
			`test/b.spec.js::parser > reads <tags> & "quotes"`: issuebench.TestPass,
			"test/b.spec.js::parser > todo":                    issuebench.TestSkip,
		}},
	} {
		fx, _ := filepath.Abs(filepath.Join("testdata", c.fixture))
		cmd := exec.Command(nodeBin, script, fx)
		cmd.Dir = "/testbed"
		if _, err := os.Stat("/testbed"); err != nil {
			cmd.Dir = t.TempDir()
		}
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s: %v", c.fixture, err)
		}
		got, err := Node{}.ParseResults(out)
		if err != nil {
			t.Fatalf("%s: %v\n%s", c.fixture, err, out)
		}
		for k, v := range c.want {
			// Outside /testbed the converter keeps the absolute path's
			// relative form; compare on the suffix after the folder.
			found := false
			for gk, gv := range got {
				if strings.HasSuffix(gk, k) && gv == v {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: no case %q with %q in %v", c.fixture, k, v, got)
			}
		}
	}
}

func TestNodeToolchain(t *testing.T) {
	tc := issuebench.ToolchainOf(Node{name: "typescript"})
	if !tc.ValidTestCommand("npx vitest run {tests}") || tc.ValidTestCommand("python -m pytest {tests}") {
		t.Error("ValidTestCommand is wrong")
	}
	if !tc.ManifestFile("pkg/package.json") || !tc.ManifestFile("yarn.lock") || tc.ManifestFile("src/index.ts") {
		t.Error("ManifestFile is wrong")
	}
	if len(tc.BuilderNotes()) > 1200 {
		t.Error("BuilderNotes longer than 1200 characters")
	}
}
