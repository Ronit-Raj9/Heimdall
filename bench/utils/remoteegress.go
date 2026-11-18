package utils

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Remote egress is the one route out of the evaluation network that an agent
// of adapter remote_http gets (docs/remote-execution.md). Its launcher
// container stays on the internal evaluation network like every agent; a
// per-run forwarder container joins that network and a fresh outbound bridge
// network, and forwards TCP to exactly the host and port of the agent's URL.
// TLS runs end to end between the launcher and the remote service, so the
// forwarder sees only ciphertext for an https URL.
const (
	// RemoteEgressPort is the port the forwarder listens on.
	RemoteEgressPort = 9300
	// EnvRemoteConnect tells the launcher which host:port to connect to in
	// place of the URL's host. The Host header and TLS server name stay the
	// URL's.
	EnvRemoteConnect = "BENCH_REMOTE_CONNECT"
	// RemoteEgressFile records the forwarder in the run folder.
	RemoteEgressFile = "egress.json"
)

// remoteForwarder is the forwarder's program, run with python3 -I -c. It
// takes its one destination from BENCH_EGRESS_TARGET and connects nowhere
// else.
const remoteForwarder = `import os, socket, threading
host, port = os.environ["BENCH_EGRESS_TARGET"].rsplit(":", 1)
host = host.strip("[]")
srv = socket.create_server(("0.0.0.0", 9300))
def pipe(a, b):
    try:
        while True:
            d = a.recv(65536)
            if not d:
                break
            b.sendall(d)
    except OSError:
        pass
    finally:
        for s in (a, b):
            try:
                s.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
def serve(c):
    try:
        u = socket.create_connection((host, int(port)), timeout=30)
        u.settimeout(None)
    except OSError as e:
        print("connect to %s:%s failed: %s" % (host, port, e), flush=True)
        c.close()
        return
    threading.Thread(target=pipe, args=(c, u), daemon=True).start()
    pipe(u, c)
print("forwarding :9300 to %s:%s" % (host, port), flush=True)
while True:
    c, _ = srv.accept()
    threading.Thread(target=serve, args=(c,), daemon=True).start()
`

// EgressRecord is egress.json in the run folder.
type EgressRecord struct {
	Allowed   []string  `json:"allowed"`   // the only destinations the run could reach outside the evaluation network
	Forwarder string    `json:"forwarder"` // forwarder container and outbound network name
	Joined    string    `json:"joined"`    // the agent network the forwarder joined
	Started   time.Time `json:"started"`
}

// RemoteEgress is a started forwarder.
type RemoteEgress struct {
	d      Docker
	name   string
	logger schemas.Logger
}

// RemoteEgressName is the forwarder container's and its outbound network's
// name for a run.
func RemoteEgressName(runID string) string { return "bench-eg-" + runID }

// RemoteEgressAddr is where a run's launcher reaches its forwarder.
func RemoteEgressAddr(runID string) string {
	return RemoteEgressName(runID) + ":" + strconv.Itoa(RemoteEgressPort)
}

// RemoteEgressTarget returns the host:port an http or https URL connects to.
func RemoteEgressTarget(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("remote url: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return "", fmt.Errorf("remote url %q: want http(s)://host[:port]/path with no user info", raw)
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
	}
	return net.JoinHostPort(u.Hostname(), port), nil
}

// StartRemoteEgress starts the forwarder a plan's Egress asks for, attached
// to the agent's network, and records it in the run folder. A plan without
// Egress gets nil and no forwarder. One destination per run is supported.
// The forwarder runs as nobody with every capability dropped, a read-only
// root and small limits; it carries the run's labels, so the janitor removes
// a leftover.
func StartRemoteEgress(ctx context.Context, d Docker, rc *schemas.RunContext, plan *schemas.RunPlan, network string) (*RemoteEgress, error) {
	if plan == nil || len(plan.Egress) == 0 {
		return nil, nil
	}
	if rc == nil || rc.Run == nil || rc.Run.RunID == "" {
		return nil, errors.New("remote egress: run context has no run")
	}
	if len(plan.Egress) != 1 {
		return nil, fmt.Errorf("remote egress: %d destinations; one is supported", len(plan.Egress))
	}
	target := plan.Egress[0]
	if err := checkHostPort(target); err != nil {
		return nil, fmt.Errorf("remote egress: %w", err)
	}
	if network == "" || network == "none" || network == "host" || network == "bridge" {
		return nil, fmt.Errorf("remote egress: agent network %q cannot be joined", network)
	}
	e := &RemoteEgress{d: d, name: RemoteEgressName(rc.Run.RunID), logger: rc.Logger}
	if e.logger == nil {
		e.logger = schemas.NoopLogger{}
	}
	labels := LabelArgs(rc.Run.ExpID, rc.Run.RunID)
	steps := [][]string{
		append(append([]string{"network", "create"}, labels...), e.name),
		append(append([]string{"create", "--name", e.name}, labels...),
			"--user", "65534", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--read-only",
			"--pids-limit", "64", "--memory", "64m", "--network", e.name, "-e", "BENCH_EGRESS_TARGET="+target,
			"--entrypoint", "python3", plan.Image, "-I", "-c", remoteForwarder),
		{"network", "connect", network, e.name},
		{"start", e.name},
	}
	for _, args := range steps {
		if _, err := d.MustRun(ctx, args...); err != nil {
			e.Stop(context.WithoutCancel(ctx))
			return nil, fmt.Errorf("remote egress to %s: %w", target, err)
		}
	}
	rec := EgressRecord{Allowed: []string{target}, Forwarder: e.name, Joined: network, Started: time.Now().UTC()}
	if rc.Dir != "" {
		b, err := json.MarshalIndent(rec, "", "  ")
		if err == nil {
			err = os.WriteFile(filepath.Join(rc.Dir, RemoteEgressFile), b, 0o644)
		}
		if err != nil {
			e.Stop(context.WithoutCancel(ctx))
			return nil, fmt.Errorf("remote egress: record: %w", err)
		}
	}
	return e, nil
}

// Stop removes the forwarder and its outbound network. It is safe on nil and
// after a partial start, where some of them do not exist; a removal that
// fails otherwise is logged and left to the janitor.
func (e *RemoteEgress) Stop(ctx context.Context) {
	if e == nil {
		return
	}
	for _, args := range [][]string{{"rm", "-f", e.name}, {"network", "rm", e.name}} {
		res, err := e.d.Run(ctx, args...)
		if err == nil && res != nil && res.ExitCode != 0 {
			msg := strings.TrimSpace(string(res.Stderr))
			if low := strings.ToLower(msg); !strings.Contains(low, "no such") && !strings.Contains(low, "not found") {
				err = fmt.Errorf("exit %d: %s", res.ExitCode, msg)
			}
		}
		if err != nil {
			e.logger.Warn("remove remote egress forwarder; the janitor removes it later", "name", e.name, "step", args[0], "error", err.Error())
		}
	}
}

// RemoteEgressNote is what the egress gate records for a remote_http run: the
// one destination its launcher could reach. It is "" for every other run.
func RemoteEgressNote(rc *schemas.RunContext) string {
	if rc == nil || rc.Agent == nil || rc.Agent.Remote == nil {
		return ""
	}
	target, err := RemoteEgressTarget(rc.Agent.Remote.URL)
	if err != nil {
		return "remote_http agent with an invalid url: " + err.Error()
	}
	note := "remote_http agent: outbound traffic allowed to exactly " + target +
		" through a per-run forwarder (" + RemoteEgressFile + " in the run folder); every other destination stays blocked"
	if RemoteToolsExposed(rc.Agent) {
		note += "; inbound, the run's tool proxy may be published to the agent through a per-run gate behind a bearer token (" + schemas.RemoteToolsFile + " in the run folder when it was)"
	}
	return note
}

func checkHostPort(hp string) error {
	host, port, err := net.SplitHostPort(hp)
	if err != nil {
		return fmt.Errorf("destination %q: %w", hp, err)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 || host == "" || strings.ContainsAny(host, " /\\") {
		return fmt.Errorf("destination %q is not host:port", hp)
	}
	return nil
}

// ExternalTrafficReason is why a remote_http agent's tokens, cost and steps
// are unknown when its model calls do not go through the gateway.
const ExternalTrafficReason = "the remote agent's model calls do not go through this gateway (remote.model_traffic external), so its tokens, cost and steps cannot be observed"

// MarkExternalTraffic marks the gateway request log of a run whose agent is
// a remote_http service with external model traffic (the default) as
// incomplete, with ExternalTrafficReason. Every metric built on the log
// (tokens, cost, steps) then reports unknown with that reason instead of
// zero. Any other run's log is left as it is.
func MarkExternalTraffic(rc *schemas.RunContext, log *schemas.RequestLog) {
	if rc != nil && rc.Agent != nil && rc.Agent.A2A != nil && log != nil { // B1: an a2a agent sent no Bifrost credential calls its models out of sight
		if rc.Agent.A2A.Credential == schemas.A2ACredentialNone {
			log.Complete = false
			log.Reason = A2ANoCredentialReason
		}
		return
	}
	if rc == nil || rc.Agent == nil || rc.Agent.Remote == nil || log == nil || rc.Agent.Remote.ModelTraffic == "gateway" {
		return
	}
	log.Complete = false
	log.Reason = ExternalTrafficReason
}
