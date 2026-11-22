package python

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/issuebench"
)

func TestRegistered(t *testing.T) {
	for _, l := range issuebench.Languages() {
		if l.Name() == "python" {
			return
		}
	}
	t.Fatal("python is not registered")
}

func TestDetect(t *testing.T) {
	p := Python{}
	for _, d := range []string{"testdata/proj", "testdata/legacy"} {
		if !p.Detect(d) {
			t.Errorf("Detect(%s) = false", d)
		}
	}
	dir := t.TempDir()
	if p.Detect(dir) {
		t.Error("empty dir detected as python")
	}
	os.MkdirAll(filepath.Join(dir, "requirements"), 0o755)
	os.WriteFile(filepath.Join(dir, "requirements", "base.txt"), []byte("requests\n"), 0o644)
	if !p.Detect(dir) {
		t.Error("requirements/base.txt not detected")
	}
}

func TestIsTestFile(t *testing.T) {
	p := Python{}
	for path, want := range map[string]bool{
		"tests/test_a.py":           true,
		"tests/data/input.json":     true,
		"pkg/test/helpers.py":       true,
		"pkg/test_io.py":            true,
		"pkg/io_test.py":            true,
		"conftest.py":               true,
		"src/pkg/conftest.py":       true,
		"./testing/test_x.py":       true,
		`pkg\tests\x.py`:            true,
		"pkg/io.py":                 false,
		"pkg/testing.py":            false,
		"pkg/latest.py":             false,
		"docs/test_guide.md":        false,
		"pkg/contest.py":            false,
		"pkg/attestations/check.py": false,
	} {
		if got := p.IsTestFile(path); got != want {
			t.Errorf("IsTestFile(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestBaseImages(t *testing.T) {
	all := Python{}.BaseImages()
	if all[0] != "python:3.13-slim" || all[len(all)-1] != "python:3.8-slim" || len(all) != 6 {
		t.Fatalf("BaseImages = %v", all)
	}
	got := PickBaseImages("testdata/proj")
	want := []string{"python:3.11-slim", "python:3.10-slim", "python:3.12-slim", "python:3.9-slim"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PickBaseImages(proj) = %v, want %v", got, want)
	}
	got = PickBaseImages("testdata/legacy")
	if got[0] != "python:3.9-slim" || got[1] != "python:3.8-slim" || len(got) != 6 {
		t.Errorf("PickBaseImages(legacy) = %v", got)
	}
	if got := PickBaseImages(t.TempDir()); !reflect.DeepEqual(got, all) {
		t.Errorf("PickBaseImages(empty) = %v", got)
	}
}

func TestAllows(t *testing.T) {
	cases := []struct {
		spec  string
		minor int
		want  bool
	}{
		{">=3.9", 8, false}, {">=3.9", 13, true}, {">=3.8,<3.11", 11, false}, {">=3.8, <3.11", 10, true},
		{"~=3.10", 9, false}, {"~=3.10", 12, true}, {"==3.*", 8, true}, {"!=3.9.*", 9, false}, {"!=3.9.*", 10, true},
		{">3.10", 10, false}, {"<4", 13, true}, {"<=3.9", 10, false}, {"", 8, true}, {">=2.7, !=3.0.*", 8, true},
	}
	for _, c := range cases {
		if got := allows(c.spec, c.minor); got != c.want {
			t.Errorf("allows(%q, 3.%d) = %v, want %v", c.spec, c.minor, got, c.want)
		}
	}
}

func TestHints(t *testing.T) {
	h := strings.Join(Python{}.Hints("testdata/proj"), "\n")
	for _, w := range []string{
		"requires-python: >=3.9,<3.13", "CI tests Python 3.10, 3.11", "base images to try in order: python:3.11-slim",
		"has pyproject.toml", "test extras: testing, dev", "pytest config in pyproject.toml",
		"CI (.github/workflows/ci.yml): pip install -e '.[testing]'", "CI (.github/workflows/ci.yml): python -m pytest -q tests",
	} {
		if !strings.Contains(h, w) {
			t.Errorf("hints lack %q:\n%s", w, h)
		}
	}
	h = strings.Join(Python{}.Hints("testdata/legacy"), "\n")
	for _, w := range []string{"requires-python: >=3.6", "CI tests Python 3.8, 3.9", "test extras: test", "pytest config in tox.ini"} {
		if !strings.Contains(h, w) {
			t.Errorf("legacy hints lack %q:\n%s", w, h)
		}
	}
}

func TestPin(t *testing.T) {
	at := time.Date(2024, 3, 5, 12, 0, 0, 0, time.FixedZone("x", 3600))
	env := Python{}.PinEnv(at)
	if !contains(env, "UV_EXCLUDE_NEWER=2024-03-05T11:00:00Z") {
		t.Errorf("PinEnv = %v", env)
	}
	for _, e := range (Python{}).PinEnv(time.Time{}) {
		if strings.HasPrefix(e, "UV_EXCLUDE_NEWER") {
			t.Errorf("zero time pinned: %v", e)
		}
	}
	if got := InstallCommand(at, "-e", ".[test]"); got != "uv pip install --exclude-newer 2024-03-05T11:00:00Z -e '.[test]'" {
		t.Errorf("InstallCommand = %s", got)
	}
}

func TestTestCommand(t *testing.T) {
	got := Python{}.TestCommand([]string{"tests/test_core.py::test_add[a b]", "tests/test_core.py::test_zero"})
	want := `python -m pytest -rA --tb=short -p no:cacheprovider -o junit_family=xunit1 --junitxml="${BENCH_JUNIT:-/tmp/bench-junit.xml}" 'tests/test_core.py::test_add[a b]' tests/test_core.py::test_zero`
	if got != want {
		t.Errorf("TestCommand =\n%s\nwant\n%s", got, want)
	}
}

func TestParseSummary(t *testing.T) {
	b, _ := os.ReadFile("testdata/rA.log")
	got, err := Python{}.ParseResults(b)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]issuebench.TestStatus{
		"tests/test_core.py::test_zero":               issuebench.TestPass,
		"tests/test_core.py::test_add[a b]":           issuebench.TestPass,
		"tests/test_core.py::TestMath::test_mul[2-4]": issuebench.TestPass,
		"tests/test_core.py::test_known":              issuebench.TestPass,
		"tests/test_core.py::test_sub":                issuebench.TestFail,
		"tests/test_core.py::test_db":                 issuebench.TestError,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseResults(rA.log) =\n%v\nwant\n%v", got, want)
	}
	v := ParseSummary([]byte("\x1b[32mtests/a.py::test_x PASSED\x1b[0m   [ 50%]\ntests/a.py::test_y FAILED [100%]\n"))
	if v["tests/a.py::test_x"] != issuebench.TestPass || v["tests/a.py::test_y"] != issuebench.TestFail {
		t.Errorf("verbose lines = %v", v)
	}
}

func TestParseJUnit(t *testing.T) {
	b, _ := os.ReadFile("testdata/junit_xunit1.xml")
	got, err := Python{}.ParseResults(b)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]issuebench.TestStatus{
		"tests/test_core.py::test_zero":               issuebench.TestPass,
		"tests/test_core.py::TestMath::test_mul[2-4]": issuebench.TestPass,
		"tests/test_core.py::test_sub":                issuebench.TestFail,
		"tests/test_core.py::test_db":                 issuebench.TestError,
		"tests/test_core.py::test_net":                issuebench.TestSkip,
		"tests/test_core.py::test_known":              issuebench.TestPass,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("xunit1 =\n%v\nwant\n%v", got, want)
	}
	b, _ = os.ReadFile("testdata/junit_xunit2.xml")
	got, err = ParseJUnit(b)
	if err != nil {
		t.Fatal(err)
	}
	want = map[string]issuebench.TestStatus{
		"src/pkg/tests/test_io.py::test_read":                    issuebench.TestPass,
		"src/pkg/tests/test_io.py::TestWrite::test_write[utf-8]": issuebench.TestFail,
		"test_top.py::test_x":                                    issuebench.TestPass,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("xunit2 =\n%v\nwant\n%v", got, want)
	}
	if _, err := ParseJUnit([]byte("<html></html>")); err == nil {
		t.Error("no error for a non-JUnit document")
	}
}

// TestParseMixed: a log with the report appended; JUnit wins where both
// name a test, the summary fills in the rest.
func TestParseMixed(t *testing.T) {
	log := []byte("PASSED tests/test_core.py::test_sub\nPASSED tests/test_core.py::test_only_log\n")
	x, _ := os.ReadFile("testdata/junit_xunit1.xml")
	got, err := Python{}.ParseResults(append(log, x...))
	if err != nil {
		t.Fatal(err)
	}
	if got["tests/test_core.py::test_sub"] != issuebench.TestFail || got["tests/test_core.py::test_only_log"] != issuebench.TestPass {
		t.Errorf("mixed = %v", got)
	}
	if _, err := (Python{}).ParseResults([]byte("<testsuite broken")); err == nil {
		t.Error("no error for a broken report with nothing else")
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// pytest escapes non-ASCII parameter ids (user@m\xfcnchen.de); turning that
// backslash into "/" made the emitted ids miss the verifier's log, so the
// reference answer failed admission (marshmallow #2936, real hosted run).
func TestNormalizeIDKeepsParameterEscapes(t *testing.T) {
	cases := map[string]string{
		`tests\test_validate.py::test_email_idn_invalid[user@m\xfcnchen.de]`: `tests/test_validate.py::test_email_idn_invalid[user@m\xfcnchen.de]`,
		`./tests/test_validate.py::test_url[https://example.org\n]`:          `tests/test_validate.py::test_url[https://example.org\n]`,
		` tests\a.py `: `tests/a.py`,
	}
	for in, want := range cases {
		if got := NormalizeID(in); got != want {
			t.Errorf("NormalizeID(%q) = %q, want %q", in, got, want)
		}
	}
}
