package swebench

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGradeScriptLanguageJUnit: the JUnit report of the JavaScript or
// TypeScript adapter and of the Go adapter grades to the right FAIL_TO_PASS
// and PASS_TO_PASS results, with ids written as classname::name.
func TestGradeScriptLanguageJUnit(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "grade.py")
	if err := os.WriteFile(script, []byte(gradeScript), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, fixture string
		tests         []specTest
		want          []string
	}{
		{"node", "../../issuebench/lang/node/testdata/junit.txt", []specTest{
			{Kind: kindF2P, ID: "src/a.test.ts::math > divides", Name: "src/a.test.ts::math > divides"},
			{Kind: kindP2P, ID: "src/a.test.ts::math > add > sums", Name: "src/a.test.ts::math > add > sums"},
		}, []string{`tests="2" failures="1" errors="0"`,
			`<testcase classname="fail_to_pass" name="src/a.test.ts::math &gt; divides"><failure message="status FAILED"/>`,
			`<testcase classname="pass_to_pass" name="src/a.test.ts::math &gt; add &gt; sums"></testcase>`}},
		{"go", "../../issuebench/lang/golang/testdata/junit.txt", []specTest{
			{Kind: kindF2P, ID: "./pkg::TestB", Name: "./pkg::TestB"},
			{Kind: kindP2P, ID: "./pkg::TestA", Name: "./pkg::TestA"},
		}, []string{`tests="2" failures="1" errors="0"`,
			`<testcase classname="fail_to_pass" name="./pkg::TestB"><failure message="status FAILED"/>`,
			`<testcase classname="pass_to_pass" name="./pkg::TestA"></testcase>`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, err := os.ReadFile(c.fixture)
			if err != nil {
				t.Fatal(err)
			}
			// The fixture is the printed command output; the converter's
			// file at $BENCH_JUNIT is the XML part only.
			xml := string(raw)[strings.Index(string(raw), "<?xml"):]
			junit := filepath.Join(dir, c.name+"-junit.xml")
			if err := os.WriteFile(junit, []byte(xml), 0o644); err != nil {
				t.Fatal(err)
			}
			b, err := json.Marshal(testSpec{Instance: "x", Rule: "junit", LogParser: parserPytest, Tests: c.tests})
			if err != nil {
				t.Fatal(err)
			}
			spec := filepath.Join(dir, c.name+".json")
			if err := os.WriteFile(spec, b, 0o644); err != nil {
				t.Fatal(err)
			}
			log := filepath.Join(dir, c.name+".log")
			if err := os.WriteFile(log, raw, 0o644); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(dir, c.name+"-out.xml")
			cmd := exec.Command(py, "-I", "-S", "-B", script, "--out", out, "--spec", spec, "--log", log, "--exit", "1", "--junit", junit)
			if o, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("grade.py: %v: %s", err, o)
			}
			got, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range c.want {
				if !strings.Contains(string(got), w) {
					t.Errorf("report lacks %q:\n%s", w, got)
				}
			}
		})
	}
}
