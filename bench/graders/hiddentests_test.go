package graders

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestScoreGrade(t *testing.T) {
	required := []string{"test_hidden_calc.py::test_add"}
	tests := []struct {
		name     string
		exit     int
		xml      string
		required []string
		want     schemas.Grade
	}{
		{
			name: "pass", exit: 0, xml: junitPass, required: required,
			want: schemas.Grade{Passed: true, TestsRan: 3, Reason: "passed (3 tests)"},
		},
		{
			name: "failure", exit: 1, xml: junitFail, required: required,
			want: schemas.Grade{ExitCode: 1, TestsRan: 2, Failures: 1, FailedTests: []string{"tests.test_hidden_calc::test_add_negative"}, Reason: "exit 1; 1 failures"},
		},
		{
			name: "skipped", exit: 0, xml: junitSkipped, required: []string{"test_hidden_calc.py::test_add_negative"},
			want: schemas.Grade{TestsRan: 2, Skipped: 1, Reason: "1 skipped"},
		},
		{
			name: "zero tests is a fail", exit: 5, xml: junitZero, required: required,
			want: schemas.Grade{ExitCode: 5, Reason: "test run broken by the patch: no tests ran (exit 5)"},
		},
		{
			name: "no junit is a fail", exit: 0, xml: "", required: required,
			want: schemas.Grade{Reason: "test run broken by the patch: no junit report (exit 0)"},
		},
		{
			name: "unreadable junit is a fail", exit: 0, xml: "<oops", required: required,
			want: schemas.Grade{Reason: "test run broken by the patch: parse junit: XML syntax error on line 1: unexpected EOF"},
		},
		{
			name: "missing required id fails", exit: 0, xml: junitPass, required: []string{"test_hidden_calc.py::test_mul"},
			want: schemas.Grade{TestsRan: 3, MissingRequired: []string{"test_hidden_calc.py::test_mul"}, Reason: "required tests did not run: test_hidden_calc.py::test_mul"},
		},
		{
			name: "clean junit with nonzero exit fails", exit: 3, xml: junitPass, required: required,
			want: schemas.Grade{ExitCode: 3, TestsRan: 3, Reason: "exit 3"},
		},
		{
			name: "collection error fails", exit: 2, xml: junitError, required: required,
			want: schemas.Grade{ExitCode: 2, TestsRan: 1, Errors: 1, FailedTests: []string{"tests.test_hidden_calc"}, MissingRequired: []string{"test_hidden_calc.py::test_add"},
				Reason: "exit 2; 1 errors; required tests did not run: test_hidden_calc.py::test_add"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := schemas.Grade{}
			scoreGrade(&got, tt.exit, []byte(tt.xml), tt.required)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestReadTarFile(t *testing.T) {
	mk := func(hdr *tar.Header, body string) []byte {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if body != "" {
			if _, err := tw.Write([]byte(body)); err != nil {
				t.Fatal(err)
			}
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	tests := []struct {
		name    string
		data    []byte
		limit   int64
		want    string
		wantErr bool
	}{
		{"regular file", mk(&tar.Header{Name: "junit.xml", Mode: 0o644, Size: 5, Typeflag: tar.TypeReg}, "<x/>\n"), 100, "<x/>\n", false},
		{"symlink reads as nothing", mk(&tar.Header{Name: "junit.xml", Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink}, ""), 100, "", false},
		{"empty stream", nil, 100, "", false},
		{"over limit", mk(&tar.Header{Name: "junit.xml", Mode: 0o644, Size: 5, Typeflag: tar.TypeReg}, "<x/>\n"), 4, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readTarFile(bytes.NewReader(tt.data), tt.limit)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if string(got) != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInputs(t *testing.T) {
	taskDir, err := filepath.Abs("testdata/task")
	if err != nil {
		t.Fatal(err)
	}
	diffPath := filepath.Join("testdata", "patches", "pass.diff")
	task := &schemas.Task{Dir: taskDir, RepoDir: "repo", Verifier: schemas.TaskVerifier{Tests: "verifier/tests"}}
	suite := func(restore ...string) *schemas.Suite {
		return &schemas.Suite{
			Version:   "1.0.0",
			BaseImage: schemas.ImageSpec{Tag: "img:1"},
			Verifier:  schemas.SuiteVerifier{Command: []string{"python", "-m", "pytest"}, Restore: restore},
		}
	}
	g := newHiddenTests(HiddenTestsConfig{})
	linked := t.TempDir()
	if err := os.Mkdir(filepath.Join(linked, "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"tests": taskDir, "hostetc": "/etc"} {
		if err := os.Symlink(target, filepath.Join(linked, name)); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name    string
		rc      *schemas.RunContext
		diff    *schemas.Diff
		wantErr string
	}{
		{"ok", &schemas.RunContext{Task: task, Suite: suite("tests")}, &schemas.Diff{Path: diffPath}, ""},
		{"no diff", &schemas.RunContext{Task: task, Suite: suite()}, nil, "no diff"},
		{"sha mismatch", &schemas.RunContext{Task: task, Suite: suite()}, &schemas.Diff{Path: diffPath, SHA256: "00"}, "export recorded"},
		{"escaping restore path", &schemas.RunContext{Task: task, Suite: suite("../x")}, &schemas.Diff{Path: diffPath}, "clean relative path"},
		{"absolute restore path", &schemas.RunContext{Task: task, Suite: suite("/etc")}, &schemas.Diff{Path: diffPath}, "clean relative path"},
		{"missing tests folder", &schemas.RunContext{Task: &schemas.Task{Dir: taskDir, RepoDir: "repo", Verifier: schemas.TaskVerifier{Tests: "nope"}}, Suite: suite()}, &schemas.Diff{Path: diffPath}, "nope not found"},
		{"tests folder links out of the task", &schemas.RunContext{Task: &schemas.Task{Dir: linked, RepoDir: "repo", Verifier: schemas.TaskVerifier{Tests: "tests"}}, Suite: suite()}, &schemas.Diff{Path: diffPath},
			"verifier.tests: tests: resolves outside the folder through a symlink"},
		{"repo links out of the task", &schemas.RunContext{Task: &schemas.Task{Dir: linked, RepoDir: "hostetc", Verifier: schemas.TaskVerifier{Tests: "repo"}}, Suite: suite()}, &schemas.Diff{Path: diffPath},
			"task repo: hostetc: resolves outside the folder through a symlink"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in, err := g.inputs(tt.rc, tt.diff)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("inputs: %v", err)
			}
			if in.image != "img:1" || in.timeout.Seconds() != defaultVerifierS || !filepath.IsAbs(in.diffPath) || in.empty {
				t.Fatalf("inputs = %+v", in)
			}
		})
	}
}

func TestInputsEmptyDiff(t *testing.T) {
	taskDir, err := filepath.Abs("testdata/task")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "workspace.diff")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	rc := &schemas.RunContext{
		Task:  &schemas.Task{Dir: taskDir, RepoDir: "repo", Verifier: schemas.TaskVerifier{Tests: "verifier/tests"}},
		Suite: &schemas.Suite{Verifier: schemas.SuiteVerifier{Command: []string{"pytest"}}},
	}
	in, err := newHiddenTests(HiddenTestsConfig{Image: "x"}).inputs(rc, &schemas.Diff{Path: p})
	if err != nil {
		t.Fatalf("inputs: %v", err)
	}
	if !in.empty || in.image != "x" {
		t.Fatalf("inputs = %+v", in)
	}
}

func TestCappedBuffer(t *testing.T) {
	c := &cappedBuffer{limit: 5}
	for _, w := range []string{"abc", "def", "ghi"} {
		if n, err := c.Write([]byte(w)); n != len(w) || err != nil {
			t.Fatalf("Write = %d, %v", n, err)
		}
	}
	if c.String() != "abcde" {
		t.Fatalf("got %q", c.String())
	}
}
