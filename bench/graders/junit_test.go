package graders

import (
	"reflect"
	"strings"
	"testing"
)

const (
	junitPass = `<?xml version="1.0" encoding="utf-8"?><testsuites><testsuite name="pytest" errors="0" failures="0" skipped="0" tests="3" time="0.03"><testcase classname="tests.test_hidden_calc" name="test_add" time="0.001"/><testcase classname="tests.test_hidden_calc" name="test_add_negative" time="0.001"/><testcase classname="tests.test_hidden_calc.TestBig" name="test_big[1]" time="0.001"/></testsuite></testsuites>`
	junitFail = `<?xml version="1.0" encoding="utf-8"?><testsuites><testsuite name="pytest" errors="0" failures="1" skipped="0" tests="2" time="0.028"><testcase classname="tests.test_hidden_calc" name="test_add" time="0.001"/><testcase classname="tests.test_hidden_calc" name="test_add_negative" time="0.000"><failure message="assert 1 == -5">def test_add_negative():
&gt;       assert add(-2, -3) == -5
E       assert 1 == -5</failure></testcase></testsuite></testsuites>`
	junitSkipped = `<testsuites><testsuite name="pytest" errors="0" failures="0" skipped="1" tests="2"><testcase classname="tests.test_hidden_calc" name="test_add"><skipped type="pytest.skip" message="nope"/></testcase><testcase classname="tests.test_hidden_calc" name="test_add_negative"/></testsuite></testsuites>`
	junitZero    = `<?xml version="1.0" encoding="utf-8"?><testsuites><testsuite name="pytest" errors="0" failures="0" skipped="0" tests="0" time="0.01"></testsuite></testsuites>`
	junitError   = `<testsuites><testsuite name="pytest" errors="1" failures="0" skipped="0" tests="1"><testcase classname="" name="tests.test_hidden_calc"><error message="collection failure">ImportError</error></testcase></testsuite></testsuites>`
	// junitUnderstated claims no failures while a testcase failed.
	junitUnderstated = `<testsuite tests="1" failures="0" errors="0" skipped="0"><testcase classname="tests.test_hidden_calc" name="test_add"><failure/></testcase></testsuite>`
	junitNoAttrs     = `<testsuite><testcase classname="t.test_x" name="test_a"/><testcase classname="t.test_x" name="test_b"><error/></testcase></testsuite>`
)

func TestParseJUnit(t *testing.T) {
	tests := []struct {
		name                             string
		xml                              string
		tests, failures, errors, skipped int
		cases                            int
		wantErr                          bool
	}{
		{name: "pass", xml: junitPass, tests: 3, cases: 3},
		{name: "failure", xml: junitFail, tests: 2, failures: 1, cases: 2},
		{name: "skipped", xml: junitSkipped, tests: 2, skipped: 1, cases: 2},
		{name: "zero tests", xml: junitZero},
		{name: "collection error", xml: junitError, tests: 1, errors: 1, cases: 1},
		{name: "understated failures", xml: junitUnderstated, tests: 1, failures: 1, cases: 1},
		{name: "testsuite root without attributes", xml: junitNoAttrs, tests: 2, errors: 1, cases: 2},
		{name: "empty testsuites", xml: `<testsuites/>`},
		{name: "not xml", xml: `garbage`, wantErr: true},
		{name: "wrong root", xml: `<html/>`, wantErr: true},
		{name: "bad count", xml: `<testsuite tests="many"/>`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := parseJUnit([]byte(tt.xml))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			got := []int{r.Tests, r.Failures, r.Errors, r.Skipped, len(r.Cases)}
			want := []int{tt.tests, tt.failures, tt.errors, tt.skipped, tt.cases}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("tests/failures/errors/skipped/cases = %v, want %v", got, want)
			}
		})
	}
}

func TestCheckRequired(t *testing.T) {
	tests := []struct {
		name     string
		xml      string
		required []string
		missing  []string
		failed   []string
		wantErr  bool
	}{
		{name: "ran and passed", xml: junitPass, required: []string{"test_hidden_calc.py::test_add"}},
		{name: "folder prefix in id", xml: junitPass, required: []string{"tests/test_hidden_calc.py::test_add_negative"}},
		{name: "class and parametrized", xml: junitPass, required: []string{"test_hidden_calc.py::TestBig::test_big"}},
		{name: "not present", xml: junitPass, required: []string{"test_hidden_calc.py::test_mul"}, missing: []string{"test_hidden_calc.py::test_mul"}},
		{name: "wrong module", xml: junitPass, required: []string{"other_test_hidden_calc.py::test_add"}, missing: []string{"other_test_hidden_calc.py::test_add"}},
		{name: "name prefix is not a match", xml: junitPass, required: []string{"test_hidden_calc.py::test_ad"}, missing: []string{"test_hidden_calc.py::test_ad"}},
		{name: "failed", xml: junitFail, required: []string{"test_hidden_calc.py::test_add_negative"}, failed: []string{"test_hidden_calc.py::test_add_negative"}},
		{name: "skipped counts as missing", xml: junitSkipped, required: []string{"test_hidden_calc.py::test_add"}, missing: []string{"test_hidden_calc.py::test_add"}},
		{name: "malformed id", xml: junitPass, required: []string{"test_add"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := parseJUnit([]byte(tt.xml))
			if err != nil {
				t.Fatal(err)
			}
			got, err := checkRequired(r, tt.required)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if !reflect.DeepEqual(got.Missing, tt.missing) || !reflect.DeepEqual(got.Failed, tt.failed) {
				t.Fatalf("missing %v failed %v, want %v %v", got.Missing, got.Failed, tt.missing, tt.failed)
			}
		})
	}
}

func TestSplitTestID(t *testing.T) {
	cls, name, err := splitTestID("tests/sub/test_x.py::TestA::test_b")
	if err != nil || cls != "tests.sub.test_x.TestA" || name != "test_b" {
		t.Fatalf("got %q %q %v", cls, name, err)
	}
	if _, _, err := splitTestID("test_x.py::"); err == nil || !strings.Contains(err.Error(), "file.py::test") {
		t.Fatalf("err = %v", err)
	}
}
