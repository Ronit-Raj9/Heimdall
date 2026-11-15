package mcpmark

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScanAndCopyState(t *testing.T) {
	src := t.TempDir()
	old := time.Date(2025, 8, 5, 8, 1, 0, 0, time.UTC)
	files := map[string]string{"a.txt": "alpha", "dir/b.txt": "beta", "__MACOSX/._a.txt": "junk"}
	for name, body := range files {
		p := filepath.Join(src, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(src, "link")); err != nil {
		t.Skip("no symlinks here")
	}
	st, err := scanState(src)
	if err != nil {
		t.Fatal(err)
	}
	if st.bytes != int64(len("alpha")+len("beta")) || len(st.skipped) != 1 || st.skipped[0] != "link" || len(st.hash) != 64 {
		t.Fatalf("state = %+v", st)
	}
	dst := filepath.Join(t.TempDir(), "state")
	if err := copyState(src, dst); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		path   string
		exists bool
	}{
		{name: "file kept", path: "a.txt", exists: true},
		{name: "nested kept", path: "dir/b.txt", exists: true},
		{name: "macos metadata dropped", path: "__MACOSX/._a.txt"},
		{name: "symlink dropped", path: "link"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := os.Lstat(filepath.Join(dst, filepath.FromSlash(tt.path)))
			if (err == nil) != tt.exists {
				t.Fatalf("exists = %v, want %v", err == nil, tt.exists)
			}
			if tt.exists && (!info.ModTime().Equal(old) || info.Mode().Perm() != 0o644) {
				t.Fatalf("time %v mode %v", info.ModTime(), info.Mode())
			}
		})
	}
	again, err := scanState(src)
	if err != nil || again.hash != st.hash {
		t.Fatal("state hash is not stable")
	}
}

func TestRenderHelpers(t *testing.T) {
	if got := exportCap(0); got != minExportBytes {
		t.Errorf("exportCap(0) = %d", got)
	}
	if got := exportCap(100 << 20); got != 216<<20 {
		t.Errorf("exportCap(100 MiB) = %d", got)
	}
	tests := []struct {
		task upTask
		want string
	}{
		{task: upTask{Meta: upMeta{Difficulty: "L1"}}, want: "easy"},
		{task: upTask{Meta: upMeta{Difficulty: "L2"}}, want: "medium"},
		{task: upTask{Meta: upMeta{Difficulty: "L3"}}, want: "hard"},
		{task: upTask{Difficulty: "easy"}, want: "easy"},
		{task: upTask{Difficulty: "standard"}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.want+tt.task.Meta.Difficulty, func(t *testing.T) {
			if got := difficulty(tt.task); got != tt.want {
				t.Fatalf("difficulty = %q, want %q", got, tt.want)
			}
		})
	}
	ins := instruction("  Please use FileSystem tools.\n\n")
	if !strings.HasPrefix(ins, "The files of this task live in the MCP server `fs`") || !strings.HasSuffix(ins, "Please use FileSystem tools.\n") {
		t.Errorf("instruction = %q", ins)
	}
	if q(`a "b"`) != `"a \"b\""` {
		t.Errorf("q = %s", q(`a "b"`))
	}
}

func TestReferenceFallback(t *testing.T) {
	w := &writer{set: settings{references: t.TempDir()}}
	ref, own, err := w.reference(candidate{id: "fs-x", task: upTask{Service: "filesystem", Difficulty: "easy", Category: "c", Name: "x"}})
	if err != nil || own || !strings.Contains(ref, "exit 1") || !strings.Contains(ref, "filesystem/easy/c/x") {
		t.Fatalf("reference = %q own %v err %v", ref, own, err)
	}
}
