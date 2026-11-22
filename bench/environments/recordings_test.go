package environments

import (
	"archive/tar"
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

func recSpec(files ...schemas.RecordingFile) schemas.ServiceSpec {
	return schemas.ServiceSpec{ID: "browser", Image: "x", Recordings: &schemas.RecordingsSpec{Files: files}}
}

func TestCheckRecordings(t *testing.T) {
	ok := schemas.RecordingFile{ID: "video", Path: "/tmp/rec/video", Kind: schemas.RecordingVideo}
	if err := checkService(recSpec(ok)); err != nil {
		t.Fatalf("valid recordings refused: %v", err)
	}
	bad := map[string]schemas.ServiceSpec{
		"no files":      recSpec(),
		"bad id":        recSpec(schemas.RecordingFile{ID: "../x", Path: "/a", Kind: "video"}),
		"repeated id":   recSpec(ok, ok),
		"bad kind":      recSpec(schemas.RecordingFile{ID: "a", Path: "/a", Kind: "screenshots"}),
		"relative":      recSpec(schemas.RecordingFile{ID: "a", Path: "tmp/a", Kind: "video"}),
		"unclean":       recSpec(schemas.RecordingFile{ID: "a", Path: "/tmp/../etc", Kind: "video"}),
		"root":          recSpec(schemas.RecordingFile{ID: "a", Path: "/", Kind: "video"}),
		"cap too high":  recSpec(schemas.RecordingFile{ID: "a", Path: "/a", Kind: "video", MaxBytes: schemas.MaxRunRecordingBytes + 1}),
		"live no frame": {ID: "desktop", Image: "x", Live: &schemas.LiveSpec{Input: []string{"x"}}},
	}
	wait := recSpec(ok)
	wait.Recordings.WaitS = schemas.MaxRecordingWaitS + 1
	bad["wait too long"] = wait
	for name, s := range bad {
		if err := checkService(s); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// writeTar writes a docker cp style tar of the given files (name: data).
func writeTar(t *testing.T, path string, files map[string]string, order []string) {
	t.Helper()
	fh, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(fh)
	for _, n := range order {
		data := files[n]
		if strings.HasSuffix(n, "/") {
			if err := tw.WriteHeader(&tar.Header{Name: n, Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := tw.WriteHeader(&tar.Header{Name: n, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.WriteHeader(&tar.Header{Name: "video/link.webm", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	fh.Close()
}

func TestUnpackRecordingVideoFolder(t *testing.T) {
	dir := t.TempDir()
	spool := filepath.Join(dir, "spool.tar")
	files := map[string]string{
		"video/":                  "",
		"video/page-1.webm":       "WEBM-ONE",
		"video/page-1.webm.start": "1795322522419\n",
		"video/page-2.webm":       "WEBM-TWO",
		"video/notes.txt":         "skipped: not a video",
	}
	writeTar(t, spool, files, []string{"video/", "video/page-2.webm", "video/page-1.webm", "video/page-1.webm.start", "video/notes.txt"})
	var total int64
	out := filepath.Join(dir, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	recs, err := unpackRecording(spool, out, "browser", schemas.RecordingFile{ID: "video", Path: "/tmp/rec/video", Kind: schemas.RecordingVideo}, 1<<20, &total)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || recs[0].File != "video-page-1.webm" || recs[1].File != "video-page-2.webm" {
		t.Fatalf("records %+v", recs)
	}
	if want := time.UnixMilli(1795322522419).UTC(); !recs[0].StartedAt.Equal(want) || !recs[1].StartedAt.IsZero() {
		t.Errorf("start times %v %v", recs[0].StartedAt, recs[1].StartedAt)
	}
	if recs[0].Mime != "video/webm" || recs[0].Bytes != 8 || recs[0].SHA256 == "" || total != 16 {
		t.Errorf("record %+v total %d", recs[0], total)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "video-page-1.webm")); string(b) != "WEBM-ONE" {
		t.Errorf("file content %q", b)
	}
	if _, err := os.Lstat(filepath.Join(out, "video-link.webm")); err == nil {
		t.Error("a symlink in the tar was written")
	}
}

func TestUnpackRecordingRedactsAndCaps(t *testing.T) {
	dir := t.TempDir()
	spool := filepath.Join(dir, "spool.tar")
	har := `{"log":{"entries":[{"request":{"headers":[{"name":"Authorization","value":"Bearer sk-ant-api03-` + strings.Repeat("a", 90) + `"}]}}]}}`
	writeTar(t, spool, map[string]string{"session.har": har}, []string{"session.har"})
	var total int64
	recs, err := unpackRecording(spool, dir, "browser", schemas.RecordingFile{ID: "har", Path: "/tmp/rec/session.har", Kind: schemas.RecordingHAR}, 1<<20, &total)
	if err != nil || len(recs) != 1 {
		t.Fatalf("%v %+v", err, recs)
	}
	b, _ := os.ReadFile(filepath.Join(dir, recs[0].File))
	if strings.Contains(string(b), "sk-ant-api03-aaaa") || !strings.Contains(string(b), `"value":"[redacted]"`) {
		t.Fatalf("HAR kept a key: %s", b)
	}
	for in, leak := range map[string]string{
		`{"name":"Cookie","value":"session=abc123"}`:    "abc123",
		`{"name": "set-cookie", "value": "sid=x\"y"}`:   "sid=",
		`console: token=ghp_` + strings.Repeat("b", 30): "bbbbbbbb",
		`{"text":"key AKIAABCDEFGHIJKLMNOP"}`:           "ABCDEFGHIJKLMNOP",
	} {
		if out := redactRecording(in); strings.Contains(out, leak) {
			t.Errorf("redactRecording(%s) = %s", in, out)
		}
	}

	// Over the recording's own cap: noted, nothing written.
	recs, err = unpackRecording(spool, dir, "browser", schemas.RecordingFile{ID: "har2", Path: "/x.har", Kind: schemas.RecordingHAR}, 10, &total)
	if err != nil || len(recs) != 1 || recs[0].Error == "" || recs[0].File != "" {
		t.Fatalf("over cap: %v %+v", err, recs)
	}
	// Over the run's cap.
	full := schemas.MaxRunRecordingBytes
	recs, err = unpackRecording(spool, dir, "browser", schemas.RecordingFile{ID: "har3", Path: "/x.har", Kind: schemas.RecordingHAR}, 1<<20, &full)
	if err != nil || len(recs) != 1 || recs[0].Error == "" {
		t.Fatalf("over the run cap: %v %+v", err, recs)
	}
}

func TestRecordingNamesAndStart(t *testing.T) {
	if got := recordingFileName("video", "page@4e30c9e3.webm"); got != "video-page_4e30c9e3.webm" {
		t.Errorf("name %q", got)
	}
	if got := recordingFileName("v", "../../.."); got != "v-file" {
		t.Errorf("dots %q", got)
	}
	if got := recordingFileName("v", strings.Repeat("x", 300)+".webm"); len(got) != 120 || !strings.HasSuffix(got, ".webm") {
		t.Errorf("long name %d %q", len(got), got)
	}
	for in, ok := range map[string]bool{"1795322522419": true, "2026-11-22T04:21:15.5Z": true, "": false, "soon": false, "-5": false} {
		if _, got := parseStart(in); got != ok {
			t.Errorf("parseStart(%q) = %v", in, got)
		}
	}
	if RecordingMime(schemas.RecordingVideo, "a.MP4") != "video/mp4" || RecordingMime(schemas.RecordingVideo, "a.har") != "" {
		t.Error("mime by kind")
	}
}

func TestRecordingIndexOutsideState(t *testing.T) {
	// The verifier mounts state/ only (graders/cleanroom.go); recordings must
	// never land there.
	if strings.HasPrefix(schemas.RecordingsDir, StateFolder) || schemas.RecordingsDir == StateFolder {
		t.Fatalf("recordings folder %q is under the verifier's state folder", schemas.RecordingsDir)
	}
	dir := t.TempDir()
	recs := []schemas.RecordingRecord{{Service: "desktop", ID: "screen", Kind: "video", File: "screen-desktop.mp4"}, {Service: "desktop", ID: "x", Kind: "log", Error: "not written"}}
	if err := writeRecordingIndex(dir, recs); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(dir, schemas.RecordingsDir, schemas.RecordingsIndexFile))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		var r schemas.RecordingRecord
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		n++
	}
	if n != 2 {
		t.Fatalf("lines %d", n)
	}
}

func TestImageMime(t *testing.T) {
	if ImageMime([]byte("\x89PNG\r\n\x1a\nx")) != "image/png" || ImageMime([]byte{0xFF, 0xD8, 0xFF, 1}) != "image/jpeg" || ImageMime([]byte("GIF8")) != "" {
		t.Error("magic")
	}
}

// TestCopyRecordingMissingIsPlainNote: an image that cannot record (no
// launcher, an old Playwright MCP, no ffmpeg) writes no file; the record
// carries a plain note naming the fix, not docker's raw error, and the run
// goes on (H10).
func TestCopyRecordingMissingIsPlainNote(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "docker")
	script := "#!/bin/sh\necho \"Error response from daemon: Could not find the file /tmp/rec/video in container bench-r_1-svc-browser\" >&2\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: utils.Docker{Binary: bin}, Logger: schemas.NoopLogger{}})
	var total int64
	for _, f := range []schemas.RecordingFile{
		{ID: "video", Path: "/tmp/rec/video", Kind: schemas.RecordingVideo},
		{ID: "har", Path: "/tmp/rec/har", Kind: schemas.RecordingHAR},
	} {
		recs := e.copyRecording(context.Background(), "bench-r_1-svc-browser", dir, "browser", f, &total)
		if len(recs) != 1 || recs[0].Error == "" {
			t.Fatalf("%s: records %+v", f.ID, recs)
		}
		msg := recs[0].Error
		if strings.Contains(msg, "Error response") || !strings.Contains(msg, "docs/recordings.md") || !strings.Contains(msg, "record.js") {
			t.Errorf("%s note %q", f.ID, msg)
		}
	}
	if !strings.Contains(recordingMissingNote(schemas.RecordingVideo, "/tmp/desktop/rec"), "ffmpeg") {
		t.Error("video note does not name ffmpeg")
	}
	if recordingPathMissing("Error response from daemon: timeout") {
		t.Error("a daemon error read as a missing path")
	}
}
