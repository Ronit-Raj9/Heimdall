package utils

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMissingTools(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want []string
	}{
		{"all present", "", nil},
		{"python3 missing", "missing python3\n", []string{"python3"}},
		{"two missing, noise ignored", "hello\nmissing git\nmissing python3\nmissing\n", []string{"git", "python3"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MissingTools(tt.out); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHelperLacks(t *testing.T) {
	tests := []struct {
		tools []string
		want  string
	}{
		{[]string{"python3"}, "helper image acme/node:1 has no python3; set images.helper to the bench helper image"},
		{[]string{"git", "python3"}, "helper image acme/node:1 has no git or python3; set images.helper to the bench helper image"},
	}
	for _, tt := range tests {
		if got := HelperLacks("acme/node:1", tt.tools...); got != tt.want {
			t.Errorf("got %q, want %q", got, tt.want)
		}
	}
}

func TestLabelArgs(t *testing.T) {
	got := LabelArgs("e_x", "r_y")
	want := []string{"--label", "bifrost-bench=1", "--label", "bifrost-bench.exp=e_x", "--label", "bifrost-bench.run=r_y"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// fakePipeDocker is a docker stand-in: "src" prints a payload, "dst" reads
// stdin and prints it reversed by line count, "fail" exits 1 with a message.
func fakePipeDocker(t *testing.T) Docker {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "docker")
	script := `#!/bin/sh
case "$1" in
  src) printf 'a\nb\nc\n' ;;
  dst) echo "got $(wc -l)" ;;
  fail) cat > /dev/null; echo "boom" >&2; exit 1 ;;
  srcfail) echo "no such file" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return Docker{Binary: bin}
}

func TestPipe(t *testing.T) {
	d := fakePipeDocker(t)
	out, err := d.Pipe(t.Context(), []string{"src"}, []string{"dst"})
	if err != nil || strings.TrimSpace(string(out)) != "got 3" {
		t.Fatalf("pipe: %q %v", out, err)
	}
	if _, err := d.Pipe(t.Context(), []string{"src"}, []string{"fail"}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("failing reader: %v", err)
	}
	if _, err := d.Pipe(t.Context(), []string{"srcfail"}, []string{"dst"}); err == nil || !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("failing writer: %v", err)
	}
}
