package openmodel

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/sandbox"
)

// Modal has no public REST API for apps: deploys and stops go through the
// official modal package (its CLI and SDK), run as modal_serve.py with the
// organization's token in the environment only, exactly like the sandbox
// driver's helper (sandbox/modal.go): an empty MODAL_CONFIG_PATH and HOME,
// so a ~/.modal.toml on this machine is never used instead.

//go:embed modal_serve.py
var serveHelper string

// DeploySpec is one deployment as the deployer receives it.
type DeploySpec struct {
	AppName      string
	Engine       string
	Model        Model
	GPU          sandbox.GPUType
	GPUCount     int
	APIKey       string // the deployment's own key; the server refuses any other
	HFToken      string // gated models only
	ScaledownS   int
	StartupS     int
	Concurrency  int
	DeployWithin time.Duration
}

// AppInfo is one Modal app this package deployed.
type AppInfo struct {
	Name  string `json:"name"`
	AppID string `json:"app_id"`
	State string `json:"state"`
	Tasks string `json:"tasks"`
}

// Deployer deploys and stops serving apps on an org's account. ModalDeployer
// is the real one; tests use a fake.
type Deployer interface {
	Deploy(ctx context.Context, c sandbox.Credentials, s DeploySpec) (url string, err error)
	Stop(ctx context.Context, c sandbox.Credentials, appName string) error
	List(ctx context.Context, c sandbox.Credentials) ([]AppInfo, error)
}

// ModalDeployer runs modal_serve.py with Python (sandbox.ModalPythonEnv,
// then python3).
type ModalDeployer struct {
	Python string
}

// Deploy implements Deployer.
func (d ModalDeployer) Deploy(ctx context.Context, c sandbox.Credentials, s DeploySpec) (string, error) {
	gpu := s.GPU.Name
	if s.GPUCount > 1 {
		gpu = fmt.Sprintf("%s:%d", s.GPU.Name, s.GPUCount)
	}
	within := s.DeployWithin
	if within <= 0 {
		within = 25 * time.Minute
	}
	req := map[string]any{
		"app_name": s.AppName, "engine": s.Engine, "model": s.Model.ID, "served_name": servedName(s.Model), "gpu": gpu,
		"args": serverArgs(s.Engine, s.Model, s.GPU, s.GPUCount), "vllm_version": VLLMVersion, "sglang_version": SGLangVersion,
		"scaledown_s": s.ScaledownS, "startup_timeout_s": s.StartupS, "concurrency": s.Concurrency,
		"hf_token": s.HFToken != "", "deploy_timeout_s": int(within.Seconds()),
	}
	extra := []string{"HEIMDALL_SERVE_KEY=" + s.APIKey}
	if s.HFToken != "" {
		extra = append(extra, "HF_TOKEN="+s.HFToken)
	}
	var out struct {
		URL string `json:"url"`
	}
	if err := d.call(ctx, c, "deploy", within+time.Minute, req, extra, []string{s.APIKey, s.HFToken}, &out); err != nil {
		return "", err
	}
	if !strings.HasPrefix(out.URL, "https://") {
		return "", errors.New("Modal deploy: the answer has no https endpoint URL")
	}
	return strings.TrimRight(out.URL, "/"), nil
}

// Stop implements Deployer. Stopping an app that is gone is not an error.
func (d ModalDeployer) Stop(ctx context.Context, c sandbox.Credentials, name string) error {
	if !appNameRe.MatchString(name) {
		return fmt.Errorf("Modal stop: %q is not an app this package deployed", name)
	}
	return d.call(ctx, c, "stop", 4*time.Minute, map[string]any{"app_name": name}, nil, nil, nil)
}

// List implements Deployer: the org's apps whose name starts with
// AppPrefix, in any state Modal lists (deployed, stopped recently, ...).
func (d ModalDeployer) List(ctx context.Context, c sandbox.Credentials) ([]AppInfo, error) {
	var out struct {
		Apps []AppInfo `json:"apps"`
	}
	if err := d.call(ctx, c, "list", 3*time.Minute, map[string]any{}, nil, nil, &out); err != nil {
		return nil, err
	}
	return out.Apps, nil
}

func (d ModalDeployer) python() string {
	if d.Python != "" {
		return d.Python
	}
	if p := strings.TrimSpace(os.Getenv(sandbox.ModalPythonEnv)); p != "" {
		return p
	}
	return "python3"
}

// ErrUnauthorized is Modal refusing the org's token.
var ErrUnauthorized = errors.New("Modal refused the token; connect Modal again in Settings, Workers")

func (d ModalDeployer) call(ctx context.Context, c sandbox.Credentials, op string, timeout time.Duration, req map[string]any, extraEnv, secrets []string, out any) error {
	id, secret, ok := strings.Cut(strings.TrimSpace(c.Token), ":")
	if !ok {
		id, secret, ok = strings.Cut(strings.TrimSpace(c.Token), " ")
	}
	id, secret = strings.TrimSpace(id), strings.TrimSpace(secret)
	if !ok || id == "" || secret == "" {
		return errors.New("Modal: the stored token is not a token id and secret (ak-...:as-...)")
	}
	secrets = append(secrets, id, secret)
	req["op"] = op
	in, err := json.Marshal(req)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "heimdall-openmodel-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	cfg := filepath.Join(dir, "modal.toml")
	if err := os.WriteFile(cfg, nil, 0o600); err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, d.python(), "-I", "-c", serveHelper)
	env := []string{"MODAL_TOKEN_ID=" + id, "MODAL_TOKEN_SECRET=" + secret, "MODAL_CONFIG_PATH=" + cfg,
		"HOME=" + dir, "PATH=" + os.Getenv("PATH"), "PYTHONUNBUFFERED=1"}
	if e := strings.TrimSpace(c.Endpoint); e != "" {
		env = append(env, "MODAL_ENVIRONMENT="+e)
	}
	for _, k := range []string{"SSL_CERT_FILE", "SSL_CERT_DIR", "HTTPS_PROXY", "https_proxy", "NO_PROXY", "no_proxy", "TMPDIR"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	cmd.Env = append(env, extraEnv...)
	cmd.Stdin = bytes.NewReader(in)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout // stderr dropped: the SDK may print request details there
	runErr := cmd.Run()
	if cctx.Err() != nil {
		return fmt.Errorf("Modal %s: %w", op, cctx.Err())
	}
	var me struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(stdout.Bytes(), &me) == nil && me.Error != "" {
		msg := scrub(me.Message, secrets)
		if me.Error == "unauthorized" {
			return fmt.Errorf("Modal %s: %w (%s)", op, ErrUnauthorized, msg)
		}
		return fmt.Errorf("Modal %s: %s", op, msg)
	}
	if runErr != nil {
		return fmt.Errorf("Modal %s: helper failed with %s (is the modal package installed? set %s)", op, d.python(), sandbox.ModalPythonEnv)
	}
	if out != nil {
		if err := json.Unmarshal(stdout.Bytes(), out); err != nil {
			return fmt.Errorf("Modal %s: decode helper answer: %w", op, err)
		}
	}
	return nil
}

func scrub(s string, secrets []string) string {
	for _, v := range secrets {
		if len(v) >= 4 {
			s = strings.ReplaceAll(s, v, "[redacted]")
		}
	}
	return s
}
