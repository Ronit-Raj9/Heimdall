package gates

import (
	"reflect"
	"testing"
)

func TestReadPyLine(t *testing.T) {
	cases := []struct {
		name     string
		line     string
		norm     string
		code     string
		indent   int
		header   bool
		complete bool
	}{
		{"spaces collapse outside strings", `    assert  f( "a  b" )  ==  1`, `assert f( "a  b" ) == 1`, `assert f( "a  b" ) == 1`, 4, false, true},
		{"hash inside a string is not a comment", `    x = "#not"  # note`, `x = "#not" # note`, `x = "#not"`, 4, false, false},
		{"tab indent", "\tif x:", "if x:", "if x:", 8, true, false},
		{"header with a comment", "def test_a():  # why", "def test_a(): # why", "def test_a():", 0, true, false},
		{"escaped quote in a raw string", `assert m(r"\"(") == 1`, `assert m(r"\"(") == 1`, `assert m(r"\"(") == 1`, 0, false, true},
		{"open bracket is not complete", "    assert f(", "assert f(", "assert f(", 4, false, false},
		{"close before open is not complete", "assert a) == (b", "assert a) == (b", "assert a) == (b", 0, false, false},
		{"open string is not complete", `assert s == """abc`, `assert s == """abc`, `assert s == """abc`, 0, false, false},
		{"continuation is not complete", `assert x == \`, `assert x == \`, `assert x == \`, 0, false, false},
		{"blank", "   ", "", "", 3, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pl := readPyLine(c.line)
			if pl.norm != c.norm || pl.code != c.code || pl.indent != c.indent || pl.isHeader() != c.header || pl.isCompleteAssert() != c.complete {
				t.Fatalf("got norm=%q code=%q indent=%d header=%v complete=%v", pl.norm, pl.code, pl.indent, pl.isHeader(), pl.isCompleteAssert())
			}
		})
	}
}

func TestIsWrapper(t *testing.T) {
	cases := []struct {
		code string
		want bool
	}{
		{`with pytest.warns(DeprecationWarning):`, true},
		{`with pytest.warns(DeprecationWarning, match=r"^f\(\) is deprecated$"):`, true},
		{`with pytest.warns(DeprecationWarning) as rec:`, true},
		{`with pytest.deprecated_call():`, true},
		{`with warnings.catch_warnings(record=True) as caught:`, true},
		{`with pytest.raises(ValueError):`, false},
		{`with contextlib.suppress(Exception):`, false},
		{`with pytest.warns(DeprecationWarning), contextlib.suppress(AssertionError):`, false},
		{`with pytest.warns(DeprecationWarning): assert f() == 1`, false},
		{`with pytest.warns(DeprecationWarning`, false},
		{`if False:`, false},
	}
	for _, c := range cases {
		t.Run(c.code, func(t *testing.T) {
			if got := isWrapper(c.code); got != c.want {
				t.Fatalf("isWrapper(%q) = %v, want %v", c.code, got, c.want)
			}
		})
	}
}

func TestAssertKept(t *testing.T) {
	const r = "    assert f(x) == [1, 2]"
	cases := []struct {
		added string
		want  bool
	}{
		{"assert f(x) == [1, 2]", true},
		{`assert f(x) == [1, 2], "f of x"`, true},
		{"with pytest.warns(DeprecationWarning): assert f(x) == [1, 2]", true},
		{"assert f(x) == [1, 2] or True", false},
		{"assert f(x) == [1, 2] + [3]", false},
		{"if False: assert f(x) == [1, 2]", false},
		{"with pytest.raises(AssertionError): assert f(x) == [1, 2]", false},
		{"assert not f(x) == [1, 2]", false},
	}
	for _, c := range cases {
		t.Run(c.added, func(t *testing.T) {
			if got := assertKept(readPyLine(r), readPyLine(c.added)); got != c.want {
				t.Fatalf("assertKept(%q) = %v, want %v", c.added, got, c.want)
			}
		})
	}
	if assertKept(readPyLine("    assert f(x) == [1,"), readPyLine("assert f(x) == [1, 2]")) {
		t.Fatal("an incomplete assert must never be kept by containment")
	}
}

func TestParseImport(t *testing.T) {
	cases := []struct {
		line    string
		module  string
		entries []string
		ok      bool
	}{
		{"from gatekeeper import drop_duplicates, find_duplicates", "from gatekeeper", []string{"drop_duplicates", "find_duplicates"}, true},
		{"from m import (a, b as c,)", "from m", []string{"a", "b as c"}, true},
		{"import os, sys", "import", []string{"os", "sys"}, true},
		{"from m import (", "", nil, false},
		{"import os; import sys", "", nil, false},
		{"x = 1", "", nil, false},
	}
	for _, c := range cases {
		t.Run(c.line, func(t *testing.T) {
			imp, ok := parseImport(readPyLine(c.line))
			if ok != c.ok || imp.module != c.module || !reflect.DeepEqual(imp.entries, c.entries) {
				t.Fatalf("got %+v ok=%v", imp, ok)
			}
		})
	}
}

func TestImportCovers(t *testing.T) {
	old, _ := parseImport(readPyLine("from m import a, b"))
	cases := []struct {
		line string
		want bool
	}{
		{"from m import a, b, c", true},
		{"from m import c, b, a", true},
		{"from m import a", false},
		{"from n import a, b", false},
		{"from m import a, b, c as a", false},
		{"from m import a as x, b", false},
	}
	for _, c := range cases {
		t.Run(c.line, func(t *testing.T) {
			imp, _ := parseImport(readPyLine(c.line))
			if got := imp.covers(old); got != c.want {
				t.Fatalf("covers = %v, want %v", got, c.want)
			}
		})
	}
}
