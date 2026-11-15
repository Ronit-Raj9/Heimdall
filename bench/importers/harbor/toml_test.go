package harbor

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseTOML(t *testing.T) {
	src := `schema_version = "1.1" # trailing comment
artifacts = []

[task]
name = "terminal-bench/x"
keywords = ["a", 'b',
  "c", # comment inside an array
]
[[task.authors]]
name = "One"
[[task.authors]]
name = "Two"

[metadata]
difficulty = "hard"
expert = 45.0
count = 1_000
hex = 0x10
"quoted key" = true
dotted.key = "v"
inline = { a = 1, b = "two" }
multi = """
line one
line two"""
literal = 'C:\path'
escaped = "tab\there \u00e9"

[verifier.env]
`
	got, err := parseTOML(src)
	if err != nil {
		t.Fatalf("parseTOML: %v", err)
	}
	task := tomlTable(got, "task")
	meta := tomlTable(got, "metadata")
	checks := []struct {
		name string
		got  any
		want any
	}{
		{"string", got["schema_version"], "1.1"},
		{"empty array", got["artifacts"], []any{}},
		{"array across lines", task["keywords"], []any{"a", "b", "c"}},
		{"array of tables", len(task["authors"].([]any)), 2},
		{"second author", task["authors"].([]any)[1].(map[string]any)["name"], "Two"},
		{"float", meta["expert"], 45.0},
		{"underscored int", meta["count"], int64(1000)},
		{"hex int", meta["hex"], int64(16)},
		{"quoted key", meta["quoted key"], true},
		{"dotted key", meta["dotted"].(map[string]any)["key"], "v"},
		{"inline table", meta["inline"], map[string]any{"a": int64(1), "b": "two"}},
		{"multi-line string", meta["multi"], "line one\nline two"},
		{"literal string", meta["literal"], `C:\path`},
		{"escapes", meta["escaped"], "tab\there é"},
		{"empty sub-table", tomlTable(tomlTable(got, "verifier"), "env"), map[string]any{}},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if !reflect.DeepEqual(c.got, c.want) {
				t.Errorf("got %#v, want %#v", c.got, c.want)
			}
		})
	}
}

func TestParseTOMLErrors(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"duplicate key", "a = 1\na = 2\n", "line 2: key a defined twice"},
		{"unterminated string", "a = \"x\n", "line 1: newline in string"},
		{"date", "a = 1979-05-27\n", "line 1: unsupported value"},
		{"junk after value", "a = 1 2\n", "line 1: unexpected"},
		{"missing equals", "a 1\n", "want = after key a"},
		{"table over value", "a = 1\n[a]\n", "key a is not a table"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseTOML(c.src)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want error containing %q", err, c.want)
			}
		})
	}
}
