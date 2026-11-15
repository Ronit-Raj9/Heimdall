package swebench

import (
	"reflect"
	"testing"
)

func TestLegacyTestCmd(t *testing.T) {
	cases := []struct {
		repo, version, want string
		ok                  bool
	}{
		{"astropy/astropy", "1.3", cmdAstropyLegacy, true},
		{"astropy/astropy", "5.1", cmdPytest, true},
		{"django/django", "1.9", cmdDjangoSerial, true},
		{"django/django", "4.0", cmdDjango, true},
		{"sympy/sympy", "1.7", cmdSympy, true},
		{"sphinx-doc/sphinx", "5.0", cmdSphinx, true},
		{"mwaskom/seaborn", "0.12", cmdSeaborn, true},
		{"pydata/xarray", "0.12", cmdPytest, true},
		{"acme/unknown", "1.0", "", false},
	}
	for _, c := range cases {
		got, ok := legacyTestCmd(c.repo, c.version)
		if got != c.want || ok != c.ok {
			t.Errorf("%s %s: got %q %v, want %q %v", c.repo, c.version, got, ok, c.want, c.ok)
		}
	}
}

func TestTestDirectives(t *testing.T) {
	files := []string{"tests/forms_tests/tests/test_input.py", "tests/data/x.json", "docs/notes.md"}
	if got := testDirectives("django/django", files); !reflect.DeepEqual(got, []string{"forms_tests.tests.test_input"}) {
		t.Errorf("django: %v", got)
	}
	if got := testDirectives("pydata/xarray", []string{"xarray/tests/test_a.py", "x.png"}); !reflect.DeepEqual(got, []string{"xarray/tests/test_a.py"}) {
		t.Errorf("pytest: %v", got)
	}
}

func TestParserTables(t *testing.T) {
	impl := map[string]bool{parserPytest: true, parserPytestOptions: true, parserPytestV2: true, parserDjango: true, parserSympy: true, parserSeaborn: true, parserMatplotlib: true}
	for name, to := range parserAliases {
		if !impl[to] {
			t.Errorf("alias %s maps to %s, which grade.py does not implement", name, to)
		}
	}
	for repo, p := range repoParsers {
		if !impl[p] {
			t.Errorf("repo %s uses %s, which grade.py does not implement", repo, p)
		}
	}
}
