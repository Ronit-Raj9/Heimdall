package environments

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Remote coverage: tools through this bench (docs/remote-execution.md,
// docs/runner-safety.md "Published tool gate"). For a remote_http agent with
// remote.tools expose, a per-run gate container publishes the run's tool
// proxy on the bench host. The gate is the only container of the run with a
// published port. It sits on its own bridge network (for the published port)
// and joins the run's internal service network, where it reaches only the
// tool proxy; the tool proxy itself keeps no route out. Every call must carry
// the run's bearer token, minted here, kept only in memory (rc.RemoteTools)
// and handed to the launcher through redacted env. The gate refuses every
// call after the run's deadline, and Destroy removes it with the run.
const (
	remoteGatePort   = 9201
	remoteGateReadyS = 30
	// remoteGateMargin is how long past the agent's wall budget the gate
	// still answers, for the export that follows the agent.
	remoteGateMargin = 10 * time.Minute
)

// remoteGateName is the gate container's and its bridge network's name.
func remoteGateName(runID string) string { return "bench-rt-" + runID }

// remoteGateReady waits until the published gate answers GET /healthz from
// the bench host; tests replace it.
var remoteGateReady = func(ctx context.Context, addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 3 * time.Second}
	var last error
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/healthz", nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			err = fmt.Errorf("status %d", resp.StatusCode)
		}
		last = err
		if err := sleepCtxEnv(ctx, readyPoll); err != nil {
			return err
		}
	}
	return fmt.Errorf("gate at %s not ready after %s: %v", addr, timeout, last)
}

func sleepCtxEnv(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// startRemoteTools publishes the run's tool proxy to a remote agent when its
// config asks for it, and sets rc.RemoteTools. A run whose agent does not ask
// for it, or that has nothing proxied, is left alone.
func (e *dockerWorkspace) startRemoteTools(ctx context.Context, rc *schemas.RunContext, expID, runID string, handles []schemas.ServiceHandle) error {
	if !utils.RemoteToolsExposed(rc.Agent) {
		return nil
	}
	access := utils.RemoteToolsAccessFrom(runID, handles)
	if len(access.Services)+len(access.External) == 0 {
		return nil
	}
	host, port, err := utils.SplitRemoteToolsListen(e.cfg.RemoteToolsListen)
	if err != nil {
		return err
	}
	token, err := utils.NewRemoteToolsToken()
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(token))
	wall := time.Duration(rc.Agent.Budgets.WallS) * time.Second
	if wall <= 0 {
		wall = time.Hour
	}
	until := time.Now().UTC().Add(wall + remoteGateMargin).Truncate(time.Second)
	name := remoteGateName(runID)
	e.removeContainers(ctx, name)
	_, _ = e.cfg.Docker.Run(ctx, "network", "rm", name)
	publish := net.JoinHostPort(host, "")
	if port > 0 {
		publish = net.JoinHostPort(host, strconv.Itoa(port))
	}
	labels := utils.LabelArgs(expID, runID)
	steps := [][]string{
		append(append([]string{"network", "create"}, labels...), name),
		append(append([]string{"create", "--name", name}, labels...),
			"--user", "65534", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--read-only",
			"--pids-limit", "64", "--memory", "64m", "--log-driver", "json-file", "--log-opt", "max-size=1m",
			"--network", name, "-p", publish+":"+strconv.Itoa(remoteGatePort),
			"-e", "BENCH_GATE_TOKEN_SHA256="+hex.EncodeToString(sum[:]),
			"--entrypoint", "bifrost-bench", e.toolProxyImage(),
			"toolproxy", "gate", "-listen", ":"+strconv.Itoa(remoteGatePort),
			"-upstream", "http://"+ToolProxyHost+":"+strconv.Itoa(ToolProxyPort),
			"-run", runID, "-until", until.Format(time.RFC3339)),
		{"network", "connect", serviceNetwork(runID), name},
		{"start", name},
	}
	for _, args := range steps {
		if _, err := e.cfg.Docker.MustRun(ctx, args...); err != nil {
			return fmt.Errorf("remote tool gate: %w", err)
		}
	}
	res, err := e.cfg.Docker.MustRun(ctx, "port", name, strconv.Itoa(remoteGatePort)+"/tcp")
	if err != nil {
		return fmt.Errorf("remote tool gate port: %w", err)
	}
	bound, err := publishedPort(string(res.Stdout), host)
	if err != nil {
		return fmt.Errorf("remote tool gate port: %w", err)
	}
	probeHost := host
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		probeHost = "127.0.0.1"
	}
	if err := remoteGateReady(ctx, net.JoinHostPort(probeHost, strconv.Itoa(bound)), remoteGateReadyS*time.Second); err != nil {
		return fmt.Errorf("remote tool gate: %w: %s", err, e.logTail(ctx, name))
	}
	base, err := utils.RemoteToolsBaseURL(e.cfg.RemoteToolsPublicURL, host, bound)
	if err != nil {
		return err
	}
	access.BaseURL, access.Token = base, token
	if rc.Dir != "" {
		rec := schemas.RemoteToolsRecord{Listen: net.JoinHostPort(host, strconv.Itoa(bound)), BaseURL: base, Gate: name,
			Services: access.Services, External: access.External, Until: until.Format(time.RFC3339)}
		b, err := json.MarshalIndent(rec, "", "  ")
		if err == nil {
			err = os.WriteFile(filepath.Join(rc.Dir, schemas.RemoteToolsFile), b, 0o644)
		}
		if err != nil {
			return fmt.Errorf("remote tool gate: record: %w", err)
		}
	}
	rc.RemoteTools = access
	e.cfg.Logger.Info("remote tool gate published", "run", runID, "listen", net.JoinHostPort(host, strconv.Itoa(bound)), "services", len(access.Services), "external", len(access.External))
	return nil
}

// publishedPort reads the host port of `docker port` output ("127.0.0.1:49153",
// one line per binding), preferring the binding on host. It is pure.
func publishedPort(out, host string) (int, error) {
	var first int
	for _, line := range strings.Fields(out) {
		h, p, err := net.SplitHostPort(strings.TrimSpace(line))
		if err != nil {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 {
			continue
		}
		if h == host {
			return n, nil
		}
		if first == 0 {
			first = n
		}
	}
	if first == 0 {
		return 0, fmt.Errorf("no published port in %q", strings.TrimSpace(out))
	}
	return first, nil
}
