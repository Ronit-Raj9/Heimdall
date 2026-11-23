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
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/registrycreds"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
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
	// registry is the org's own image registry login (fix wave 3, N216):
	// Modal pulls an image of that registry with it.
	registry *ImageRegistry
}

// UseRegistry makes Create pass the org's registry login to Modal for every
// image of that registry, so Modal can pull Heimdall's images from it.
func (m *Modal) UseRegistry(r ImageRegistry) {
	m.registry = &r
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
		msg := scrub(me.Message, m.hidden())
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
			scrub(runErr.Error(), m.hidden()), m.python, ModalPythonEnv, scrub(tail(stdout.Bytes(), 300), m.hidden()))
	}
	if out != nil {
		if err := json.Unmarshal(stdout.Bytes(), out); err != nil {
			return fmt.Errorf("Modal %s: decode helper answer: %w", op, err)
		}
	}
	return nil
}

// hidden lists the secrets an answer must never carry.
func (m *Modal) hidden() []string {
	out := []string{m.id, m.secret}
	if m.registry != nil && m.registry.Token != "" {
		out = append(out, m.registry.Token)
	}
	return out
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
	if s.GPU != "" {
		in["gpu"] = s.GPU // Z05: GPU sandboxes run on gVisor, Modal's default runtime
	}
	if m.registry != nil && m.registry.Holds(s.Image) {
		in["registry_auth"] = map[string]string{"username": m.registry.Username, "password": m.registry.Token}
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

// ImageRegistry is an organization's own image registry login (fix wave 3,
// N216 fix 2): Registry is the path images go under (ghcr.io/team/heimdall),
// Token a token with push and pull rights. A sandbox platform in the cloud
// cannot pull images that exist only on Heimdall's machines, so they are
// pushed there first and pulled from there with the same login.
type ImageRegistry struct {
	Registry string `json:"registry"`
	Username string `json:"username"`
	Token    string `json:"token,omitempty"`
}

// Host is the registry's host name (with its port, if any).
func (r ImageRegistry) Host() string {
	h, _, _ := strings.Cut(r.Registry, "/")
	return h
}

// Holds reports whether image lives under the registry's path.
func (r ImageRegistry) Holds(image string) bool {
	return r.Registry != "" && strings.HasPrefix(image, r.Registry+"/")
}

// DockerRunner runs the docker CLI (utils.Docker under the org's Docker
// policy on the shared runner).
type DockerRunner interface {
	MustRunEnv(ctx context.Context, env []string, args ...string) (*utils.DockerResult, error)
}

// RegistryImages pushes local images to an org's own registry so a sandbox
// platform can pull them (DriverConfig.RemoteImage). Each local image is
// tagged <registry>/<repo>:<first 12 hex of its image id>, pushed with the
// org's login through a temporary Docker config, and returned as
// <registry>/<repo>@<digest>. A pushed image is remembered by its local
// image id, so it is pushed once per process.
type RegistryImages struct {
	Login  ImageRegistry
	Docker DockerRunner
	// HostCheck is checked before every push with the registry's host name
	// (no port); an error refuses the push. nil uses PublicRegistryHost, the
	// guard the login was saved under.
	HostCheck func(ctx context.Context, host string) error

	mu    sync.Mutex
	cache map[string]string // local image id -> remote reference
}

var (
	pushDigestRe = regexp.MustCompile(`digest: (sha256:[0-9a-f]{64})`)
	repoBadRe    = regexp.MustCompile(`[^a-z0-9._-]+`)
	repoSepRe    = regexp.MustCompile(`[._-]{2,}`)
)

// remoteRepo is the repository name of a local image under the org's
// registry: its path without tag or digest, with "/" as "-", lower case.
func remoteRepo(ref string) string {
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		ref = ref[:i]
	}
	repo := repoBadRe.ReplaceAllString(strings.ToLower(strings.ReplaceAll(ref, "/", "-")), "-")
	repo = strings.Trim(repoSepRe.ReplaceAllString(repo, "-"), "._-")
	if repo == "" {
		repo = "image"
	}
	return repo
}

// Remote implements DriverConfig.RemoteImage.
func (r *RegistryImages) Remote(ctx context.Context, ref string) (string, error) {
	if r.Docker == nil || r.Login.Registry == "" {
		return "", errors.New(modalRegistryNeeded)
	}
	res, err := r.Docker.MustRunEnv(ctx, nil, "image", "inspect", "--format", "{{.Id}}", ref)
	if err != nil {
		return "", fmt.Errorf("image %s: %w", ref, err)
	}
	id := strings.TrimSpace(string(res.Stdout))
	hexID := strings.TrimPrefix(id, "sha256:")
	if len(hexID) < 12 {
		return "", fmt.Errorf("image %s: unreadable image id %q", ref, id)
	}
	r.mu.Lock()
	if out, ok := r.cache[id]; ok {
		r.mu.Unlock()
		return out, nil
	}
	r.mu.Unlock()
	// The login was checked against public addresses when it was saved, but
	// the name may resolve elsewhere now: check again before every push, so
	// the worker's Docker daemon never pushes to an internal address.
	check := r.HostCheck
	if check == nil {
		check = PublicRegistryHost
	}
	if err := check(ctx, registryHostOf(r.Login.Registry)); err != nil {
		return "", fmt.Errorf("Heimdall could not push to %s: the address is not public.", r.Login.Registry)
	}
	repo := r.Login.Registry + "/" + remoteRepo(ref)
	target := repo + ":" + hexID[:12]
	out, err := r.push(ctx, ref, repo, target)
	if err != nil {
		return "", r.pushError(err)
	}
	r.mu.Lock()
	if r.cache == nil {
		r.cache = map[string]string{}
	}
	r.cache[id] = out
	r.mu.Unlock()
	return out, nil
}

func (r *RegistryImages) push(ctx context.Context, ref, repo, target string) (string, error) {
	if _, err := r.Docker.MustRunEnv(ctx, nil, "tag", ref, target); err != nil {
		return "", err
	}
	defer func() { _, _ = r.Docker.MustRunEnv(context.WithoutCancel(ctx), nil, "image", "rm", target) }()
	dir, err := os.MkdirTemp("", "heimdall-push-")
	if err != nil {
		return "", fmt.Errorf("temporary Docker config: %w", err)
	}
	defer os.RemoveAll(dir)
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", fmt.Errorf("temporary Docker config: %w", err)
	}
	auth := schemas.RegistryAuth{Host: r.Login.Host(), Username: r.Login.Username, Password: r.Login.Token}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), registrycreds.DockerConfig(auth), 0o600); err != nil {
		return "", fmt.Errorf("temporary Docker config: %w", err)
	}
	res, err := r.Docker.MustRunEnv(ctx, []string{"DOCKER_CONFIG=" + dir}, "push", target)
	if err != nil {
		return "", err
	}
	if m := pushDigestRe.FindSubmatch(res.Stdout); m != nil {
		return repo + "@" + string(m[1]), nil
	}
	ins, err := r.Docker.MustRunEnv(ctx, nil, "image", "inspect", "--format", "{{json .RepoDigests}}", target)
	if err != nil {
		return "", err
	}
	var digests []string
	_ = json.Unmarshal(bytes.TrimSpace(ins.Stdout), &digests)
	for _, dg := range digests {
		if strings.HasPrefix(dg, repo+"@") {
			return dg, nil
		}
	}
	return "", errors.New("the push did not report a digest")
}

// pushError is a failed push in plain words, without the token.
func (r *RegistryImages) pushError(err error) error {
	msg := strings.TrimSpace(err.Error())
	if r.Login.Token != "" {
		msg = strings.ReplaceAll(msg, r.Login.Token, "[hidden]")
	}
	return fmt.Errorf("Heimdall could not push to %s: %s.", r.Login.Registry, strings.TrimSuffix(tail([]byte(msg), 300), "."))
}

// modalRegistryNeeded is the refusal of a platform without an image registry.
const modalRegistryNeeded = "Modal needs an image registry it can pull from. Add one on the Modal card."

// registryHostOf is the host name of a registry path, without its port.
func registryHostOf(registry string) string {
	host := ImageRegistry{Registry: registry}.Host()
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

// PublicRegistryHost refuses a registry host that is not a public internet
// address, with the guard the registry sign-in uses when a login is saved:
// the name rules of utils.CheckPublicHost, then every address the public
// resolver returns for it must be public (utils.BlockedIP).
func PublicRegistryHost(ctx context.Context, host string) error {
	if host == "docker.io" {
		host = "registry-1.docker.io"
	}
	if err := utils.CheckPublicHost(host); err != nil {
		return err
	}
	if net.ParseIP(host) != nil {
		return nil // a literal address: CheckPublicHost checked it
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	addrs, err := utils.PublicResolver().LookupIPAddr(cctx, host)
	if err != nil {
		return fmt.Errorf("%s could not be resolved", host)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("%s has no address", host)
	}
	for _, a := range addrs {
		if utils.BlockedIP(a.IP) {
			return utils.ErrBlockedAddress
		}
	}
	return nil
}
