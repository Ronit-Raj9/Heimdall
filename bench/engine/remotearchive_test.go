package engine

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// archiveOf builds a tar.gz from entries in order.
func archiveOf(t *testing.T, entries ...*tar.Header) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, h := range entries {
		if h.Typeflag == tar.TypeReg && h.Size == 0 {
			h.Size = int64(len(h.Name))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(h.Name)[:h.Size]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func TestPackExtractRoundTrip(t *testing.T) {
	src := t.TempDir()
	files := map[string]string{"run.json": "{}", "state/db/dump.sql": "insert", "agent.stream.1.jsonl": "{\"a\":1}\n"}
	for name, content := range files {
		p := filepath.Join(src, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o444); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	if err := packRun(src, &buf); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "x")
	got, err := extractRun(&buf, dst, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(files) {
		t.Fatalf("extracted %v", got)
	}
	for name, content := range files {
		if got[name] != sha256Hex([]byte(content)) {
			t.Fatalf("%s hash %s", name, got[name])
		}
		b, err := os.ReadFile(filepath.Join(dst, filepath.FromSlash(name)))
		if err != nil || string(b) != content {
			t.Fatalf("%s: %q %v", name, b, err)
		}
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	if err := packRun(src, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("pack with a symlink: %v", err)
	}
}

func TestExtractRefuses(t *testing.T) {
	reg := func(name string) *tar.Header { return &tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: 0o644} }
	cases := []struct {
		name    string
		entries []*tar.Header
		want    string
	}{
		{"parent segment", []*tar.Header{reg("../escape")}, "segment"},
		{"nested parent", []*tar.Header{reg("a/../../escape")}, "segment"},
		{"absolute", []*tar.Header{reg("/etc/x")}, "relative"},
		{"backslash", []*tar.Header{reg(`a\b`)}, "relative"},
		{"symlink", []*tar.Header{{Typeflag: tar.TypeSymlink, Name: "s", Linkname: "/etc/passwd"}}, "symlinks"},
		{"hard link", []*tar.Header{{Typeflag: tar.TypeLink, Name: "h", Linkname: "run.json"}}, "symlinks"},
		{"device", []*tar.Header{{Typeflag: tar.TypeChar, Name: "dev"}}, "symlinks"},
		{"duplicate", []*tar.Header{reg("a"), reg("a")}, "twice"},
		{"too large", []*tar.Header{reg("big-file-name-longer-than-cap")}, "larger than"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dst := filepath.Join(t.TempDir(), "x")
			_, err := extractRun(archiveOf(t, tc.entries...), dst, 16)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(dst), "escape")); err == nil {
				t.Fatal("a file escaped the extraction folder")
			}
		})
	}
	if _, err := extractRun(strings.NewReader("not gzip"), filepath.Join(t.TempDir(), "x"), 16); err == nil {
		t.Fatal("non-gzip stream extracted")
	}
}

func TestCompareSealFiles(t *testing.T) {
	want := map[string]string{"a": "1", "b": "2"}
	cases := []struct {
		got  map[string]string
		want string
	}{
		{map[string]string{"a": "1", "b": "2"}, ""},
		{map[string]string{"a": "1"}, "file b removed"},
		{map[string]string{"a": "1", "b": "2", "c": "3"}, "file c added"},
		{map[string]string{"a": "9", "b": "2"}, "file a changed"},
	}
	for _, tc := range cases {
		err := compareSealFiles(want, tc.got)
		if tc.want == "" {
			if err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) || !errors.Is(err, schemas.ErrSealMismatch) {
			t.Fatalf("got %v, want %q", err, tc.want)
		}
	}
}

func TestVerifyUploadIdentity(t *testing.T) {
	dir := t.TempDir()
	slot := &schemas.Slot{SlotID: "s_1", PairID: "p_1", ExpID: "e_1"}
	write := func(name, content string) string {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return sha256Hex([]byte(content))
	}
	runJSON := `{"run_id":"r_1","slot_id":"s_1","pair_id":"p_1","exp_id":"e_1","attempt":1,"stage":"seal","manifest_hash":"m",` +
		`"suite_lock_hash":"l","outcome":"pass","timing":{"started":"2026-11-15T00:00:00Z","sealed":"2026-11-15T00:01:00Z"}}`
	files := map[string]string{runJSONFile: write(runJSONFile, runJSON), transitionsFile: write(transitionsFile, "")}
	seal := `{"run_id":"r_1","manifest_hash":"m","files":{"run.json":"` + files[runJSONFile] + `","transitions.jsonl":"` + files[transitionsFile] + `"}}`
	sealSum := write(sealJSONFile, seal)
	want := uploadWant{expID: "e_1", slot: slot, attempt: 1, manifestHash: "m", suiteLockHash: "l", runID: "r_1", sealSHA256: sealSum}
	if _, err := verifyUpload(dir, files, want); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(w *uploadWant){
		"other run":     func(w *uploadWant) { w.runID = "r_2" },
		"other attempt": func(w *uploadWant) { w.attempt = 2 },
		"other slot":    func(w *uploadWant) { w.slot = &schemas.Slot{SlotID: "s_2", PairID: "p_1"} },
		"other seal":    func(w *uploadWant) { w.sealSHA256 = strings.Repeat("0", 64) },
		"other lock":    func(w *uploadWant) { w.suiteLockHash = "x" },
	} {
		w := want
		mutate(&w)
		if _, err := verifyUpload(dir, files, w); err == nil {
			t.Fatalf("%s: verified", name)
		}
	}
	reserved := map[string]string{runJSONFile: files[runJSONFile], transitionsFile: files[transitionsFile], "remote/worker.json": "x"}
	if _, err := verifyUpload(dir, reserved, want); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("reserved folder: %v", err)
	}
}
