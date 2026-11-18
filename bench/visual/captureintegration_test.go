//go:build integration

package visual

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Real-docker capture tests (X1). Run one at a time:
//
//	GOWORK=off go test -tags integration ./visual/ -run TestIntegration -p 1 -v
//
// They need the pinned Playwright image (docs/browser-tasks.md), the bench
// helper image bench/coding-core-base:1 for the web server, and, for the
// desktop test, heimdall-example/acme-desktop:1 (examples/acme-desktop).
// Every container and network is named hd-x1-it-* and removed at the end.

const repeats = 5

func dockerOK(t *testing.T, images ...string) {
	t.Helper()
	for _, img := range images {
		if err := exec.Command("docker", "image", "inspect", img).Run(); err != nil {
			t.Skipf("image %s is not present: %v", img, err)
		}
	}
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

const pageHTML = `<!doctype html><html><head><meta charset="utf-8"><link rel="stylesheet" href="style.css"></head>
<body><main class="card"><h1>Account settings</h1><p>Updated just now</p><div class="actions"><button class="primary">Save changes</button></div></main></body></html>`

const pageCSS = `body{margin:0;font-family:"Liberation Sans",sans-serif;background:#f3f4f6}
.card{width:520px;margin:40px auto;background:#fff;border-radius:8px;padding:24px}
h1{font-size:22px;margin:0 0 8px}.actions{display:flex;justify-content:center;margin-top:24px}
.primary{background:#1d4ed8;color:#fff;border:0;border-radius:6px;padding:10px 22px;font-size:16px}`

const chartSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="320" height="200" viewBox="0 0 320 200">
<rect width="320" height="200" fill="#ffffff"/><rect x="40" y="60" width="50" height="110" fill="#2563eb"/>
<rect x="130" y="100" width="50" height="70" fill="#2563eb"/><text x="65" y="190" font-family="Liberation Sans" font-size="14" text-anchor="middle">Q1</text>
<text x="155" y="190" font-family="Liberation Sans" font-size="14" text-anchor="middle">Q2</text></svg>`

func TestIntegrationRenderArtifactIsDeterministic(t *testing.T) {
	dockerOK(t, DefaultRenderer)
	dir := t.TempDir()
	for name, body := range map[string]string{"index.html": pageHTML, "style.css": pageCSS, "chart.svg": chartSVG} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	c := Capturer{}
	labels := utils.LabelArgs("e_hdx1it", "r_hdx1it")
	for _, tc := range []struct {
		name string
		set  schemas.VisualCapture
		w, h int
	}{
		{"index.html", schemas.VisualCapture{Width: 800, Height: 400}, 800, 400},
		{"index.html", schemas.VisualCapture{Width: 800, Height: 300, Scale: 2, FullPage: true}, 1600, -1},
		{"chart.svg", schemas.VisualCapture{Width: 320, Height: 200}, 320, 200},
	} {
		var first string
		var firstImg *Image
		for i := 0; i < repeats; i++ {
			shot, err := c.RenderArtifact(context.Background(), tc.set, dir, tc.name, labels)
			if err != nil {
				t.Fatalf("%s render %d: %v", tc.name, i, err)
			}
			img, err := Decode(shot.PNG)
			if err != nil {
				t.Fatal(err)
			}
			if img.W != tc.w || (tc.h > 0 && img.H != tc.h) {
				t.Fatalf("%s: %dx%d, want %dx%d", tc.name, img.W, img.H, tc.w, tc.h)
			}
			if shot.Fonts == "" {
				t.Fatalf("%s: no font hash", tc.name)
			}
			if i == 0 {
				first, firstImg = sum(shot.PNG), img
				continue
			}
			if got := sum(shot.PNG); got != first {
				v, _, _ := PixelDiff(img, firstImg, img.Bounds(), nil, 0)
				t.Fatalf("%s capture %d differs from the first: %s vs %s (%.6f of pixels)", tc.name, i+1, got, first, v)
			}
		}
		t.Logf("%s %+v: %d identical captures, sha256 %s", tc.name, tc.set, repeats, first)
	}
}

func TestIntegrationBrowserCaptureIsDeterministic(t *testing.T) {
	dockerOK(t, DefaultRenderer, "bench/coding-core-base:1")
	ctx := context.Background()
	d := utils.Docker{}
	net, web, br := "hd-x1-it-net", "hd-x1-it-webapp", "hd-x1-it-browser"
	cleanup := func() {
		_, _ = d.Run(ctx, "rm", "-f", web, br)
		_, _ = d.Run(ctx, "network", "rm", net)
	}
	cleanup()
	t.Cleanup(cleanup)
	if _, err := d.MustRun(ctx, "network", "create", "--internal", net); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, body := range map[string]string{"index.html": pageHTML, "style.css": pageCSS} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.Chmod(dir, 0o755)
	if _, err := d.MustRun(ctx, "run", "-d", "--name", web, "--network", net, "--network-alias", "webapp", "-v", dir+":/srv:ro",
		"bench/coding-core-base:1", "python3", "-m", "http.server", "8000", "--directory", "/srv"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.MustRun(ctx, "run", "-d", "--name", br, "--network", net, "--entrypoint", "sleep", DefaultRenderer, "600"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
	c := Capturer{Docker: d}
	set := schemas.VisualCapture{Kind: schemas.VisualCaptureBrowser, URL: "http://webapp:8000/index.html", Width: 800, Height: 400}
	var first string
	for i := 0; i < repeats; i++ {
		shot, err := c.CaptureBrowser(ctx, set, br)
		if err != nil {
			t.Fatalf("capture %d: %v", i, err)
		}
		if i == 0 {
			first = sum(shot.PNG)
			img, err := Decode(shot.PNG)
			if err != nil || img.W != 800 || img.H != 400 {
				t.Fatalf("capture: %v", err)
			}
			dom := DominantColors(img, Rect{0, 0, 800, 400}, nil, 4)
			if dom[0].Hex() != "#f3f4f6" {
				t.Fatalf("page background not rendered: %+v", dom)
			}
			continue
		}
		if got := sum(shot.PNG); got != first {
			t.Fatalf("capture %d differs: %s vs %s", i+1, got, first)
		}
	}
	t.Logf("browser capture: %d identical captures, sha256 %s", repeats, first)
	// Another origin is never loaded: the page cannot pull the capture off
	// the task's application.
	set.URL = "http://example.com/"
	if _, err := c.CaptureBrowser(ctx, set, br); err == nil || !strings.Contains(err.Error(), "ERR") {
		t.Fatalf("capture of an unreachable origin: %v", err)
	}
}

func TestIntegrationDesktopCaptureIsDeterministic(t *testing.T) {
	const img = "heimdall-example/acme-desktop:1"
	dockerOK(t, img)
	ctx := context.Background()
	d := utils.Docker{}
	name := "hd-x1-it-desktop"
	_, _ = d.Run(ctx, "rm", "-f", name)
	t.Cleanup(func() { _, _ = d.Run(context.Background(), "rm", "-f", name) })
	if _, err := d.MustRun(ctx, "run", "-d", "--name", name, "--network", "none", "-e", "DESKTOP_APP=files", "-e", "DESKTOP_OPEN=/home/desk/Documents", img); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		res, _ := d.Run(ctx, "exec", name, "python3", "-I", "/opt/desktop/desktop.py", "ready")
		if res != nil && res.ExitCode == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("desktop not ready in 90 s")
		}
		time.Sleep(time.Second)
	}
	c := Capturer{Docker: d}
	var first []byte
	for i := 0; i < repeats; i++ {
		shot, err := c.CaptureDesktop(ctx, name, 8000, "/mcp")
		if err != nil {
			t.Fatalf("capture %d: %v", i, err)
		}
		if i == 0 {
			first = shot.PNG
			m, err := Decode(shot.PNG)
			if err != nil || m.W != 1280 || m.H != 800 {
				t.Fatalf("desktop capture: %v", err)
			}
			continue
		}
		if !bytes.Equal(shot.PNG, first) {
			t.Fatalf("desktop capture %d differs from the first", i+1)
		}
	}
	t.Logf("desktop capture: %d identical captures, sha256 %s", repeats, sum(first))
}
