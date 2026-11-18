package visual

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestSettingsDefaultsAndRanges(t *testing.T) {
	s, err := Settings(schemas.VisualCapture{Kind: schemas.VisualCaptureArtifact})
	if err != nil || s.Width != 1280 || s.Height != 800 || s.Scale != 1 || s.Image != DefaultRenderer {
		t.Fatalf("defaults: %+v %v", s, err)
	}
	for _, bad := range []schemas.VisualCapture{{Width: 8}, {Scale: 4}, {WaitMS: 20000}, {Width: 5000}} {
		if _, err := Settings(bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}

func TestParseHelper(t *testing.T) {
	png := []byte("\x89PNG fake")
	ok := `{"png":"` + base64.StdEncoding.EncodeToString(png) + `","fonts_sha256":"ab"}` + "\n"
	shot, err := parseHelper([]byte(ok), nil, 0)
	if err != nil || string(shot.PNG) != string(png) || shot.Fonts != "ab" {
		t.Fatalf("ok: %+v %v", shot, err)
	}
	if _, err := parseHelper([]byte(`{"error":"page.goto: net::ERR_FILE_NOT_FOUND"}`), nil, 3); err == nil || !strings.Contains(err.Error(), "ERR_FILE_NOT_FOUND") {
		t.Fatalf("helper error: %v", err)
	}
	if _, err := parseHelper(nil, []byte("Error: No such container: x"), 1); err == nil || !strings.Contains(err.Error(), "No such container") {
		t.Fatalf("docker error: %v", err)
	}
	if _, err := parseHelper([]byte("{}"), nil, 0); err == nil {
		t.Fatal("empty output accepted")
	}
	if _, err := parseHelper([]byte(`{"png":"***"}`), nil, 0); err == nil {
		t.Fatal("bad base64 accepted")
	}
	if _, err := parseHelper(make([]byte, maxCaptureOutput+1), nil, 0); err == nil {
		t.Fatal("oversized output accepted")
	}
}

func TestRenderRefusesOtherFiles(t *testing.T) {
	if _, err := (Capturer{}).RenderArtifact(t.Context(), schemas.VisualCapture{}, t.TempDir(), "chart.pdf", nil); err == nil {
		t.Fatal("pdf accepted for rendering")
	}
	if _, err := (Capturer{}).CaptureBrowser(t.Context(), schemas.VisualCapture{URL: "file:///etc/passwd"}, "c"); err == nil {
		t.Fatal("file url accepted for a browser capture")
	}
	if _, err := (Capturer{}).CaptureDesktop(t.Context(), "c", 0, ""); err == nil {
		t.Fatal("port 0 accepted")
	}
}
