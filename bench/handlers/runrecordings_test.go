package handlers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Recordings (Z03): GET /runs/{id}/recordings and /runs/{id}/recordings/{n},
// read from a fake run folder with a video, a HAR and a failed console
// recording.

type recordingsIndex struct {
	*fakeIndex
	dir string // run folder of r_t01_A
}

func (f *recordingsIndex) RunRecordingPath(runID, service, name string) (string, error) {
	if runID != "r_t01_A" || strings.ContainsAny(service+name, "/\\") || name == "" {
		return "", fmt.Errorf("recording %s: %w", name, schemas.ErrNotFound)
	}
	p := filepath.Join(f.dir, schemas.RecordingsDir, service, name)
	if _, err := os.Lstat(p); err != nil {
		return "", fmt.Errorf("recording %s: %w", name, schemas.ErrNotFound)
	}
	return p, nil
}

// recordingVideo is a stand-in for a webm: only its bytes matter here.
var recordingVideo = []byte("0123456789abcdefghij")

func recordingsFixture(t *testing.T) *recordingsIndex {
	t.Helper()
	dir := t.TempDir()
	sd := filepath.Join(dir, schemas.RecordingsDir, "browser")
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatal(err)
	}
	for n, b := range map[string][]byte{"video-page-1.webm": recordingVideo, "har-context-1.har": []byte(`{"log":{}}`), "evil.sh": []byte("x")} {
		if err := os.WriteFile(filepath.Join(sd, n), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	index := `{"service":"browser","id":"video","kind":"video","file":"video-page-1.webm","bytes":20,"sha256":"abc","started_at":"2026-11-21T10:00:00Z"}
{"service":"browser","id":"har","kind":"har","file":"har-context-1.har","bytes":10}
{"service":"browser","id":"console","kind":"console","error":"not written: no console file at /tmp/rec/console.jsonl"}
{"service":"browser","id":"x","kind":"video","file":"evil.sh"}
not json
`
	if err := os.WriteFile(filepath.Join(dir, schemas.RecordingsDir, schemas.RecordingsIndexFile), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
	idx := &recordingsIndex{fakeIndex: testIndex(), dir: dir}
	idx.files["r_t01_A"][FileToolLedger] = []byte(`{"seq":1,"server":"browser","tool":"browser_navigate","protocol":"mcp","arguments":"{\"url\":\"http://webapp:8000/\"}","started":"2026-11-21T10:00:01Z","latency_ms":300}
{"seq":2,"server":"other","tool":"search","protocol":"mcp","started":"2026-11-21T10:00:02Z"}
{"seq":3,"server":"browser","tool":"browser_click","protocol":"mcp","arguments":"{\"element\":\"Save button\",\"ref\":\"e7\"}","started":"2026-11-21T10:00:04Z","latency_ms":120,"fault":"error"}
`)
	idx.files["r_t01_A"][schemas.HumanActionsFile] = []byte(`{"seq":1,"service":"browser","action":"click","x":5,"y":6}` + "\n")
	return idx
}

func TestRunRecordingsList(t *testing.T) {
	d := fullDeps()
	d.index = recordingsFixture(t)
	c := newTestClient(t, d)
	resp := c.do(t, "GET", APIPrefix+"/runs/r_t01_A/recordings", nil, nil)
	if resp.StatusCode() != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode(), resp.Body())
	}
	var got RunRecordings
	if err := json.Unmarshal(resp.Body(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Recordings) != 3 {
		t.Fatalf("recordings = %d, want 3 (a name the kind never records is dropped): %s", len(got.Recordings), resp.Body())
	}
	v := got.Recordings[0]
	if v.Mime != "video/webm" || v.URL != APIPrefix+"/runs/r_t01_A/recordings/0" || v.StartedAt != "2026-11-21T10:00:00Z" {
		t.Errorf("video row %+v", v)
	}
	if got.Recordings[2].Error == "" || got.Recordings[2].URL != "" {
		t.Errorf("failed recording %+v: want its error and no URL", got.Recordings[2])
	}
	if len(got.Steps) != 2 || got.Steps[0].Label != "browser_navigate http://webapp:8000/" || !got.Steps[1].IsError {
		t.Errorf("steps %+v: want the browser's two calls, the faulted one marked", got.Steps)
	}
	if len(got.Actions) != 1 || got.Actions[0].Action != "click" {
		t.Errorf("actions %+v", got.Actions)
	}
}

func TestRunRecordingsEmptyWithoutReader(t *testing.T) {
	c := newTestClient(t, fullDeps())
	resp := c.do(t, "GET", APIPrefix+"/runs/r_t01_A/recordings", nil, nil)
	if resp.StatusCode() != 200 || !strings.Contains(string(resp.Body()), `"recordings":[]`) {
		t.Fatalf("status %d: %s", resp.StatusCode(), resp.Body())
	}
	if resp := c.do(t, "GET", APIPrefix+"/runs/r_t01_A/recordings/0", nil, nil); resp.StatusCode() != 404 {
		t.Fatalf("file without a reader: status %d", resp.StatusCode())
	}
}

func TestRunRecordingRanges(t *testing.T) {
	d := fullDeps()
	d.index = recordingsFixture(t)
	c := newTestClient(t, d)
	p := APIPrefix + "/runs/r_t01_A/recordings/0"
	resp := c.do(t, "GET", p, nil, nil)
	if resp.StatusCode() != 200 || string(resp.Body()) != string(recordingVideo) || string(resp.Header.ContentType()) != "video/webm" {
		t.Fatalf("whole file: %d %q %s", resp.StatusCode(), resp.Body(), resp.Header.ContentType())
	}
	if string(resp.Header.Peek("Accept-Ranges")) != "bytes" {
		t.Errorf("no Accept-Ranges")
	}
	resp = c.do(t, "GET", p, nil, map[string]string{"Range": "bytes=2-5"})
	if resp.StatusCode() != 206 || string(resp.Body()) != "2345" || string(resp.Header.Peek("Content-Range")) != "bytes 2-5/20" {
		t.Fatalf("range: %d %q %s", resp.StatusCode(), resp.Body(), resp.Header.Peek("Content-Range"))
	}
	resp = c.do(t, "GET", p, nil, map[string]string{"Range": "bytes=-3"})
	if resp.StatusCode() != 206 || string(resp.Body()) != "hij" {
		t.Fatalf("suffix range: %d %q", resp.StatusCode(), resp.Body())
	}
	if resp = c.do(t, "GET", p, nil, map[string]string{"Range": "bytes=99-"}); resp.StatusCode() != 416 {
		t.Fatalf("range past the end: %d", resp.StatusCode())
	}
	for _, n := range []string{"2", "3", "9", "-1", "x"} {
		if resp := c.do(t, "GET", APIPrefix+"/runs/r_t01_A/recordings/"+n, nil, nil); resp.StatusCode() != 404 {
			t.Errorf("recording %s: status %d, want 404", n, resp.StatusCode())
		}
	}
}

func TestParseRange(t *testing.T) {
	for _, tc := range []struct {
		h           string
		start, n    int64
		partial, ok bool
	}{
		{"", 0, 100, false, true},
		{"bytes=0-", 0, 100, true, true},
		{"bytes=10-19", 10, 10, true, true},
		{"bytes=90-200", 90, 10, true, true},
		{"bytes=-10", 90, 10, true, true},
		{"bytes=-1000", 0, 100, true, true},
		{"bytes=0-1,5-6", 0, 100, false, true},
		{"bytes=100-", 0, 0, false, false},
		{"bytes=5-2", 0, 0, false, false},
		{"bytes=-", 0, 0, false, false},
		{"items=0-1", 0, 0, false, false},
		{"bytes=a-b", 0, 0, false, false},
	} {
		s, n, partial, ok := ParseRange(tc.h, 100)
		if s != tc.start || n != tc.n || partial != tc.partial || ok != tc.ok {
			t.Errorf("ParseRange(%q) = %d %d %v %v, want %d %d %v %v", tc.h, s, n, partial, ok, tc.start, tc.n, tc.partial, tc.ok)
		}
	}
}

func TestStepLabel(t *testing.T) {
	for _, tc := range []struct{ tool, args, want string }{
		{"browser_navigate", `{"url":"http://webapp:8000/orders"}`, "browser_navigate http://webapp:8000/orders"},
		{"click", `{"x":10,"y":20,"button":"left"}`, "click (10, 20)"},
		{"key", `{"combo":"ctrl+s"}`, "key ctrl+s"},
		{"screenshot", `{}`, "screenshot"},
		{"screenshot", `not json`, "screenshot"},
		{"type", `{"text":"line one\nline two"}`, "type line one line two"},
	} {
		if got := StepLabel(tc.tool, tc.args); got != tc.want {
			t.Errorf("StepLabel(%s, %s) = %q, want %q", tc.tool, tc.args, got, tc.want)
		}
	}
	long := StepLabel("type", `{"text":"`+strings.Repeat("a", 300)+`"}`)
	if len(long) > 120 || !strings.HasSuffix(long, "...") {
		t.Errorf("long label %d bytes: %q", len(long), long)
	}
}
