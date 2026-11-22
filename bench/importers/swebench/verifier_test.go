package swebench

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGradeScriptParsers checks that grade.py implements every parser the
// Go tables can select.
func TestGradeScriptParsers(t *testing.T) {
	for _, p := range []string{parserPytest, parserPytestOptions, parserPytestV2, parserDjango, parserSympy, parserSeaborn, parserMatplotlib} {
		if !strings.Contains(gradeScript, `"`+p+`": `) {
			t.Errorf("grade.py has no parser %s", p)
		}
	}
	for _, want := range []string{`git -c safe.directory='*' apply`, "reset_path", `grade --log /tmp/bench-test.log --exit "$code" --junit "$BENCH_JUNIT"`} {
		if !strings.Contains(verifyScript, want) {
			t.Errorf("verify.sh lacks %q", want)
		}
	}
}

// TestGradeScript runs grade.py on a hand-written pytest log. It needs a
// python3 on the host and is skipped without one.
func TestGradeScript(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "grade.py")
	if err := os.WriteFile(script, []byte(gradeScript), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := func(rule string, tests ...specTest) string {
		b, err := json.Marshal(testSpec{Instance: "x", Rule: rule, LogParser: parserPytest, Tests: tests})
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, rule+".json")
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	tests := []specTest{
		{Kind: kindF2P, ID: "tests/test_core.py::test_add[a", Name: "tests/test_core.py:test_add[a"},
		{Kind: kindP2P, ID: "tests/test_core.py::test_zero", Name: "tests/test_core.py:test_zero"},
		{Kind: kindP2P, ID: "tests/test_core.py::test_sub", Name: "tests/test_core.py:test_sub"},
		{Kind: kindP2P, ID: "tests/test_core.py::test_gone", Name: "tests/test_core.py:test_gone"},
	}
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"parsed log", []string{"--spec", spec(ruleTests, tests...), "--log", filepath.Join("testdata", "pytest.log"), "--exit", "1"},
			[]string{`tests="4" failures="2" errors="0"`, `<testcase classname="fail_to_pass" name="tests/test_core.py:test_add[a"></testcase>`,
				`name="tests/test_core.py:test_sub"><failure message="status FAILED"/>`, `name="tests/test_core.py:test_gone"><failure message="not found in the test log"/>`}},
		{"junit wins over the log", []string{"--spec", spec("junit", append(tests, specTest{Kind: kindP2P, ID: "tests/test_core.py::TestMath::test_mul[2-4]", Name: "tests/test_core.py:TestMath.test_mul[2-4]"})...),
			"--log", filepath.Join("testdata", "pytest.log"), "--exit", "1", "--junit", filepath.Join("testdata", "junit.xml")},
			[]string{`tests="5" failures="2" errors="0"`, `name="tests/test_core.py:test_zero"><failure message="status FAILED"/>`,
				`name="tests/test_core.py:test_gone"></testcase>`, `name="tests/test_core.py:TestMath.test_mul[2-4]"></testcase>`,
				`name="tests/test_core.py:test_sub"><failure message="status FAILED"/>`}},
		{"missing junit falls back to the log", []string{"--spec", spec(ruleTests, tests...), "--log", filepath.Join("testdata", "pytest.log"), "--exit", "1", "--junit", filepath.Join(dir, "none.xml")},
			[]string{`tests="4" failures="2" errors="0"`}},
		{"exit code pass", []string{"--spec", spec(ruleExitCode, specTest{Kind: "test_command", ID: "exit", Name: "exit_status"}), "--exit", "0"},
			[]string{`tests="1" failures="0"`, `<testcase classname="test_command" name="exit_status"></testcase>`}},
		{"exit code fail", []string{"--spec", spec(ruleExitCode, specTest{Kind: "test_command", ID: "exit", Name: "exit_status"}), "--exit", "2"},
			[]string{`failures="1"`, `message="test command exited 2"`}},
		{"broken", []string{"--spec", spec(ruleTests, tests...), "--broken", "test patch does not apply"},
			[]string{`errors="4"`, `<error message="test patch does not apply"/>`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := filepath.Join(dir, "junit.xml")
			cmd := exec.Command(py, append([]string{"-I", "-S", "-B", script, "--out", out}, c.args...)...)
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("grade.py: %v: %s", err, b)
			}
			b, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range c.want {
				if !strings.Contains(string(b), w) {
					t.Errorf("junit lacks %q:\n%s", w, b)
				}
			}
		})
	}
}

// TestVerifyScriptImportsTheCopy: on a read-only workspace the tests run in
// a copy, and the image's development install still points at the
// workspace, so the copy must come first on PYTHONPATH before cmd.sh runs
// (else in-package tests import the workspace's old test modules).
func TestVerifyScriptImportsTheCopy(t *testing.T) {
	set := strings.Index(verifyScript, `export PYTHONPATH="$W${PYTHONPATH:+:$PYTHONPATH}"`)
	run := strings.Index(verifyScript, `bash "$G/cmd.sh"`)
	guard := strings.Index(verifyScript, `if [ "$W" != "$SRC" ]; then`)
	if set < 0 || run < 0 || guard < 0 || !(guard < set && set < run) {
		t.Fatalf("verify.sh must export PYTHONPATH=$W, only when it works on a copy, before running cmd.sh (guard %d, set %d, run %d)", guard, set, run)
	}
	if err := exec.Command("bash", "-n", "-c", verifyScript).Run(); err != nil {
		t.Fatalf("verify.sh does not parse: %v", err)
	}
}
