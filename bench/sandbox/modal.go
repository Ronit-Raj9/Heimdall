package sandbox

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Modal (https://modal.com/docs/guide/sandboxes). Modal has no public REST
// sandbox API: its API is gRPC, reached only through the official SDKs. The
// client therefore runs modal_helper.py (embedded) with the official modal
// Python package for each call: one JSON request on stdin, one JSON answer
// on stdout. The organization's token reaches the helper only as
// MODAL_TOKEN_ID and MODAL_TOKEN_SECRET in its environment, with
// MODAL_CONFIG_PATH pointing at an empty file, so a ~/.modal.toml profile
// on this machine is never used instead. The helper's stderr is dropped:
// the SDK may print request details there.

//go:embed modal_helper.py
var modalHelper string

// ModalPythonEnv names the Python interpreter with the modal package
// installed (default python3).
const ModalPythonEnv = "HEIMDALL_MODAL_PYTHON"

const (
	modalOutputCap   = 1 << 20
	modalAnswerCap   = 96 << 20 // a base64 download of the largest file the driver reads
	modalCallTimeout = 2 * time.Minute
)

func init() {
	Register(Info{
		Name: PlatformModal, Title: "Modal",
		TokenHint:     "A Modal API token (Settings, API tokens), as token-id:token-secret (ak-...:as-...).",
		EndpointLabel: "Environment (optional, the Modal environment name)",
		DocsURL:       "https://modal.com/docs/reference/modal.config",
	}, func(c Credentials) (Platform, error) { return NewModal(c, "") })
}

// Modal is the Modal client.
type Modal struct {
	id, secret  string
	environment string
	python      string
	extraEnv    []string // tests
}

// NewModal returns a Modal client. The token is "<token id>:<token secret>"
// (a space also separates). python "" uses HEIMDALL_MODAL_PYTHON, then
// python3.
func NewModal(c Credentials, python string) (*Modal, error) {
	id, secret, ok := splitModalToken(c.Token)
	if !ok {
		return nil, errors.New("Modal: the token must be a token id and secret, as ak-...:as-...")
	}
	if python == "" {
		python = strings.TrimSpace(os.Getenv(ModalPythonEnv))
	}
	if python == "" {
		python = "python3"
	}
	return &Modal{id: id, secret: secret, environment: strings.TrimSpace(c.Endpoint), python: python}, nil
}

func splitModalToken(tok string) (string, string, bool) {
	tok = strings.TrimSpace(tok)
	for _, sep := range []string{":", " "} {
		if id, secret, ok := strings.Cut(tok, sep); ok {
			id, secret = strings.TrimSpace(id), strings.TrimSpace(secret)
			if id != "" && secret != "" {
				return id, secret, true
			}
		}
	}
	return "", "", false
}

// Name implements Platform.
func (m *Modal) Name() string { return PlatformModal }

// modalError is the helper's error answer.
type modalError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// call runs one helper request and decodes the answer into out.
func (m *Modal) call(ctx context.Context, op string, timeout time.Duration, req map[string]any, out any) error {
	req["op"] = op
	in, err := json.Marshal(req)
	if err != nil {
		return err
	}
	cfgDir, err := os.MkdirTemp("", "heimdall-modal-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(cfgDir)
	cfg := filepath.Join(cfgDir, "modal.toml")
	if err := os.WriteFile(cfg, nil, 0o600); err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, m.python, "-I", "-c", modalHelper)
	env := []string{"MODAL_TOKEN_ID=" + m.id, "MODAL_TOKEN_SECRET=" + m.secret, "MODAL_CONFIG_PATH=" + cfg,
		"HOME=" + cfgDir, "PATH=" + os.Getenv("PATH"), "PYTHONUNBUFFERED=1"}
	if m.environment != "" {
		env = append(env, "MODAL_ENVIRONMENT="+m.environment)
	}
	for _, k := range []string{"SSL_CERT_FILE", "SSL_CERT_DIR", "HTTPS_PROXY", "https_proxy", "NO_PROXY", "no_proxy", "TMPDIR"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	cmd.Env = append(env, m.extraEnv...)
	cmd.Stdin = bytes.NewReader(in)
	var stdout capBuffer
	stdout.max = modalAnswerCap
	cmd.Stdout = &stdout
	runErr := cmd.Run()
	if cctx.Err() != nil {
		return fmt.Errorf("Modal %s: %w", op, cctx.Err())
	}
	if stdout.over {
		return fmt.Errorf("Modal %s: the helper's answer is over %d bytes", op, modalAnswerCap)
	}
	var me modalError
	if json.Unmarshal(stdout.Bytes(), &me) == nil && me.Error != "" {
		msg := scrub(me.Message, []string{m.id, m.secret})
		switch me.Error {
		case "unauthorized":
			return fmt.Errorf("Modal %s: %w (%s)", op, ErrUnauthorized, msg)
		case "not_found":
			return fmt.Errorf("Modal %s: %w (%s)", op, ErrNotFound, msg)
		case "too_large":
			return fmt.Errorf("Modal %s: %w", op, ErrTooLarge)
		}
		return fmt.Errorf("Modal %s: %s", op, msg)
	}
	if runErr != nil {
		return fmt.Errorf("Modal %s: helper failed (%s with %s; is the modal package installed? set %s): %s", op,
			scrub(runErr.Error(), []string{m.id, m.secret}), m.python, ModalPythonEnv, scrub(tail(stdout.Bytes(), 300), []string{m.id, m.secret}))
	}
	if out != nil {
		if err := json.Unmarshal(stdout.Bytes(), out); err != nil {
			return fmt.Errorf("Modal %s: decode helper answer: %w", op, err)
		}
	}
	return nil
}

// capBuffer keeps at most max bytes.
type capBuffer struct {
	bytes.Buffer
	max  int
	over bool
}

func (b *capBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.Len(); len(p) > room {
		b.over = true
		if room > 0 {
			b.Buffer.Write(p[:room])
		}
		return len(p), nil
	}
	return b.Buffer.Write(p)
}

// Check implements Platform: an authenticated lookup of the app the
// sandboxes run under (created on first use).
func (m *Modal) Check(ctx context.Context) error {
	var out struct {
		OK bool `json:"ok"`
	}
	if err := m.call(ctx, "check", modalCallTimeout, map[string]any{}, &out); err != nil {
		return err
	}
	if !out.OK {
		return errors.New("Modal check: the helper did not answer ok")
	}
	return nil
}

// Create implements Platform.
func (m *Modal) Create(ctx context.Context, s Spec) (*Box, error) {
	in := map[string]any{
		"name": s.Name, "image": s.Image, "env": s.Env, "tags": s.Labels,
		"timeout_s": lifetimeSeconds(s), "block_network": s.Egress.BlockAll || (len(s.Egress.AllowHosts) == 0 && len(s.Egress.AllowCIDRs) == 0),
		"allow_hosts": sortedCopy(s.Egress.AllowHosts), "allow_cidrs": sortedCopy(s.Egress.AllowCIDRs),
	}
	if s.CPUs > 0 {
		in["cpu"] = s.CPUs
	}
	if s.MemGB > 0 {
		in["memory_mb"] = int(math.Ceil(s.MemGB * 1024))
	}
	var out struct {
		SandboxID string `json:"sandbox_id"`
	}
	if err := m.call(ctx, "create", createTimeout, in, &out); err != nil {
		return nil, err
	}
	if out.SandboxID == "" {
		return nil, errors.New("Modal create: the answer has no sandbox id")
	}
	return &Box{ID: out.SandboxID, Platform: PlatformModal, Data: map[string]string{}}, nil
}

// Exec implements Platform.
func (m *Modal) Exec(ctx context.Context, b *Box, c Command) (*Result, error) {
	if b == nil || b.ID == "" {
		return nil, errors.New("Modal: no sandbox")
	}
	to := commandTimeout(c)
	in := map[string]any{"sandbox_id": b.ID, "argv": c.Argv, "env": c.Env, "cwd": c.Cwd, "timeout_s": int(math.Ceil(to.Seconds()))}
	var out struct {
		ExitCode *int   `json:"exit_code"`
		Output   string `json:"output"`
	}
	if err := m.call(ctx, "exec", to+modalCallTimeout, in, &out); err != nil {
		return nil, err
	}
	if out.ExitCode == nil {
		return nil, errors.New("Modal exec: the answer has no exit code")
	}
	raw, err := base64.StdEncoding.DecodeString(out.Output)
	if err != nil {
		return nil, fmt.Errorf("Modal exec: output is not base64: %w", err)
	}
	if len(raw) > modalOutputCap {
		raw = raw[:modalOutputCap]
	}
	return &Result{ExitCode: *out.ExitCode, Output: raw}, nil
}

// Upload implements Platform.
func (m *Modal) Upload(ctx context.Context, b *Box, path string, data []byte) error {
	if b == nil || b.ID == "" {
		return errors.New("Modal: no sandbox")
	}
	return m.call(ctx, "upload", modalCallTimeout, map[string]any{"sandbox_id": b.ID, "path": path, "data": base64.StdEncoding.EncodeToString(data)}, nil)
}

// Download implements Platform.
func (m *Modal) Download(ctx context.Context, b *Box, path string, max int64) ([]byte, error) {
	if b == nil || b.ID == "" {
		return nil, errors.New("Modal: no sandbox")
	}
	var out struct {
		Data string `json:"data"`
	}
	if err := m.call(ctx, "download", modalCallTimeout, map[string]any{"sandbox_id": b.ID, "path": path, "max_bytes": max}, &out); err != nil {
		if errors.Is(err, ErrTooLarge) {
			return nil, fmt.Errorf("Modal download %s: %w (over %d bytes)", path, ErrTooLarge, max)
		}
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(out.Data)
	if err != nil {
		return nil, fmt.Errorf("Modal download %s: not base64: %w", path, err)
	}
	if int64(len(raw)) > max {
		return nil, fmt.Errorf("Modal download %s: %w (over %d bytes)", path, ErrTooLarge, max)
	}
	return raw, nil
}

// Destroy implements Platform.
func (m *Modal) Destroy(ctx context.Context, b *Box) error {
	if b == nil || b.ID == "" {
		return nil
	}
	err := m.call(ctx, "terminate", modalCallTimeout, map[string]any{"sandbox_id": b.ID}, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}
