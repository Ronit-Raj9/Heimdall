package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/gateways/bifrost"
	"github.com/maximhq/bifrost/bench/schemas"
)

// The egress probe. A sandbox with the run sandboxes' egress rule (the
// public gateway host only) tries the internet, the cloud metadata address
// and DNS, and the gateway. Isolation is shown, never assumed: the result
// is "blocked" only when every forbidden target failed and the gateway, the
// positive control, answered. The result is kept for probeTTL, since a
// runner runs the gateway preflight before every slot and a probe sandbox
// costs the organization money and a minute.

// Egress probe outcomes, the values of the manifest's egress_probe setting.
const (
	EgressBlocked = "blocked"
	EgressOpen    = "open"
	EgressNotRun  = "not_run"

	probeTTL     = 30 * time.Minute
	probeTimeout = 20 * time.Minute
)

// probeScript connects to each target in argv[1] (a JSON list) for 3
// seconds and prints one JSON object {name: "open"|"blocked"}.
const probeScript = `import json, socket, sys
out = {}
for t in json.loads(sys.argv[1]):
    try:
        if t["kind"] == "dns":
            socket.setdefaulttimeout(3)
            socket.getaddrinfo(t["host"], 443)
        else:
            socket.create_connection((t["host"], int(t["port"])), timeout=3).close()
        out[t["name"]] = "open"
    except Exception:
        out[t["name"]] = "blocked"
print(json.dumps(out))
`

type probeTarget struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"` // tcp | dns
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Control bool   `json:"-"` // must be open
}

func (d *Driver) probeTargets() []probeTarget {
	t := []probeTarget{
		{Name: "internet", Kind: "tcp", Host: "1.1.1.1", Port: 443},
		{Name: "internet-80", Kind: "tcp", Host: "8.8.8.8", Port: 53},
		{Name: "metadata", Kind: "tcp", Host: "169.254.169.254", Port: 80},
		{Name: "dns", Kind: "dns", Host: "example.com"},
	}
	if u, err := url.Parse(d.cfg.PublicGatewayURL); err == nil && u.Hostname() != "" {
		port, _ := strconv.Atoi(u.Port())
		if port == 0 {
			port = 80
			if u.Scheme == "https" {
				port = 443
			}
		}
		t = append(t, probeTarget{Name: "gateway", Kind: "tcp", Host: u.Hostname(), Port: port, Control: true})
	}
	return t
}

// EgressReport is one probe's result per target.
type EgressReport struct {
	Results map[string]string `json:"results"`
}

// RunEgressProbe creates a sandbox from the helper image with the run
// egress rule, runs the probe in it and deletes it.
func (d *Driver) RunEgressProbe(ctx context.Context) (*EgressReport, error) {
	if strings.TrimSpace(d.cfg.HelperImage) == "" {
		return nil, errors.New("no helper image with python3 to probe from (images.helper must be a registry image the platform can pull)")
	}
	eg, err := d.gatewayEgress()
	if err != nil {
		return nil, err
	}
	img, err := d.resolveImage(ctx, nil, d.cfg.HelperImage, &record{})
	if err != nil {
		return nil, err
	}
	box, err := d.cfg.Platform.Create(ctx, Spec{Name: "heimdall-egress-probe", Image: img, Egress: eg, Lifetime: 10 * time.Minute,
		Labels: map[string]string{"heimdall.role": "egress-probe", "heimdall.install": d.cfg.Install}})
	if err != nil {
		return nil, fmt.Errorf("create probe sandbox: %w", err)
	}
	defer func() { _ = d.cfg.Platform.Destroy(context.WithoutCancel(ctx), box) }()
	targets, _ := json.Marshal(d.probeTargets())
	out, err := d.run(ctx, box, 2*time.Minute, "python3", "-I", "-c", probeScript, string(targets))
	if err != nil {
		return nil, fmt.Errorf("probe: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	rep := &EgressReport{}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &rep.Results); err != nil {
		return nil, fmt.Errorf("probe printed %q", tail(out, 200))
	}
	return rep, nil
}

// EgressVerdict reads a probe report: blocked when every forbidden target
// failed and every control answered; otherwise open (a forbidden target
// answered) or not_run (no control answered, so blocked proves nothing).
func (d *Driver) EgressVerdict(rep *EgressReport) (string, string) {
	var open, deaf []string
	for _, t := range d.probeTargets() {
		got := rep.Results[t.Name]
		switch {
		case t.Control && got != "open":
			deaf = append(deaf, t.Name)
		case !t.Control && got != "blocked":
			open = append(open, t.Name)
		}
	}
	switch {
	case len(open) > 0:
		return EgressOpen, "reachable from a run sandbox: " + strings.Join(open, ", ")
	case len(deaf) > 0:
		return EgressNotRun, "the gateway (gateway.public_url) did not answer from a run sandbox, so a blocked result proves nothing"
	}
	return EgressBlocked, ""
}

// Gateway wraps the Bifrost gateway so its preflight records the sandbox
// egress probe instead of the Docker one: in a cloud sandbox the agent's
// network is the platform's egress rule, not a Docker network.
type Gateway struct {
	schemas.Gateway
	driver *Driver

	mu      sync.Mutex
	at      time.Time
	state   string
	why     string
	targets int
}

// WrapGateway returns gw with the driver's egress probe.
func WrapGateway(gw schemas.Gateway, d *Driver) *Gateway { return &Gateway{Gateway: gw, driver: d} }

// UnwrapGateway implements schemas.GatewayWrapper.
func (g *Gateway) UnwrapGateway() schemas.Gateway { return g.Gateway }

// Preflight implements schemas.Gateway: the inner preflight runs as
// before, then its Docker egress settings and problems are replaced by the
// sandbox probe.
func (g *Gateway) Preflight(ctx context.Context) (*schemas.GatewayManifest, error) {
	m, err := g.Gateway.Preflight(ctx)
	if m == nil {
		return m, err
	}
	kept := m.Problems[:0]
	for _, p := range m.Problems {
		if !strings.HasPrefix(p, "egress ") {
			kept = append(kept, p)
		}
	}
	m.Problems = kept
	if m.Settings == nil {
		m.Settings = map[string]string{}
	}
	delete(m.Settings, bifrost.SettingEgressError)
	m.Settings[bifrost.SettingEvalNetwork] = g.driver.EvalIdentity()
	state, why, n := g.probe(ctx)
	m.Settings[bifrost.SettingEgressProbe] = state
	m.Settings["egress_probe_targets"] = strconv.Itoa(n)
	if why != "" {
		m.Settings[bifrost.SettingEgressError] = why
		m.Problems = append(m.Problems, "egress ("+g.driver.Driver()+") "+state+": "+why)
	}
	return m, err
}

// probe returns the cached verdict, or runs a new probe.
func (g *Gateway) probe(ctx context.Context) (string, string, int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state == EgressBlocked && time.Since(g.at) < probeTTL {
		return g.state, g.why, g.targets
	}
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	g.targets = len(g.driver.probeTargets())
	rep, err := g.driver.RunEgressProbe(pctx)
	if err != nil {
		g.state, g.why = EgressNotRun, "egress probe not run on "+g.driver.Driver()+": "+err.Error()
	} else {
		g.state, g.why = g.driver.EgressVerdict(rep)
	}
	g.at = time.Now()
	return g.state, g.why, g.targets
}
