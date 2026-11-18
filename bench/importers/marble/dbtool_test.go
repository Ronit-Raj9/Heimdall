package marble

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestDBToolSplitSQL checks the database tool's statement splitter (it runs
// upstream's init_sql statement by statement): semicolons inside quotes,
// dollar quotes and comments do not split.
func TestDBToolSplitSQL(t *testing.T) {
	needPython(t)
	dir := t.TempDir()
	tool := filepath.Join(dir, "dbtool.py")
	if err := os.WriteFile(tool, []byte(dbToolScript), 0o644); err != nil {
		t.Fatal(err)
	}
	sql := "-- 1. Users table; with a comment\nCREATE TABLE users (id SERIAL PRIMARY KEY, name VARCHAR(50) DEFAULT 'a;b');\n" +
		"/* block; comment */ INSERT INTO users (name) VALUES ('it''s; fine');\n" +
		"CREATE FUNCTION f() RETURNS int AS $$ SELECT 1; $$ LANGUAGE sql;\n" +
		"SELECT \"odd;name\" FROM users;   \n;\n"
	prog := `import importlib.util, json, sys
spec = importlib.util.spec_from_file_location("dbtool", sys.argv[1])
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)
print(json.dumps(m.split_sql(sys.stdin.read())))`
	cmd := exec.Command("python3", "-B", "-I", "-c", prog, tool)
	cmd.Stdin = strings.NewReader(sql)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	want := []string{
		"CREATE TABLE users (id SERIAL PRIMARY KEY, name VARCHAR(50) DEFAULT 'a;b')",
		"INSERT INTO users (name) VALUES ('it''s; fine')",
		"CREATE FUNCTION f() RETURNS int AS $$ SELECT 1; $$ LANGUAGE sql",
		"SELECT \"odd;name\" FROM users",
	}
	if !slices.Equal(got, want) {
		t.Errorf("split_sql =\n%q\nwant\n%q", got, want)
	}
}
