package researchkit

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWebsearchCopyIsCurrent fails when the embedded copy drifts from
// bench/research/websearch, the research track's search tool.
func TestWebsearchCopyIsCurrent(t *testing.T) {
	files, tag, err := WebsearchFiles()
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range files {
		want, err := os.ReadFile(filepath.Join("..", "..", "research", "websearch", name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("websearch/%s differs from research/websearch/%s: copy it again", name, name)
		}
	}
	if !strings.HasPrefix(tag, "bench/import-websearch:") || len(tag) != len("bench/import-websearch:")+12 {
		t.Errorf("tag %q", tag)
	}
}

func TestContentTagChangesWithContent(t *testing.T) {
	a := ContentTag(map[string][]byte{"x": []byte("1")})
	b := ContentTag(map[string][]byte{"x": []byte("2")})
	c := ContentTag(map[string][]byte{"y": []byte("1")})
	if a == b || a == c || a != ContentTag(map[string][]byte{"x": []byte("1")}) {
		t.Errorf("tags %s %s %s", a, b, c)
	}
}

func TestWriteTreeRefusesEscape(t *testing.T) {
	dir := t.TempDir()
	if err := WriteTree(dir, map[string][]byte{"../x": []byte("no")}); err == nil {
		t.Fatal("wrote outside the folder")
	}
	if err := WriteTree(dir, map[string][]byte{"a/b.sh": []byte("#!/bin/sh\n"), "c.txt": []byte("c")}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "a", "b.sh"))
	if err != nil || fi.Mode().Perm()&0o100 == 0 {
		t.Fatalf("script not executable: %v %v", fi, err)
	}
}

func TestPrepareTaskDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "task")
	if err := PrepareTaskDir(dir, false); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := PrepareTaskDir(dir, false); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("want ErrExist, got %v", err)
	}
	if err := PrepareTaskDir(dir, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("force did not remove the folder")
	}
}

func TestTaskID(t *testing.T) {
	for in, want := range map[[2]string]string{
		{"", "C61D22DE-5F6E-4958-A7F6-5E9707BD3466"}: "c61d22de-5f6e-4958-a7f6-5e9707bd3466",
		{"bcp", "769"}:  "bcp-769",
		{"", "__a b__"}: "a-b",
		{"x", "Q/1.2"}:  "x-q-1.2",
	} {
		if got := TaskID(in[0], in[1]); got != want {
			t.Errorf("TaskID(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}
