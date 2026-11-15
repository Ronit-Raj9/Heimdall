package environments

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// External APIs (schemas.ExternalAPI, docs/external-apis.md). Each one is a
// route of kind external in the tool proxy's routes.json. In replay mode the
// proxy answers from the cassette copied into its config volume and nothing
// leaves the bench. In live and record mode the proxy, and only the proxy,
// joins a second internal network (bench-xnet-<run>) where one forwarder per
// API connects to exactly that API's host and port; the agent never joins it.
const (
	toolProxyExternalCfg = toolProxyCfg + "/external" // replay cassettes, read by the proxy
	toolProxyExternalRec = "/ledger/external"         // record mode cassettes, written by the proxy
	maxCassetteBytes     = maxLedgerBytes             // a recorded cassette larger than this is not exported
	externalCassetteMode = 0o644                      // cassette files copied into the proxy
	externalDirMode      = 0o755                      // the folders holding them
	protocolExternal     = "external"                 // toolproxy.ProtocolExternal
)

// lookupEnv reads the bench's own environment; tests replace it.
var lookupEnv = os.LookupEnv

// externalAPI is one external API of a run with its resolved mode.
type externalAPI struct {
	ref      utils.ExternalRef
	mode     string
	cassette string // absolute host path of the cassette; set in replay mode
}

// externalRoute is the external part of one routes.json entry
// (toolproxy.ExternalRoute).
type externalRoute struct {
	Mode       string                  `json:"mode"`
	Allow      []schemas.ExternalAllow `json:"allow,omitempty"`
	Headers    map[string]string       `json:"headers,omitempty"`
	AuthEnv    string                  `json:"auth_env,omitempty"`
	AuthHeader string                  `json:"auth_header,omitempty"`
	AuthScheme string                  `json:"auth_scheme,omitempty"`
	Cassette   string                  `json:"cassette,omitempty"`
	RecordTo   string                  `json:"record_to,omitempty"`
	Connect    string                  `json:"connect,omitempty"`
	MaxCalls   int                     `json:"max_calls,omitempty"`
	MaxBytes   int64                   `json:"max_bytes,omitempty"`
	TimeoutS   int                     `json:"timeout_s,omitempty"`
}

// externalAPIs resolves the run's external APIs: it validates them, refuses
// an id a service already uses, resolves each mode (utils.RunExternalMode)
// and, in live and record mode, refuses an auth_env the bench's environment
// does not set. A run without external APIs returns nil.
func externalAPIs(rc *schemas.RunContext, svcs []runService) ([]externalAPI, error) {
	refs := utils.ExternalAPIs(rc.Suite, rc.Task)
	if len(refs) == 0 {
		return nil, nil
	}
	if err := utils.CheckExternalAPIs(refs); err != nil {
		return nil, err
	}
	taken := map[string]bool{ToolProxyHost: true}
	for _, s := range svcs {
		taken[s.spec.ID] = true
	}
	var out []externalAPI
	for _, r := range refs {
		if taken[r.API.ID] {
			return nil, fmt.Errorf("external api id %q is also a service id", r.API.ID)
		}
		mode, err := utils.RunExternalMode(rc, r)
		if err != nil {
			return nil, err
		}
		x := externalAPI{ref: r, mode: mode}
		switch mode {
		case schemas.ExternalReplay:
			if x.cassette, _, err = utils.ExternalCassette(r); err != nil {
				return nil, err
			}
		default:
			if r.API.AuthEnv != "" {
				if v, ok := lookupEnv(r.API.AuthEnv); !ok || v == "" {
					return nil, fmt.Errorf("external api %s runs %s but auth_env %s is not set in the bench's environment", r.API.ID, mode, r.API.AuthEnv)
				}
			}
		}
		out = append(out, x)
	}
	return out, nil
}

// externalRoutes builds the routes.json entries of the run's external APIs,
// caps filled with their defaults. It is pure.
func externalRoutes(runID string, ext []externalAPI) []toolRoute {
	var routes []toolRoute
	for _, x := range ext {
		a := x.ref.API
		er := &externalRoute{
			Mode: x.mode, Allow: a.Allow, Headers: a.Headers,
			MaxCalls: a.MaxCalls, MaxBytes: a.MaxBytes, TimeoutS: a.TimeoutS,
		}
		if er.MaxCalls == 0 {
			er.MaxCalls = utils.DefaultExternalMaxCalls
		}
		if er.MaxBytes == 0 {
			er.MaxBytes = utils.DefaultExternalMaxBytes
		}
		if er.TimeoutS == 0 {
			er.TimeoutS = utils.DefaultExternalTimeoutS
		}
		switch x.mode {
		case schemas.ExternalReplay:
			er.Cassette = path.Join(toolProxyExternalCfg, a.ID+utils.ExternalCassetteSuffix)
		default:
			er.Connect = utils.ExternalEgressAddr(runID, a.ID)
			if a.AuthEnv != "" {
				er.AuthEnv, er.AuthHeader, er.AuthScheme = a.AuthEnv, utils.ExternalAuthHeader(a), a.AuthScheme
			}
			if x.mode == schemas.ExternalRecord {
				er.RecordTo = path.Join(toolProxyExternalRec, a.ID+utils.ExternalCassetteSuffix)
			}
		}
		routes = append(routes, toolRoute{Service: a.ID, Upstream: strings.TrimRight(a.BaseURL, "/"), Protocol: protocolExternal, External: er})
	}
	return routes
}

// externalProxyEnv is the docker create flags that hand the tool proxy the
// credentials of its live and record APIs: `-e NAME` with no value, so the
// docker CLI copies the value from the bench's environment and it never
// appears on a command line, in a log or in routes.json. It is pure.
func externalProxyEnv(ext []externalAPI) []string {
	var args []string
	seen := map[string]bool{}
	for _, x := range ext {
		name := x.ref.API.AuthEnv
		if x.mode == schemas.ExternalReplay || name == "" || seen[name] {
			continue
		}
		seen[name] = true
		args = append(args, "-e", name)
	}
	return args
}

// externalLive reports whether any external API of the run reaches the real
// host. It is pure.
func externalLive(ext []externalAPI) bool {
	for _, x := range ext {
		if x.mode != schemas.ExternalReplay {
			return true
		}
	}
	return false
}

// startExternalEgress creates the run's external network and its forwarders
// for the live and record APIs. A replay-only run gets neither.
func (e *dockerWorkspace) startExternalEgress(ctx context.Context, rc *schemas.RunContext, expID, runID string, ext []externalAPI) error {
	if !externalLive(ext) {
		return nil
	}
	n := externalNetwork(runID)
	if _, err := e.cfg.Docker.Run(ctx, "network", "rm", n); err != nil {
		return fmt.Errorf("remove stale network %s: %w", n, err)
	}
	args := append([]string{"network", "create", "--internal"}, utils.LabelArgs(expID, runID)...)
	if _, err := e.cfg.Docker.MustRun(ctx, append(args, n)...); err != nil {
		return fmt.Errorf("create network %s: %w", n, err)
	}
	targets := map[string]string{}
	for _, x := range ext {
		if x.mode == schemas.ExternalReplay {
			continue
		}
		t, err := utils.ExternalTarget(x.ref.API.BaseURL)
		if err != nil {
			return fmt.Errorf("external api %s: %w", x.ref.API.ID, err)
		}
		targets[x.ref.API.ID] = t
	}
	if _, err := utils.StartExternalEgress(ctx, e.cfg.Docker, rc, e.helperImage(rc), n, targets); err != nil {
		return err
	}
	return nil
}

// prepareExternal readies the created (not started) tool proxy for its
// external APIs: replay cassettes go into its config volume, record mode
// gets a folder in its ledger volume owned by the proxy's user, and with any
// live or record API it joins the external network.
func (e *dockerWorkspace) prepareExternal(ctx context.Context, name, runID, user string, ext []externalAPI) error {
	if len(ext) == 0 {
		return nil
	}
	cassettes := map[string][]byte{}
	record := false
	for _, x := range ext {
		switch x.mode {
		case schemas.ExternalReplay:
			b, err := os.ReadFile(x.cassette)
			if err != nil {
				return fmt.Errorf("external api %s cassette: %w", x.ref.API.ID, err)
			}
			cassettes[x.ref.API.ID+utils.ExternalCassetteSuffix] = b
		case schemas.ExternalRecord:
			record = true
		}
	}
	if len(cassettes) > 0 {
		if err := e.cpTar(ctx, name, toolProxyCfg, io.NopCloser(bytes.NewReader(externalTar(cassettes, 0, 0)))); err != nil {
			return fmt.Errorf("copy cassettes: %w", err)
		}
	}
	if record {
		uid, gid := numericUser(user)
		if err := e.cpTar(ctx, name, "/ledger", io.NopCloser(bytes.NewReader(externalTar(nil, uid, gid)))); err != nil {
			return fmt.Errorf("prepare record folder: %w", err)
		}
	}
	if externalLive(ext) {
		if _, err := e.cfg.Docker.MustRun(ctx, "network", "connect", externalNetwork(runID), name); err != nil {
			return fmt.Errorf("connect tool proxy to %s: %w", externalNetwork(runID), err)
		}
	}
	return nil
}

// externalHandles are the handles and results of the run's external APIs:
// the agent reaches each one at the tool proxy. It is pure.
func externalHandles(runID, image string, ext []externalAPI) ([]schemas.ServiceHandle, []schemas.ServiceResult) {
	var hs []schemas.ServiceHandle
	var rs []schemas.ServiceResult
	for _, x := range ext {
		a := x.ref.API
		hs = append(hs, schemas.ServiceHandle{
			Spec:      schemas.ServiceSpec{ID: a.ID, Role: schemas.ServiceRoleExternal},
			Container: toolProxyContainer(runID),
			Host:      ToolProxyHost,
			URL:       utils.ExternalURL(a.ID),
			Upstream:  strings.TrimRight(a.BaseURL, "/"),
		})
		rs = append(rs, schemas.ServiceResult{ID: a.ID, Role: schemas.ServiceRoleExternal, Image: image, Ready: true, Proxied: true})
	}
	return hs, rs
}

// exportExternal copies every record mode cassette out of the tool proxy into
// external/<id>.cassette.jsonl in the run folder, where the seal covers it.
// A cassette that cannot be read is logged, not returned: the run itself is
// unaffected. Only a run folder that cannot be written is an error.
func (e *dockerWorkspace) exportExternal(ctx context.Context, rc *schemas.RunContext, runID string) error {
	for _, r := range utils.ExternalAPIs(rc.Suite, rc.Task) {
		mode, err := utils.RunExternalMode(rc, r)
		if err != nil || mode != schemas.ExternalRecord {
			continue
		}
		dir := filepath.Join(rc.Dir, utils.ExternalFolder)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
		out := filepath.Join(dir, r.API.ID+utils.ExternalCassetteSuffix)
		src := path.Join(toolProxyExternalRec, r.API.ID+utils.ExternalCassetteSuffix)
		cctx, cancel := context.WithTimeout(ctx, exportTimeout)
		n, problem, err := e.exportPath(cctx, toolProxyContainer(runID), src, out, maxCassetteBytes)
		cancel()
		if err != nil {
			return err
		}
		if problem != "" {
			e.cfg.Logger.Warn("recorded cassette not exported", "run", runID, "api", r.API.ID, "problem", problem)
			if rerr := os.Remove(out); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
				return fmt.Errorf("remove %s: %w", out, rerr)
			}
			continue
		}
		e.cfg.Logger.Debug("recorded cassette exported", "run", runID, "api", r.API.ID, "bytes", n)
	}
	return nil
}

// externalTar is a tar holding an external/ folder (owned by uid:gid) and
// the given files in it. It is pure.
func externalTar(files map[string][]byte, uid, gid int) []byte {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	epoch := time.Unix(0, 0)
	// Writes to a bytes.Buffer cannot fail.
	_ = tw.WriteHeader(&tar.Header{Name: utils.ExternalFolder + "/", Mode: externalDirMode, Typeflag: tar.TypeDir, Uid: uid, Gid: gid, ModTime: epoch})
	for _, n := range sortedByteKeys(files) {
		_ = tw.WriteHeader(&tar.Header{Name: utils.ExternalFolder + "/" + n, Mode: externalCassetteMode, Size: int64(len(files[n])), Typeflag: tar.TypeReg, Uid: uid, Gid: gid, ModTime: epoch})
		_, _ = tw.Write(files[n])
	}
	_ = tw.Close()
	return buf.Bytes()
}

func sortedByteKeys(m map[string][]byte) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func externalNetwork(runID string) string { return "bench-xnet-" + runID }

// toolProxyImage is the configured tool proxy image (images.toolproxy), else
// the default.
func (e *dockerWorkspace) toolProxyImage() string {
	if e.cfg.ToolProxyImage != "" {
		return e.cfg.ToolProxyImage
	}
	return DefaultToolProxyImage
}
