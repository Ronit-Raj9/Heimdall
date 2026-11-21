package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/gateways/bifrost"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// The egress probe (docs/kubernetes.md, "Network isolation"). A pod with the
// run pods' network settings (the same deny-all and gateway policies, no
// DNS server, the gateway as an /etc/hosts entry) tries the internet, the
// cloud metadata address, the cluster's DNS and API server, and the
// gateway. Isolation is shown, never assumed: the result is "blocked" only
// when every forbidden target failed and the gateway, the positive control,
// answered; with no gateway configured the positive control is the API
// server reached from a namespace without policies.

// Egress probe outcomes, the values of the manifest's egress_probe setting.
const (
	EgressBlocked = "blocked"
	EgressOpen    = "open"
	EgressNotRun  = "not_run"
)

// probeScript connects to each target in argv[1] (a JSON list) for 3
// seconds and prints "<name> open|blocked" per target. With argv[2] it
// first waits up to that many seconds for the internet to become
// unreachable: a network plugin programs a new pod's policy a moment after
// the pod starts (kindnet adds the pod's address to its enforced set), and
// the agent of a run pod is never released before that (waitIsolated). A "udp" target is a
// DNS query for example.com that must get an answer to count as open; "dns"
// resolves example.com through the pod's resolver.
const probeScript = `import json, socket, sys, struct, time
settle = float(sys.argv[2]) if len(sys.argv) > 2 else 0
deadline = time.time() + settle
while time.time() < deadline:
    try:
        socket.create_connection(("1.1.1.1", 443), timeout=1).close()
        time.sleep(1)
    except Exception:
        break
for t in json.loads(sys.argv[1]):
    name, kind = t["name"], t["kind"]
    ok = False
    try:
        if kind == "dns":
            socket.setdefaulttimeout(3)
            socket.getaddrinfo("example.com", 443)
            ok = True
        elif kind == "udp":
            s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
            s.settimeout(3)
            q = struct.pack(">HHHHHH", 0x4242, 0x0100, 1, 0, 0, 0) + b"\x07example\x03com\x00" + struct.pack(">HH", 1, 1)
            s.sendto(q, (t["host"], t["port"]))
            s.recvfrom(512)
            ok = True
        else:
            s = socket.create_connection((t["host"], t["port"]), timeout=3)
            s.close()
            ok = True
    except Exception:
        ok = False
    print("%s %s" % (name, "open" if ok else "blocked"), flush=True)
`

// probeTarget is one connection the probe pod tries.
type probeTarget struct {
	Name string `json:"name"`
	Kind string `json:"kind"` // tcp | udp | dns
	Host string `json:"host,omitempty"`
	Port int    `json:"port,omitempty"`
	// Inform marks a target whose reachability is reported, not held
	// against isolation: the API server, which on a cluster that hosts it
	// on the nodes (kind, kubeadm) is the pod's own node, and NetworkPolicy
	// implementations let a pod reach its node. Run pods carry no token.
	Inform bool `json:"-"`
}

// EgressReport is one probe pod's answers, target name to open or blocked.
type EgressReport struct {
	Namespace string            `json:"namespace"`
	Policy    bool              `json:"policy"` // the run network policies applied
	Results   map[string]string `json:"results"`
}

// forbiddenTargets are what a run pod must never reach.
func (d *Driver) forbiddenTargets(ctx context.Context) []probeTarget {
	t := []probeTarget{
		{Name: "dns", Kind: "dns"},
		{Name: "tcp:1.1.1.1:443", Kind: "tcp", Host: "1.1.1.1", Port: 443},
		{Name: "tcp:169.254.169.254:80", Kind: "tcp", Host: "169.254.169.254", Port: 80},
	}
	var s Service
	if err := d.cfg.Client.Get(ctx, corePath("kube-system", "services")+"/kube-dns", &s); err == nil && s.Spec.ClusterIP != "" && s.Spec.ClusterIP != "None" {
		t = append(t, probeTarget{Name: "udp:cluster-dns:53", Kind: "udp", Host: s.Spec.ClusterIP, Port: 53},
			probeTarget{Name: "tcp:cluster-dns:53", Kind: "tcp", Host: s.Spec.ClusterIP, Port: 53})
	}
	if ip := d.apiServerIP(ctx); ip != "" {
		t = append(t, probeTarget{Name: "tcp:api-server:443", Kind: "tcp", Host: ip, Port: 443, Inform: true})
	}
	return t
}

// apiServerIP is the cluster IP of the default/kubernetes Service.
func (d *Driver) apiServerIP(ctx context.Context) string {
	var s Service
	if err := d.cfg.Client.Get(ctx, corePath("default", "services")+"/kubernetes", &s); err == nil {
		return s.Spec.ClusterIP
	}
	return ""
}

// RunEgressProbe runs one probe pod in a fresh namespace, with the run
// policies when policy is true (and the gateway reachable through them when
// one is configured), and returns its answers. The namespace is deleted
// afterwards.
func (d *Driver) RunEgressProbe(ctx context.Context, policy bool, targets []probeTarget) (*EgressReport, error) {
	id := strings.ToLower(strings.TrimPrefix(utils.NewID("c", 6), "c_"))
	ns := NamespaceFor(d.cfg.NamespacePrefix, "check-"+id)
	labels := map[string]string{LabelBench: "1", LabelRole: RoleCheck}
	rep := &EgressReport{Namespace: ns, Policy: policy, Results: map[string]string{}}
	var owner []OwnerReference
	if d.poolMode() {
		// Pool mode: lease a pool namespace like a run does, and release it
		// (anchor and probe objects, never the namespace) afterwards.
		leaseID := "check-" + id
		pns, o, err := d.leasePool(ctx, leaseID, labels, map[string]string{"run_id": leaseID, "driver_version": DriverVersion})
		if err != nil {
			return nil, fmt.Errorf("probe namespace: %w", err)
		}
		ns, owner, rep.Namespace = pns, o, pns
		defer func() {
			rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), poolGoneTimeout)
			defer cancel()
			if err := d.releasePool(rctx, ns, "", owner[0].UID); err != nil {
				d.cfg.Logger.Warn("release egress probe pool namespace", "namespace", ns, "error", err.Error())
			}
			d.forget(leaseID)
		}()
	} else {
		if err := d.cfg.Client.Create(ctx, "/api/v1/namespaces", Namespace{APIVersion: "v1", Kind: "Namespace", Metadata: ObjectMeta{Name: ns, Labels: d.ownedLabels(maps.Clone(labels))}}, nil); err != nil {
			return nil, fmt.Errorf("probe namespace: %w", err)
		}
		defer func() {
			if err := d.cfg.Client.Delete(context.WithoutCancel(ctx), nsPath(ns)); err != nil {
				d.cfg.Logger.Warn("remove egress probe namespace", "namespace", ns, "error", err.Error())
			}
		}()
	}
	if err := d.createServiceAccount(ctx, ns, labels, owner); err != nil {
		return nil, fmt.Errorf("probe %w", err)
	}
	var hosts []HostAlias
	if policy {
		if err := d.cfg.Client.Create(ctx, netpolPath(ns), denyAllPolicy(labels, owner), nil); err != nil {
			return nil, fmt.Errorf("probe deny-all policy: %w", err)
		}
		if d.cfg.GatewayURL != "" {
			gw, err := d.ResolveGateway(ctx)
			if err != nil {
				return nil, err
			}
			if err := d.cfg.Client.Create(ctx, netpolPath(ns), gatewayPolicy(labels, owner, gw.namespace, gw.selector, gw.targetPort), nil); err != nil {
				return nil, fmt.Errorf("probe gateway policy: %w", err)
			}
			hosts = []HostAlias{{IP: gw.ip, Hostnames: []string{gw.host}}}
			au, _ := url.Parse(d.cfg.AgentGatewayURL)
			targets = append(targets, probeTarget{Name: "gateway", Kind: "tcp", Host: gw.host, Port: urlPort(au)})
		}
		// Give the network plugin a moment to program the policies; the
		// probe still fails closed if it is slower (a target that answers
		// counts as open).
		if err := sleepCtx(ctx, d.policySettle); err != nil {
			return nil, err
		}
	}
	arg, err := json.Marshal(targets)
	if err != nil {
		return nil, err
	}
	uid := int64(1000)
	pod := Pod{APIVersion: "v1", Kind: "Pod", Metadata: ObjectMeta{Name: probePodName, Labels: labels, OwnerReferences: owner},
		Spec: PodSpec{RestartPolicy: "Never", RuntimeClassName: d.cfg.RuntimeClass, AutomountServiceAccountToken: boolPtr(false), EnableServiceLinks: boolPtr(false),
			DNSPolicy: "None", DNSConfig: &PodDNSConfig{Nameservers: []string{"127.0.0.1"}}, HostAliases: hosts,
			NodeSelector: d.cfg.NodeSelector, Tolerations: d.cfg.Tolerations, ServiceAccountName: d.cfg.ServiceAccount,
			SecurityContext:               &PodSecurityContext{SeccompProfile: &SeccompProfile{Type: "RuntimeDefault"}},
			TerminationGracePeriodSeconds: int64Ptr(1), ActiveDeadlineSeconds: int64Ptr(300),
			Containers: []Container{{Name: "probe", Image: d.cfg.HelperImage, ImagePullPolicy: d.cfg.ImagePullPolicy,
				Command: []string{"python3", "-c", probeScript, string(arg), strconv.Itoa(settleFor(policy))},
				SecurityContext: &SecurityContext{RunAsUser: &uid, RunAsGroup: &uid, RunAsNonRoot: boolPtr(true), AllowPrivilegeEscalation: boolPtr(false),
					ReadOnlyRootFilesystem: boolPtr(true), Capabilities: &Capabilities{Drop: []string{"ALL"}}},
				Resources: &ResourceRequirements{Limits: map[string]string{"memory": "128Mi"}}}}}}
	if !policy {
		pod.Spec.DNSPolicy, pod.Spec.DNSConfig = "", nil
	}
	if err := d.cfg.Client.Create(ctx, corePath(ns, "pods"), pod, nil); err != nil {
		return nil, fmt.Errorf("probe pod: %w", err)
	}
	r := runRef{ns: ns}
	deadline := time.Now().Add(podStartTimeout)
	for {
		var p Pod
		err := d.cfg.Client.Get(ctx, corePath(ns, "pods")+"/egress-probe", &p)
		if err == nil && (p.Status.Phase == "Succeeded" || p.Status.Phase == "Failed") {
			break
		}
		if err == nil {
			if cs := containerStatus(&p, "probe", false); cs != nil && cs.State.Waiting != nil {
				switch cs.State.Waiting.Reason {
				case "ErrImagePull", "ImagePullBackOff", "ErrImageNeverPull", "InvalidImageName", "CreateContainerConfigError":
					return nil, d.withEvents(ctx, r, fmt.Errorf("probe pod cannot start: %s %s", cs.State.Waiting.Reason, cs.State.Waiting.Message))
				}
			}
		}
		if time.Now().After(deadline) {
			return nil, d.withEvents(ctx, r, errors.New("probe pod did not finish"))
		}
		if err := sleepCtx(ctx, pollEvery); err != nil {
			return nil, err
		}
	}
	logs, err := d.cfg.Client.Raw(ctx, corePath(ns, "pods")+"/egress-probe/log", url.Values{"container": {"probe"}}, 1<<16)
	if err != nil {
		return nil, fmt.Errorf("probe output: %w", err)
	}
	for _, line := range strings.Split(string(logs), "\n") {
		name, state, ok := strings.Cut(strings.TrimSpace(line), " ")
		if ok && (state == "open" || state == "blocked") {
			rep.Results[name] = state
		}
	}
	return rep, nil
}

// EgressVerdict reads a probe with the run policies: blocked when every
// forbidden target is blocked and the gateway (when configured) is open;
// open when a forbidden target answered; not_run otherwise, with the
// reason. It is pure.
func EgressVerdict(rep *EgressReport, forbidden []probeTarget, gateway bool) (string, string) {
	if rep == nil {
		return EgressNotRun, "no probe result"
	}
	var open, missing []string
	for _, t := range forbidden {
		if t.Inform {
			continue
		}
		switch rep.Results[t.Name] {
		case "blocked":
		case "open":
			open = append(open, t.Name)
		default:
			missing = append(missing, t.Name)
		}
	}
	sort.Strings(open)
	switch {
	case len(open) > 0:
		return EgressOpen, "reachable from a run pod: " + strings.Join(open, ", ")
	case len(missing) > 0:
		return EgressNotRun, "no probe result for " + strings.Join(missing, ", ")
	case gateway && rep.Results["gateway"] != "open":
		return EgressNotRun, "the positive control failed: the gateway was not reachable through the run policies, so a blocked result proves nothing"
	}
	return EgressBlocked, ""
}

// ControlledEgressVerdict is EgressVerdict for the policed probe, held
// against a control probe without policies: blocked stands only when the
// control reached at least one forbidden target that the policies then
// blocked. Otherwise enforcement is not shown and the verdict is not_run,
// which fails the preflight. It is pure.
func ControlledEgressVerdict(control, policed *EgressReport, forbidden []probeTarget, gateway bool) (string, string) {
	state, why := EgressVerdict(policed, forbidden, gateway)
	if state != EgressBlocked {
		return state, why
	}
	if control == nil {
		return EgressNotRun, "no control probe result, so enforcement is not shown"
	}
	for _, t := range forbidden {
		if !t.Inform && control.Results[t.Name] == "open" && policed.Results[t.Name] == "blocked" {
			return EgressBlocked, ""
		}
	}
	return EgressNotRun, "every forbidden target is blocked, but the control pod without policies reached none of them either, so enforcement is not shown"
}

// Gateway wraps the Bifrost gateway so its preflight records the
// Kubernetes egress probe instead of the Docker one: under this driver the
// agents' network is the run namespaces' policies, not a Docker network.
// Everything else passes through unchanged.
type Gateway struct {
	schemas.Gateway
	driver *Driver
}

// WrapGateway returns gw with the driver's egress probe.
func WrapGateway(gw schemas.Gateway, d *Driver) *Gateway { return &Gateway{Gateway: gw, driver: d} }

// UnwrapGateway implements schemas.GatewayWrapper, so the planner still
// finds the inner gateway's optional capabilities (prompt-cache and
// complexity probes, team and A2A support) through schemas.GatewayAs.
func (g *Gateway) UnwrapGateway() schemas.Gateway { return g.Gateway }

// Preflight implements schemas.Gateway: the inner preflight runs as
// before, then its Docker egress settings and problems are replaced by the
// probe from a pod with the run policies.
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
	pctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	forbidden := g.driver.forbiddenTargets(pctx)
	// 2-D-16: a blocked result counts only against a control pod without
	// the policies that reached a forbidden target, as in `k8s check`.
	control, cerr := g.driver.RunEgressProbe(pctx, false, forbidden)
	var rep *EgressReport
	perr := cerr
	if cerr == nil {
		rep, perr = g.driver.RunEgressProbe(pctx, true, forbidden)
	}
	state, why := EgressNotRun, ""
	if perr != nil {
		why = "egress probe not run in the cluster: " + perr.Error()
	} else {
		state, why = ControlledEgressVerdict(control, rep, forbidden, g.driver.cfg.GatewayURL != "")
	}
	m.Settings[bifrost.SettingEgressProbe] = state
	m.Settings["egress_probe_targets"] = strconv.Itoa(len(forbidden))
	if rep != nil {
		if note := informNote(rep, forbidden); note != "" {
			m.Settings["egress_probe_note"] = note
		}
	}
	if why != "" {
		m.Settings[bifrost.SettingEgressError] = why
		m.Problems = append(m.Problems, "egress (kubernetes) "+state+": "+why)
	}
	return m, err
}

// isolationSettle is how long a pod under the run policies may take before
// its egress is enforced.
const isolationSettle = 30

func settleFor(policy bool) int {
	if policy {
		return isolationSettle
	}
	return 0
}

// isolationScript runs in the run pod's bench helper, in the pod's network
// namespace, before the agent is released: it waits until the internet is
// unreachable, up to isolationSettle seconds, and exits 1 if it never is.
const isolationScript = `import socket, sys, time
deadline = time.time() + float(sys.argv[1])
while True:
    try:
        socket.create_connection(("1.1.1.1", 443), timeout=1).close()
    except Exception:
        sys.exit(0)
    if time.time() > deadline:
        sys.exit(1)
    time.sleep(1)
`

// waitIsolated holds the agent until the run pod's egress is enforced: a
// connection to the internet from the pod's network namespace must fail.
// A pod whose policies never take effect fails the run before the agent
// starts, as infrastructure, never as the agent's result.
func (d *Driver) waitIsolated(ctx context.Context, r runRef) error {
	cctx, cancel := context.WithTimeout(ctx, (isolationSettle+30)*time.Second)
	defer cancel()
	start := time.Now()
	_, err := d.cfg.Client.ExecOutput(cctx, r.ns, podName, ctrBench, []string{"python3", "-I", "-c", isolationScript, strconv.Itoa(isolationSettle)}, nil, 1<<12)
	var ee *ExitError
	if errors.As(err, &ee) && ee.Code == 1 {
		return fmt.Errorf("the run pod can reach the internet after %ds: the network plugin does not enforce the run policies (bifrost-bench k8s check)", isolationSettle)
	}
	if err != nil {
		return fmt.Errorf("egress check in the run pod: %w", err)
	}
	d.cfg.Logger.Debug("run pod isolated", "run", r.runID, "after", time.Since(start).Round(time.Millisecond).String())
	return nil
}

// informNote names the informational targets a policed probe reached.
func informNote(rep *EgressReport, targets []probeTarget) string {
	var open []string
	for _, t := range targets {
		if t.Inform && rep.Results[t.Name] == "open" {
			open = append(open, t.Name)
		}
	}
	if len(open) == 0 {
		return ""
	}
	return "reachable but not held against isolation: " + strings.Join(open, ", ") + " (the API server on the pod's own node; run pods mount no service account token)"
}
