package byoworker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Environment a started box reads (deploy/runner/install.sh reads the same
// names, so the one-line install and a platform driver start a runner the
// same way).
const (
	EnvServer      = "HEIMDALL_SERVER"
	EnvRunnerToken = "HEIMDALL_RUNNER_TOKEN"
	EnvPool        = "HEIMDALL_POOL"
	EnvRunnerName  = "HEIMDALL_RUNNER_NAME"
	EnvSlots       = "HEIMDALL_SLOTS"
	EnvLabels      = "HEIMDALL_LABELS"
)

// InstallPath is the install script's path on the hosted server.
const InstallPath = "/api/bench/runners/install.sh"

// PlatformLabel is the runner label that names where a runner runs; the
// workers page shows it as the worker's platform.
func PlatformLabel(platform string) string { return "platform=" + platform }

// BootEnv is the environment of a box a driver starts.
func BootEnv(b Boot, platform string) map[string]string {
	labels := append([]string{PlatformLabel(platform)}, b.Labels...)
	slots := b.Slots
	if slots <= 0 {
		slots = 1
	}
	return map[string]string{
		EnvServer:      strings.TrimRight(b.ServerURL, "/"),
		EnvRunnerToken: b.RunnerToken,
		EnvPool:        b.Pool,
		EnvRunnerName:  b.Name,
		EnvSlots:       strconv.Itoa(slots),
		EnvLabels:      strings.Join(labels, ","),
	}
}

// BootScript is the shell line a Linux box (Ubuntu or Debian) runs at start
// to become a runner: curl when missing, then the one-line install in
// foreground mode (no systemd in a sandbox: the runner is the box's main
// process) with Docker installed when the image has none. The values come
// from BootEnv; nothing secret is in the line itself.
func BootScript() string {
	return `set -e; S=""; [ "$(id -u)" -eq 0 ] || S="sudo -E"; ` +
		`command -v curl >/dev/null 2>&1 || { $S apt-get update -qq && $S apt-get install -y -qq curl ca-certificates; }; ` +
		`curl -fsSL "$` + EnvServer + InstallPath + `" | $S sh -s -- --foreground --install-docker --min-disk-gb 10`
}

// checkBoot refuses a Boot a driver cannot start.
func checkBoot(b Boot) error {
	switch {
	case !strings.HasPrefix(b.ServerURL, "https://") && !strings.HasPrefix(b.ServerURL, "http://127.0.0.1") && !strings.HasPrefix(b.ServerURL, "http://localhost"):
		return fmt.Errorf("%w: the server address %q must be https (set auth.public_url)", ErrInvalid, b.ServerURL)
	case !strings.HasPrefix(b.RunnerToken, "hmr_"):
		return fmt.Errorf("%w: no runner token", ErrInvalid)
	case b.Name == "" || b.Pool == "":
		return fmt.Errorf("%w: a worker needs a name and a pool", ErrInvalid)
	}
	return nil
}

// apiClient is the small JSON-over-HTTPS client the platform drivers share.
type apiClient struct {
	base   string // https://api.example.com, no trailing slash
	token  string
	hc     *http.Client
	header string // "Authorization"
	scheme string // "Bearer "
}

func newAPIClient(base, token string, hc *http.Client) *apiClient {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &apiClient{base: strings.TrimRight(base, "/"), token: token, hc: hc, header: "Authorization", scheme: "Bearer "}
}

// apiError is a non-2xx answer.
type apiError struct {
	Code int
	Body string
}

func (e *apiError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("HTTP %d", e.Code)
	}
	return fmt.Sprintf("HTTP %d: %s", e.Code, e.Body)
}

func isStatus(err error, code int) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.Code == code
}

func (c *apiClient) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set(c.header, c.scheme+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 300 {
			msg = msg[:300] + "..."
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return fmt.Errorf("%w (%v)", ErrRefused, &apiError{Code: resp.StatusCode, Body: msg})
		}
		return &apiError{Code: resp.StatusCode, Body: msg}
	}
	if out != nil && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("decode %s %s: %w", method, path, err)
		}
	}
	return nil
}

// checkResult turns a probe's error into a Health.
func checkResult(now time.Time, err error, okDetail, capacity string) Health {
	h := Health{Checked: now.UTC(), Capacity: capacity}
	switch {
	case err == nil:
		h.OK, h.Detail = true, okDetail
	case errors.Is(err, ErrRefused):
		h.Detail = "The platform refused the token: " + err.Error()
	default:
		h.Detail = "The platform could not be reached: " + err.Error()
	}
	return h
}
