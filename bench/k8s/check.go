package k8s

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// minInotifyInstances is what kind documents for a host running a cluster
// ("too many open files" when a node's containerd or kubelet cannot get an
// inotify instance); the bench's own workspace observers use them too.
const minInotifyInstances = 512

// inotifyPath is where Linux exposes the per-user inotify instance limit.
var inotifyPath = "/proc/sys/fs/inotify/max_user_instances"

// Check runs `k8s check`: the host limit a local cluster needs, API
// reachability and version, the driver's permissions, the gateway Service,
// the images on the nodes and NetworkPolicy enforcement, shown by a probe
// pod with and without the run policies. images are the tags the
// experiments will run (the helper image is always checked).
func (d *Driver) Check(ctx context.Context, images []string) []schemas.DoctorCheck {
	var out []schemas.DoctorCheck
	add := func(id string, st schemas.DoctorStatus, format string, args ...any) {
		out = append(out, schemas.DoctorCheck{ID: id, Status: st, Detail: fmt.Sprintf(format, args...)})
	}
	if row, ok := inotifyRow(d.cfg.Client.Server); ok {
		out = append(out, row)
	}
	v, err := d.cfg.Client.ServerVersion(ctx)
	if err != nil {
		add("api server", schemas.DoctorFail, "%s (context %s): %v", d.cfg.Client.Server, d.cfg.Client.Context, err)
		return out
	}
	add("api server", schemas.DoctorPass, "%s %s (context %s)", d.cfg.Client.Server, v.GitVersion, d.cfg.Client.Context)
	if ok, why := versionOK(v); ok {
		add("api version", schemas.DoctorPass, "%s: native sidecars and WebSocket exec with stdin close (v5.channel.k8s.io)", v.GitVersion)
	} else {
		add("api version", schemas.DoctorFail, "%s", why)
	}
	out = append(out, d.permissionRows(ctx)...)
	gwOK := false
	if d.cfg.GatewayURL == "" {
		add("gateway", schemas.DoctorWarn, "kubernetes.gateway_url is not set: networked agents cannot run; scripted controls with no network can")
	} else if gw, err := d.ResolveGateway(ctx); err != nil {
		add("gateway", schemas.DoctorFail, "%v", err)
	} else {
		gwOK = true
		add("gateway", schemas.DoctorPass, "%s -> %s (agents reach %s through /etc/hosts; egress allowed to pods %v port %s)",
			d.cfg.GatewayURL, gw.ip, d.cfg.AgentGatewayURL, gw.selector, gw.targetPort)
	}
	out = append(out, d.imageRows(ctx, images)...)
	out = append(out, d.policyRows(ctx, gwOK)...)
	return out
}

// inotifyRow reports the host's inotify instance limit when the API server
// is on this machine (a kind or minikube cluster); ok is false otherwise.
func inotifyRow(server string) (schemas.DoctorCheck, bool) {
	u, err := url.Parse(server)
	if err != nil {
		return schemas.DoctorCheck{}, false
	}
	ip := net.ParseIP(u.Hostname())
	if u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return schemas.DoctorCheck{}, false
	}
	row := schemas.DoctorCheck{ID: "host inotify"}
	b, err := os.ReadFile(inotifyPath)
	if err != nil {
		row.Status, row.Detail = schemas.DoctorWarn, "cannot read "+inotifyPath+": "+err.Error()
		return row, true
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	switch {
	case err != nil:
		row.Status, row.Detail = schemas.DoctorWarn, "cannot parse "+inotifyPath
	case n < minInotifyInstances:
		row.Status = schemas.DoctorWarn
		row.Detail = fmt.Sprintf("fs.inotify.max_user_instances is %d: a local cluster's containerd and kubelet fail with \"too many open files\" when the host runs out (kind's known issue); raise it with `sudo sysctl -w fs.inotify.max_user_instances=%d`", n, 1024)
	default:
		row.Status, row.Detail = schemas.DoctorPass, fmt.Sprintf("fs.inotify.max_user_instances is %d", n)
	}
	return row, true
}

// versionOK requires Kubernetes 1.30: native sidecars (1.29) and the v5
// WebSocket protocol that can close stdin (1.30). It is pure.
func versionOK(v *Version) (bool, string) {
	major, err1 := strconv.Atoi(strings.TrimRight(v.Major, "+"))
	minor, err2 := strconv.Atoi(strings.TrimRight(v.Minor, "+"))
	if err1 != nil || err2 != nil {
		return false, fmt.Sprintf("cannot read the server version %q.%q", v.Major, v.Minor)
	}
	if major > 1 || (major == 1 && minor >= 30) {
		return true, ""
	}
	return false, fmt.Sprintf("Kubernetes %s is older than 1.30: the driver needs native sidecars and stdin close over WebSocket exec", v.GitVersion)
}

// permissionRows asks the API server for every verb the driver uses.
func (d *Driver) permissionRows(ctx context.Context) []schemas.DoctorCheck {
	type need struct{ verb, group, resource, sub string }
	needs := []need{
		{"create", "", "namespaces", ""}, {"delete", "", "namespaces", ""},
		{"create", "", "pods", ""}, {"get", "", "pods", ""}, {"create", "", "pods", "exec"}, {"create", "", "pods", "attach"},
		{"get", "", "pods", "exec"}, {"get", "", "pods", "attach"}, // 2-D-17: the WebSocket exec and attach upgrade an HTTP GET
		{"get", "", "pods", "log"}, // QC2-10-03: the egress probe reads its pod's log
		{"create", "", "secrets", ""}, {"create", "", "configmaps", ""}, {"delete", "", "configmaps", ""},
		{"create", "networking.k8s.io", "networkpolicies", ""}, {"list", "", "events", ""}, {"list", "", "nodes", ""},
	}
	if ownServiceAccount(d.cfg.ServiceAccount) { // 2-D-05: created in every run namespace
		needs = append(needs, need{"create", "", "serviceaccounts", ""})
	}
	ns := NamespaceFor(d.cfg.NamespacePrefix, "check")
	var denied []string
	for _, n := range needs {
		scope := ns
		if n.resource == "namespaces" || n.resource == "nodes" {
			scope = ""
		}
		ok, _, err := d.cfg.Client.CanI(ctx, n.verb, n.group, n.resource, n.sub, scope)
		if err != nil {
			return []schemas.DoctorCheck{{ID: "permissions", Status: schemas.DoctorWarn, Detail: "cannot ask the API server (SelfSubjectAccessReview): " + err.Error()}}
		}
		if !ok {
			name := n.resource
			if n.sub != "" {
				name += "/" + n.sub
			}
			denied = append(denied, n.verb+" "+name)
		}
	}
	if len(denied) > 0 {
		return []schemas.DoctorCheck{{ID: "permissions", Status: schemas.DoctorFail, Detail: "denied: " + strings.Join(denied, ", ") + " (deploy/k8s/rbac.yaml lists what the driver needs)"}}
	}
	return []schemas.DoctorCheck{{ID: "permissions", Status: schemas.DoctorPass, Detail: fmt.Sprintf("all %d verbs the driver uses are allowed", len(needs))}}
}

// imageRows reports, per image, whether every schedulable node has it.
// With pull policy Never a missing image fails; otherwise it warns, since
// the node may pull it.
func (d *Driver) imageRows(ctx context.Context, images []string) []schemas.DoctorCheck {
	var nodes struct {
		Items []Node `json:"items"`
	}
	if err := d.cfg.Client.Get(ctx, "/api/v1/nodes", &nodes); err != nil {
		return []schemas.DoctorCheck{{ID: "images", Status: schemas.DoctorWarn, Detail: "cannot list nodes: " + err.Error()}}
	}
	if len(nodes.Items) == 0 {
		return []schemas.DoctorCheck{{ID: "images", Status: schemas.DoctorFail, Detail: "the cluster lists no nodes: no pod can run"}}
	}
	want := map[string]bool{d.cfg.HelperImage: true}
	for _, i := range images {
		if i != "" {
			want[i] = true
		}
	}
	tags := make([]string, 0, len(want))
	for t := range want {
		if t != "" {
			tags = append(tags, t)
		}
	}
	sort.Strings(tags)
	var out []schemas.DoctorCheck
	for _, tag := range tags {
		var missing []string
		for _, n := range nodes.Items {
			if !nodeHasImage(n, tag) {
				missing = append(missing, n.Metadata.Name)
			}
		}
		row := schemas.DoctorCheck{ID: "image " + tag}
		switch {
		case len(missing) == 0:
			row.Status, row.Detail = schemas.DoctorPass, fmt.Sprintf("on all %d nodes", len(nodes.Items))
		case d.cfg.ImagePullPolicy == "Never":
			row.Status, row.Detail = schemas.DoctorFail, "missing on "+strings.Join(missing, ", ")+" and image_pull_policy is Never: `bifrost-bench k8s images load` (kind) or push it to a registry"
		default:
			row.Status, row.Detail = schemas.DoctorWarn, "missing on "+strings.Join(missing, ", ")+"; the node will try to pull it (`bifrost-bench k8s images load` for a kind cluster)"
		}
		out = append(out, row)
	}
	return out
}

// nodeHasImage reports whether a node lists tag, with or without the
// docker.io and library prefixes containerd adds. It is pure.
func nodeHasImage(n Node, tag string) bool {
	for _, want := range imageNames(tag) {
		for _, img := range n.Status.Images {
			for _, name := range img.Names {
				if name == want {
					return true
				}
			}
		}
	}
	return false
}

// imageNames are the names containerd may list for a docker reference.
func imageNames(ref string) []string {
	out := []string{ref}
	first, _, _ := strings.Cut(ref, "/")
	if !strings.Contains(ref, "/") {
		out = append(out, "docker.io/library/"+ref)
	} else if !strings.ContainsAny(first, ".:") && first != "localhost" {
		out = append(out, "docker.io/"+ref)
	}
	return out
}

// policyRows shows NetworkPolicy enforcement: a control pod without the
// run policies, then one with them. Enforcement is shown when a target the
// control reached is blocked under the policies; the gateway must stay
// reachable through them.
func (d *Driver) policyRows(ctx context.Context, gateway bool) []schemas.DoctorCheck {
	pctx, cancel := context.WithTimeout(ctx, 6*time.Minute)
	defer cancel()
	forbidden := d.forbiddenTargets(pctx)
	control, err := d.RunEgressProbe(pctx, false, forbidden)
	if err != nil {
		return []schemas.DoctorCheck{{ID: "network policy", Status: schemas.DoctorFail, Detail: "control probe not run: " + err.Error()}}
	}
	saved := d.cfg.GatewayURL
	if !gateway {
		d.cfg.GatewayURL = ""
	}
	policed, err := d.RunEgressProbe(pctx, true, forbidden)
	d.cfg.GatewayURL = saved
	if err != nil {
		return []schemas.DoctorCheck{{ID: "network policy", Status: schemas.DoctorFail, Detail: "policy probe not run: " + err.Error()}}
	}
	state, why := EgressVerdict(policed, forbidden, gateway)
	var reached, nowBlocked []string
	for _, t := range forbidden {
		if t.Inform {
			continue
		}
		if control.Results[t.Name] == "open" {
			reached = append(reached, t.Name)
			if policed.Results[t.Name] == "blocked" {
				nowBlocked = append(nowBlocked, t.Name)
			}
		}
	}
	row := schemas.DoctorCheck{ID: "network policy"}
	switch {
	case state == EgressOpen:
		row.Status = schemas.DoctorFail
		row.Detail = "NOT enforced: " + why + "; the cluster's network plugin ignores NetworkPolicy (install an enforcing one: docs/kubernetes.md)"
	case state == EgressNotRun:
		row.Status, row.Detail = schemas.DoctorFail, why
	case len(nowBlocked) == 0:
		row.Status = schemas.DoctorWarn
		row.Detail = "every forbidden target is blocked, but the control pod without policies reached none of them either, so enforcement is not shown"
	default:
		row.Status = schemas.DoctorPass
		row.Detail = fmt.Sprintf("enforced: without policies a pod reached %s; with the run policies all of them are blocked", strings.Join(reached, ", "))
		if gateway {
			row.Detail += " and the gateway answers"
		}
	}
	rows := []schemas.DoctorCheck{row}
	if note := informNote(policed, forbidden); note != "" {
		rows = append(rows, schemas.DoctorCheck{ID: "api server reach", Status: schemas.DoctorWarn, Detail: note})
	}
	return rows
}
