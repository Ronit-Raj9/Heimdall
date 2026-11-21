package sharedpool

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestProjectRoundTrip(t *testing.T) {
	src := t.TempDir()
	write := func(rel, body string, mode os.FileMode) {
		p := filepath.Join(src, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("benchmarks/s/suite.yaml", "id: s\n", 0o600)
	write("benchmarks/s/run.sh", "#!/bin/sh\n", 0o700)
	write(".heimdall/webhooks/hooks.json", "{}", 0o600)
	write(".heimdall/agent-versions/a/v1.yaml", "v1", 0o644)
	write(".git/config", "x", 0o644)
	write("releases/log.jsonl", "x", 0o644)
	if err := os.Symlink("/etc/passwd", filepath.Join(src, "benchmarks", "link")); err != nil {
		t.Fatal(err)
	}
	tag1, err := ProjectETag(src)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := WriteProject(&buf, src); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	if err := ExtractProject(bytes.NewReader(buf.Bytes()), dst); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(dst, "benchmarks/s/suite.yaml")); err != nil || fi.Mode().Perm()&0o444 != 0o444 {
		t.Fatalf("suite file: %v %v", fi, err)
	}
	if fi, err := os.Stat(filepath.Join(dst, "benchmarks/s/run.sh")); err != nil || fi.Mode().Perm()&0o111 == 0 {
		t.Fatalf("executable lost: %v %v", fi, err)
	}
	if _, err := os.Stat(filepath.Join(dst, ".heimdall/agent-versions/a/v1.yaml")); err != nil {
		t.Fatal("agent version snapshot not transferred")
	}
	for _, gone := range []string{".heimdall/webhooks", ".git", "releases", "benchmarks/link"} {
		if _, err := os.Lstat(filepath.Join(dst, gone)); err == nil {
			t.Errorf("%s transferred", gone)
		}
	}
	write("benchmarks/s/suite.yaml", "id: s2\n", 0o600)
	if tag2, _ := ProjectETag(src); tag2 == tag1 {
		t.Fatal("etag did not change")
	}
}

func TestExtractProjectRefusesUnsafe(t *testing.T) {
	for _, h := range []tar.Header{
		{Name: "../escape", Typeflag: tar.TypeReg, Size: 1, Mode: 0o644},
		{Name: "/abs", Typeflag: tar.TypeReg, Size: 1, Mode: 0o644},
		{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc", Mode: 0o777},
		{Name: ".heimdall/webhooks/hooks.json", Typeflag: tar.TypeReg, Size: 1, Mode: 0o644},
	} {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		hdr := h
		if err := tw.WriteHeader(&hdr); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			_, _ = tw.Write([]byte("x"))
		}
		_ = tw.Close()
		_ = gz.Close()
		if err := ExtractProject(&buf, t.TempDir()); err == nil {
			t.Errorf("%s accepted", h.Name)
		}
	}
}
