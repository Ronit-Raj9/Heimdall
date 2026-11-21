package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Screen replay: GET /runs/{id}/frames and /runs/{id}/frames/{n}, read from
// a fake run folder with 3 frames.

type framesIndex struct {
	*fakeIndex
	dir string // run folder of r_t01_A
}

func (f *framesIndex) RunFrame(runID, name string) ([]byte, error) {
	if runID != "r_t01_A" || (name != schemas.FramesIndexFile && !FrameNameOK(name)) {
		return nil, fmt.Errorf("frame %s: %w", name, schemas.ErrNotFound)
	}
	b, err := os.ReadFile(filepath.Join(f.dir, schemas.FramesDir, name))
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("frame %s: %w", name, schemas.ErrNotFound)
	}
	return b, err
}

var (
	framePNG  = []byte("\x89PNG\r\n\x1a\nframe")
	frameJPEG = []byte{0xFF, 0xD8, 0xFF, 0xE0, 'j'}
)

func framesFixture(t *testing.T) *framesIndex {
	t.Helper()
	dir := t.TempDir()
	fd := filepath.Join(dir, schemas.FramesDir)
	if err := os.MkdirAll(fd, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"0001.png": framePNG, "0002.png": framePNG, "0003.jpg": frameJPEG}
	for n, b := range files {
		if err := os.WriteFile(filepath.Join(fd, n), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	index := `{"file":"0001.png","seq":1,"ts":"2026-11-21T10:00:00Z","tool":"screenshot"}
{"file":"../seal.json","ts":"2026-11-21T10:00:01Z"}
not json
{"file":"0002.png","seq":3,"ts":"2026-11-21T10:00:02.500Z","tool":"screenshot","action":"click {\"x\":10,\"y\":20}"}
{"file":"0003.jpg","seq":5,"ts":"2026-11-21T10:00:04Z","tool":"browser_take_screenshot","action":"type {\"text\":\"hi\"}"}
`
	if err := os.WriteFile(filepath.Join(fd, schemas.FramesIndexFile), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
	return &framesIndex{fakeIndex: testIndex(), dir: dir}
}

func TestRunFramesList(t *testing.T) {
	d := fullDeps()
	d.index = framesFixture(t)
	c := newTestClient(t, d)
	resp := c.do(t, "GET", APIPrefix+"/runs/r_t01_A/frames", nil, nil)
	if resp.StatusCode() != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode(), resp.Body())
	}
	var got struct {
		Frames []frameView `json:"frames"`
	}
	if err := json.Unmarshal(resp.Body(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Frames) != 3 {
		t.Fatalf("frames = %d, want 3: %s", len(got.Frames), resp.Body())
	}
	f := got.Frames[1]
	if f.Index != 1 || f.OffsetMS != 2500 || f.Action != `click {"x":10,"y":20}` || f.Image != "frames/0002.png" || f.URL != APIPrefix+"/runs/r_t01_A/frames/1" {
		t.Errorf("frame 1 = %+v", f)
	}
	// A run without frames lists none.
	resp = c.do(t, "GET", APIPrefix+"/runs/r_t02_A/frames", nil, nil)
	if resp.StatusCode() != 200 || !bytes.Contains(resp.Body(), []byte(`"frames":[]`)) {
		t.Fatalf("empty run: %d %s", resp.StatusCode(), resp.Body())
	}
}

func TestRunFrameImage(t *testing.T) {
	d := fullDeps()
	d.index = framesFixture(t)
	c := newTestClient(t, d)
	for _, tc := range []struct {
		n, ctype string
		body     []byte
	}{{"0", "image/png", framePNG}, {"2", "image/jpeg", frameJPEG}} {
		resp := c.do(t, "GET", APIPrefix+"/runs/r_t01_A/frames/"+tc.n, nil, nil)
		if resp.StatusCode() != 200 || !bytes.Equal(resp.Body(), tc.body) {
			t.Fatalf("frame %s: %d %s", tc.n, resp.StatusCode(), resp.Body())
		}
		for k, v := range map[string]string{"Content-Type": tc.ctype, "X-Content-Type-Options": "nosniff", "Content-Security-Policy": "sandbox"} {
			if got := string(resp.Header.Peek(k)); got != v {
				t.Errorf("frame %s header %s = %q, want %q", tc.n, k, got, v)
			}
		}
	}
	for _, n := range []string{"3", "-1", "x", "..%2Fseal.json"} {
		if resp := c.do(t, "GET", APIPrefix+"/runs/r_t01_A/frames/"+n, nil, nil); resp.StatusCode() != 404 {
			t.Errorf("frame %s: status %d, want 404", n, resp.StatusCode())
		}
	}
}

func TestFrameNameOK(t *testing.T) {
	for name, want := range map[string]bool{"0001.png": true, "123456.jpeg": true, "1.png": false, "0001.gif": false, "../0001.png": false, "frames.jsonl": false} {
		if FrameNameOK(name) != want {
			t.Errorf("FrameNameOK(%q) = %v", name, !want)
		}
	}
}
