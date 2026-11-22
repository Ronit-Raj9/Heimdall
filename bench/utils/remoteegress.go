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

// TCPForwarderProgram is remoteForwarder for other one-destination
// forwarders: run with python3 -I -c, it listens on RemoteEgressPort and
// forwards every connection to BENCH_EGRESS_TARGET only.
func TCPForwarderProgram() string { return remoteForwarder }

// EgressRecord is egress.json in the run folder.
type EgressRecord struct {
	Allowed   []string  `json:"allowed"`   // the only destinations the run could reach outside the evaluation network
	Forwarder string    `json:"forwarder"` // forwarder container and outbound network name
	Joined    string    `json:"joined"`    // the agent network the forwarder joined
	Started   time.Time `json:"started"`
}

// RemoteEgress is a started forwarder, or two.
type RemoteEgress struct {
	d      Docker
	names  []string
	logger schemas.Logger
}

// MaxRemoteEgress is how many destinations a plan may ask for: the agent's
// own endpoint and, for a remote coding agent (Y11 repo in, patch out),
// Heimdall's pull endpoint that keeps the workspace tarball.
const MaxRemoteEgress = 2

// RemoteEgressName is the forwarder container's and its outbound network's
// name for a run.
func RemoteEgressName(runID string) string { return "bench-eg-" + runID }

// remoteEgressNameN is the name of the run's forwarder for destination i.
func remoteEgressNameN(runID string, i int) string {
	if i == 0 {
		return RemoteEgressName(runID)
	}
	return RemoteEgressName(runID) + "-" + strconv.Itoa(i+1)
}

// RemoteEgressAddr is where a run's launcher reaches its forwarder.
func RemoteEgressAddr(runID string) string {
	return RemoteEgressName(runID) + ":" + strconv.Itoa(RemoteEgressPort)
}

// RemoteEgressAddrN is where a run's launcher reaches the forwarder of the
// plan's destination i (0 is RemoteEgressAddr).
func RemoteEgressAddrN(runID string, i int) string {
	return remoteEgressNameN(runID, i) + ":" + strconv.Itoa(RemoteEgressPort)
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

// StartRemoteEgress starts the forwarders a plan's Egress asks for, one per
// destination (at most MaxRemoteEgress), attached to the agent's network,
// and records them in the run folder. A plan without Egress gets nil and no
// forwarder. Each forwarder runs as nobody with every capability dropped, a
// read-only root and small limits, and forwards to its one destination only;
// it carries the run's labels, so the janitor removes a leftover.
func StartRemoteEgress(ctx context.Context, d Docker, rc *schemas.RunContext, plan *schemas.RunPlan, network string) (*RemoteEgress, error) {
	if plan == nil || len(plan.Egress) == 0 {
		return nil, nil
	}
	if rc == nil || rc.Run == nil || rc.Run.RunID == "" {
		return nil, errors.New("remote egress: run context has no run")
	}
	if len(plan.Egress) > MaxRemoteEgress {
		return nil, fmt.Errorf("remote egress: %d destinations; at most %d are supported", len(plan.Egress), MaxRemoteEgress)
	}
	for _, target := range plan.Egress {
		if err := checkHostPort(target); err != nil {
			return nil, fmt.Errorf("remote egress: %w", err)
		}
	}
	if network == "" || network == "none" || network == "host" || network == "bridge" {
		return nil, fmt.Errorf("remote egress: agent network %q cannot be joined", network)
	}
	e := &RemoteEgress{d: d, logger: rc.Logger}
	if e.logger == nil {
		e.logger = schemas.NoopLogger{}
	}
	labels := LabelArgs(rc.Run.ExpID, rc.Run.RunID)
	for i, target := range plan.Egress {
		name := remoteEgressNameN(rc.Run.RunID, i)
		e.names = append(e.names, name)
		steps := [][]string{
			append(append([]string{"network", "create"}, labels...), name),
			append(append([]string{"create", "--name", name}, labels...),
				"--user", "65534", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--read-only",
				"--pids-limit", "64", "--memory", "64m", "--network", name, "-e", "BENCH_EGRESS_TARGET="+target,
				"--entrypoint", "python3", plan.Image, "-I", "-c", remoteForwarder),
			{"network", "connect", network, name},
			{"start", name},
		}
		for _, args := range steps {
			if _, err := d.MustRun(ctx, args...); err != nil {
				e.Stop(context.WithoutCancel(ctx))
				return nil, fmt.Errorf("remote egress to %s: %w", target, err)
			}
		}
	}
	rec := EgressRecord{Allowed: append([]string(nil), plan.Egress...), Forwarder: strings.Join(e.names, ","), Joined: network, Started: time.Now().UTC()}
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

// Stop removes the forwarders and their outbound networks. It is safe on nil
// and after a partial start, where some of them do not exist; a removal that
// fails otherwise is logged and left to the janitor.
func (e *RemoteEgress) Stop(ctx context.Context) {
	if e == nil {
		return
	}
	for _, name := range e.names {
		for _, args := range [][]string{{"rm", "-f", name}, {"network", "rm", name}} {
			res, err := e.d.Run(ctx, args...)
			if err == nil && res != nil && res.ExitCode != 0 {
				msg := strings.TrimSpace(string(res.Stderr))
				if low := strings.ToLower(msg); !strings.Contains(low, "no such") && !strings.Contains(low, "not found") {
					err = fmt.Errorf("exit %d: %s", res.ExitCode, msg)
				}
			}
			if err != nil {
				e.logger.Warn("remove remote egress forwarder; the janitor removes it later", "name", name, "step", args[0], "error", err.Error())
			}
		}
	}
}

// RemoteEgressNote is what the egress gate records for a remote_http run: the
// one destination its launcher could reach. It is "" for every other run.
func RemoteEgressNote(rc *schemas.RunContext) string {
	if rc != nil && IsPullAgent(rc.Agent) { // Y11
		return "pull agent: outbound traffic allowed only to Heimdall's pull endpoint through a per-run forwarder (" + RemoteEgressFile + " in the run folder); every other destination stays blocked"
	}
	if rc == nil || rc.Agent == nil || rc.Agent.Remote == nil {
		return ""
	}
	target, err := RemoteEgressTarget(rc.Agent.Remote.URL)
	if err != nil {
		return "remote_http agent with an invalid url: " + err.Error()
	}
	note := "remote_http agent: outbound traffic allowed to exactly " + target +
		" through a per-run forwarder (" + RemoteEgressFile + " in the run folder); every other destination stays blocked"
	if rc.Agent.Remote.Repo == schemas.RepoTarball {
		note += "; for coding tasks also to Heimdall's pull endpoint, which keeps the workspace tarball the agent downloads"
	}
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

// OpenAIExternalReason is why an openai_agent run's tokens, cost and steps
// from the gateway are unknown: the endpoint's model calls are its own.
const OpenAIExternalReason = "the agent is an OpenAI-compatible endpoint whose model calls do not go through this gateway, so gateway tokens, cost and steps cannot be observed (the endpoint's own reported usage is in the agent trace)"

// PullExternalReason is why a pull agent's run has no gateway tokens, cost
// or steps: the customer's process calls its models itself.
const PullExternalReason = "the agent runs in your own process (pull mode) and calls its models itself, so gateway tokens, cost and steps cannot be observed (the usage it reports is in the agent trace)"

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
	if rc != nil && IsPullAgent(rc.Agent) && log != nil { // Y11: a pull agent's process calls its models out of sight
		log.Complete = false
		log.Reason = PullExternalReason
		return
	}
	if rc != nil && rc.Agent != nil && rc.Agent.OpenAI != nil && log != nil { // A08: an openai_agent endpoint calls its models out of sight
		log.Complete = false
		log.Reason = OpenAIExternalReason
		return
	}
	if rc == nil || rc.Agent == nil || rc.Agent.Remote == nil || log == nil || rc.Agent.Remote.ModelTraffic == "gateway" {
		return
	}
	log.Complete = false
	log.Reason = ExternalTrafficReason
}
