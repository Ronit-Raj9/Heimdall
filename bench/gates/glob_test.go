package gates

import "testing"

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"tests/**", "tests/test_a.py", true},
		{"tests/**", "tests/sub/deep/test_b.py", true},
		{"tests/**", "./tests/test_a.py", true},
		{"tests/**", "src/tests/test_a.py", false},
		{"tests/**", "testsuite/a.py", false},
		{"**/conftest.py", "conftest.py", true},
		{"**/conftest.py", "a/b/conftest.py", true},
		{"**/conftest.py", "a/b/conftest.pyc", false},
		{"src/**/fixtures/*.json", "src/fixtures/x.json", true},
		{"src/**/fixtures/*.json", "src/a/b/fixtures/x.json", true},
		{"src/**/fixtures/*.json", "src/a/b/fixtures/sub/x.json", false},
		{"*.md", "README.md", true},
		{"*.md", "docs/README.md", false},
		{"tests/test_?.py", "tests/test_a.py", true},
		{"", "anything", false},
	}
	for _, c := range cases {
		t.Run(c.pattern+"|"+c.name, func(t *testing.T) {
			got, err := matchGlob(c.pattern, c.name)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Fatalf("matchGlob(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
			}
		})
	}
}

func TestMatchGlobBadPattern(t *testing.T) {
	if _, err := matchGlob("tests/[", "tests/a"); err == nil {
		t.Fatal("want error for malformed pattern")
	}
}

func TestUnderPath(t *testing.T) {
	cases := []struct {
		root, name string
		want       bool
	}{
		{"tests", "tests/test_a.py", true},
		{"tests", "tests", true},
		{"tests/", "tests/sub/a.py", true},
		{"tests", "tests2/a.py", false},
		{"conftest.py", "conftest.py", true},
		{"conftest.py", "pkg/conftest.py", false},
		{"pkg/*", "pkg/sub/a.py", true},
		{"", "a", false},
	}
	for _, c := range cases {
		t.Run(c.root+"|"+c.name, func(t *testing.T) {
			got, err := underPath(c.root, c.name)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Fatalf("underPath(%q, %q) = %v, want %v", c.root, c.name, got, c.want)
			}
		})
	}
}

func TestNormPath(t *testing.T) {
	cases := map[string]string{
		"./a/b":  "a/b",
		"/a/b/":  "a/b",
		"a//b":   "a/b",
		"":       "",
		".":      "",
		`a\b.py`: "a/b.py",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			if got := normPath(in); got != want {
				t.Fatalf("normPath(%q) = %q, want %q", in, got, want)
			}
		})
	}
}
