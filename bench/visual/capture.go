package visual

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// DefaultRenderer is the image svg and html artifacts are rendered with and
// the image a browser capture expects in the browser service: the pinned
// Playwright MCP image of the browser tasks (docs/browser-tasks.md), which
// carries Chromium, Node, playwright-core at /app/node_modules and a fixed
// font set.
const DefaultRenderer = "mcr.microsoft.com/playwright/mcp:v0.0.83@sha256:690763cbb437ee7233f538a953bb5390bf03791eb5c965b471e59d17a6132fcb"

// Capture defaults and limits.
const (
	DefaultWidth   = 1280
	DefaultHeight  = 800
	DefaultScale   = 1
	MaxScale       = 3
	MaxWaitMS      = 10000
	captureTimeout = 120 * time.Second
	// maxCaptureOutput bounds what a capture helper may print: the base64
	// of a capped PNG plus a little JSON.
	maxCaptureOutput = MaxImageBytes/3*4 + 64<<10
)

//go:embed render.js
var renderScript string

//go:embed desktopshot.py
var desktopScript string

// Shot is one capture: the PNG bytes and what the helper reported.
type Shot struct {
	PNG    []byte
	Fonts  string // sha256 of the renderer's font list, when it reported one
	Parked bool   // desktop: the pointer was parked in the corner before the shot
	Source schemas.VisualSource
}

// Capturer takes the bench's own captures through the docker CLI.
type Capturer struct {
	Docker utils.Docker
}

// renderRequest is what render.js reads (its first argument, as JSON).
type renderRequest struct {
	URL      string `json:"url"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Scale    int    `json:"scale"`
	FullPage bool   `json:"full_page"`
	WaitMS   int    `json:"wait_ms"`
	Origin   string `json:"origin,omitempty"` // only requests to this origin (and data:) load; empty allows file: only
}

// helperOutput is what render.js and desktopshot.py print.
type helperOutput struct {
	PNG    string `json:"png"`
	Fonts  string `json:"fonts_sha256"`
	Parked bool   `json:"pointer_parked"`
	Error  string `json:"error"`
}

// Settings fills a capture's defaults and checks its ranges.
func Settings(c schemas.VisualCapture) (schemas.VisualCapture, error) {
	if c.Width == 0 {
		c.Width = DefaultWidth
	}
	if c.Height == 0 {
		c.Height = DefaultHeight
	}
	if c.Scale == 0 {
		c.Scale = DefaultScale
	}
	switch {
	case c.Width < 16 || c.Width > MaxImageWidth/MaxScale || c.Height < 16 || c.Height > MaxImageHeight/MaxScale:
		return c, fmt.Errorf("capture viewport %dx%d is outside 16x16 to %dx%d", c.Width, c.Height, MaxImageWidth/MaxScale, MaxImageHeight/MaxScale)
	case c.Scale < 1 || c.Scale > MaxScale:
		return c, fmt.Errorf("capture scale %d is outside 1 to %d", c.Scale, MaxScale)
	case c.WaitMS < 0 || c.WaitMS > MaxWaitMS:
		return c, fmt.Errorf("capture wait_ms %d is outside 0 to %d", c.WaitMS, MaxWaitMS)
	}
	if c.Image == "" {
		c.Image = DefaultRenderer
	}
	return c, nil
}

// RenderArtifact renders the svg or html file name in dir (a folder of
// the run's artifacts) in a fresh container of the renderer image with no
// network, no capabilities and the folder read-only, and returns the PNG.
// The labels let the janitor remove a container a crash left behind.
func (c Capturer) RenderArtifact(ctx context.Context, set schemas.VisualCapture, dir, name string, labels []string) (*Shot, error) {
	set, err := Settings(set)
	if err != nil {
		return nil, err
	}
	ext := strings.ToLower(path.Ext(name))
	if ext != ".svg" && ext != ".html" && ext != ".htm" {
		return nil, fmt.Errorf("artifact %s is not svg or html", name)
	}
	req := renderRequest{URL: "file:///in/" + url.PathEscape(name), Width: set.Width, Height: set.Height, Scale: set.Scale,
		FullPage: set.FullPage && ext != ".svg", WaitMS: set.WaitMS}
	arg, _ := json.Marshal(req)
	args := []string{"run", "--rm", "--network", "none", "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
		"--memory", "2g", "--pids-limit", "512", "--user", "1000:1000", "--tmpfs", "/tmp:size=256m", "-v", dir + ":/in:ro"}
	args = append(args, labels...)
	args = append(args, "--entrypoint", "node", set.Image, "-e", renderScript, string(arg))
	shot, err := c.run(ctx, args)
	if err != nil {
		return nil, fmt.Errorf("render %s: %w", name, err)
	}
	shot.Source = schemas.VisualSource{Kind: schemas.VisualCaptureArtifact, Renderer: set.Image, Width: set.Width, Height: set.Height,
		Scale: set.Scale, FullPage: req.FullPage, Fonts: shot.Fonts}
	return shot, nil
}

// CaptureBrowser opens set.URL in a new Chromium inside the run's browser
// service container (the agent's browser session is not touched), at the
// fixed viewport and scale, loads only that URL's origin, and screenshots
// it.
func (c Capturer) CaptureBrowser(ctx context.Context, set schemas.VisualCapture, container string) (*Shot, error) {
	set, err := Settings(set)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(set.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("capture url %q is not an http(s) url", set.URL)
	}
	req := renderRequest{URL: set.URL, Width: set.Width, Height: set.Height, Scale: set.Scale, FullPage: set.FullPage, WaitMS: set.WaitMS,
		Origin: u.Scheme + "://" + u.Host}
	arg, _ := json.Marshal(req)
	shot, err := c.run(ctx, []string{"exec", container, "node", "-e", renderScript, string(arg)})
	if err != nil {
		return nil, fmt.Errorf("browser capture of %s: %w", set.URL, err)
	}
	shot.Source = schemas.VisualSource{Kind: schemas.VisualCaptureBrowser, URL: set.URL, Width: set.Width, Height: set.Height,
		Scale: set.Scale, FullPage: set.FullPage, Fonts: shot.Fonts}
	return shot, nil
}

// CaptureDesktop calls the desktop service's own MCP screenshot tool from
// inside its container (at 127.0.0.1:port), the tool the agent used, so the
// bench sees the screen the way the agent last could.
func (c Capturer) CaptureDesktop(ctx context.Context, container string, port int, mcpPath string) (*Shot, error) {
	if port <= 0 || port > 65535 {
		return nil, fmt.Errorf("desktop service port %d is not usable", port)
	}
	if mcpPath == "" {
		mcpPath = "/mcp"
	}
	if !strings.HasPrefix(mcpPath, "/") {
		return nil, fmt.Errorf("desktop mcp path %q must start with /", mcpPath)
	}
	endpoint := "http://127.0.0.1:" + strconv.Itoa(port) + mcpPath
	shot, err := c.run(ctx, []string{"exec", container, "python3", "-I", "-c", desktopScript, endpoint})
	if err != nil {
		return nil, fmt.Errorf("desktop screenshot: %w", err)
	}
	shot.Source = schemas.VisualSource{Kind: schemas.VisualCaptureDesktop, PointerParked: shot.Parked}
	return shot, nil
}

// run runs one capture helper and reads its single JSON line.
func (c Capturer) run(ctx context.Context, args []string) (*Shot, error) {
	ctx, cancel := context.WithTimeout(ctx, captureTimeout)
	defer cancel()
	res, err := c.Docker.Run(ctx, args...)
	if err != nil {
		return nil, err
	}
	return parseHelper(res.Stdout, res.Stderr, res.ExitCode)
}

// parseHelper reads a helper's output: one JSON object with the base64 PNG,
// or an error. It is pure.
func parseHelper(stdout, stderr []byte, exit int) (*Shot, error) {
	if len(stdout) > maxCaptureOutput {
		return nil, fmt.Errorf("capture output is %d bytes, over the %d byte cap", len(stdout), maxCaptureOutput)
	}
	var out helperOutput
	jerr := json.Unmarshal([]byte(strings.TrimSpace(string(stdout))), &out)
	switch {
	case jerr == nil && out.Error != "":
		return nil, errors.New(clip(out.Error))
	case exit != 0:
		return nil, fmt.Errorf("exit %d: %s", exit, clip(strings.TrimSpace(string(stderr))))
	case jerr != nil:
		return nil, fmt.Errorf("capture output is not json: %v", jerr)
	case out.PNG == "":
		return nil, errors.New("capture output holds no image")
	}
	png, err := base64.StdEncoding.DecodeString(out.PNG)
	if err != nil {
		return nil, fmt.Errorf("capture image is not base64: %w", err)
	}
	return &Shot{PNG: png, Fonts: out.Fonts, Parked: out.Parked}, nil
}

func clip(s string) string {
	if len(s) > 400 {
		return strings.ToValidUTF8(s[:400], "") + "..."
	}
	return s
}
