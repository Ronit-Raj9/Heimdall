package k8s

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/agentevents"
	"github.com/maximhq/bifrost/bench/environments"
)

// Objects of one run, all in the run's own namespace and all owned by the
// anchor config map, so deleting the anchor (or the namespace) removes
// every one of them (docs/kubernetes.md, "Cleanup").
const (
	anchorName   = "run"
	podName      = "run"
	envSecret    = "agent-env"
	scriptsMap   = "observers"
	denyPolicy   = "deny-all"
	egressPolicy = "allow-gateway"

	ctrPrep      = "prep"
	ctrFSWatch   = "fswatch"
	ctrProcWatch = "procwatch"
	ctrAgent     = "agent"
	ctrBench     = "bench"
	ctrCollector = "collector"

	volWorkspace = "workspace"
	volAux       = "aux"
	volHome      = "home"
	volTmp       = "tmp"
	volObs       = "obs"
	volCtl       = "ctl"
	volScripts   = "scripts"
	volHelperTmp = "helper-tmp"
	volLedger    = "ledger"
	volCollTmp   = "collector-tmp"

	obsDir     = "/bench-obs"
	ctlDir     = "/bench-ctl"
	scriptsDir = "/bench-scripts"
	seededFile = ctlDir + "/seeded"
	ledgerDir  = "/bench-ledger"

	// collectorUID runs the agent event collector: not root (it parses what
	// the agent sends), not the agent's uid (the process observer keeps only
	// the agent's uid), not the pause container's.
	collectorUID = 65534

	// Labels on every object the driver creates.
	LabelBench = "heimdall.dev/bench"
	LabelRun   = "heimdall.dev/run"
	LabelExp   = "heimdall.dev/exp"
	LabelRole  = "heimdall.dev/role"
	RoleRun    = "run"
	RoleCheck  = "check"
	RoleGW     = "gateway"
	nsNameKey  = "kubernetes.io/metadata.name"

	// activeDeadlineMargin bounds a pod the bench lost track of: the wall
	// budget plus this, then the kubelet kills every container.
	activeDeadlineMargin = 1800
	tmpfsSize            = "512Mi"
)

// gateScript is the agent container's first process: it waits for the
// bench to attach (one line, or EOF, on stdin), so not one byte of the
// agent's output can be written before it is captured, then execs the
// agent with stdin from /dev/null, as `docker start -a` gives it.
const gateScript = `IFS= read -r _ || :; exec "$@" </dev/null`

// prepScript holds the prep init container until the bench has seeded the
// workspace through exec (seedScript), at most five minutes.
const prepScript = `i=0; while [ ! -e ` + seededFile + ` ]; do i=$((i+1)); [ "$i" -lt 3000 ] || exit 1; sleep 0.1; done`

// seedTail runs after environments.PopulateScript in the prep container: the
// agent's home and /tmp get the owner and mode Docker's tmpfs mounts have.
// It prints nothing, so the baseline commit id stays the last line. The
// prep container is released by a separate exec, after any staged probe
// files are laid down (staging.go).
const seedTail = `
chown %[1]d:%[2]d /bench-home && chmod 0700 /bench-home
chmod 1777 /bench-tmp
`

// observerCommand starts an observer whose script arrives on stdin, as in
// Docker, so the agent sees only "python3 -u - <arg>" in ps. A restarted
// sidecar (the kubelet restarts an init sidecar that exits) finds its
// output already there and sleeps instead of overwriting it.
func observerCommand(name, arg string) []string {
	out := obsDir + "/" + name
	return []string{"/bin/sh", "-c", fmt.Sprintf(`[ -e %[1]s.jsonl ] && exec sleep 2147483647; echo $$ > %[1]s.pid; exec python3 -u - %[2]s < %[3]s/%[4]s.py > %[1]s.jsonl 2> %[1]s.err`,
		out, arg, scriptsDir, name)}
}

// collectorCommand runs the agent event collector on the pod's loopback,
// with its ledger in a volume only the bench helper also mounts. A restarted
// sidecar sleeps instead of opening the ledger again. Its startup probe
// passes once it answers, or once it died (missing evidence, never a held
// agent).
func collectorCommand() []string {
	return []string{"/bin/sh", "-c", fmt.Sprintf(`[ -e /ledger/.started ] && exec sleep 2147483647; touch /ledger/.started; echo $$ > /ledger/collector.pid; exec bifrost-bench eventcollector -listen 127.0.0.1:%d -ledger /ledger/%s 2> /ledger/collector.err`,
		environments.ToolProxyPort, agentevents.LedgerName)}
}

// readyProbe passes once the observer printed its ready line, or once it
// failed with a message (python3 missing, say): a broken observer is
// missing evidence and must not hold the agent back. The records then say
// why (readObserver).
func readyProbe(name string) *Probe {
	f := obsDir + "/" + name
	return &Probe{Exec: &ExecAction{Command: []string{"/bin/sh", "-c", `grep -qE '"t": ?"ready"' ` + f + `.jsonl || [ -s ` + f + `.err ]`}},
		PeriodSeconds: 1, FailureThreshold: 60, TimeoutSeconds: 2}
}

// podInput is everything the run pod is built from. It is plain data, so the
// spec is a pure function of it (buildPod).
type podInput struct {
	RunID, ExpID    string
	AgentImage      string
	HelperImage     string
	PullPolicy      string
	Argv            []string // the agent's argv after the gate
	Workdir         string
	UID, GID        int64
	CPUs, MemGB     float64
	WallS           int
	ProcWatch       bool
	FSWatch         bool
	IntervalMS      int
	ServiceAccount  string
	NodeSelector    map[string]string
	Tolerations     []Toleration
	GatewayIP       string // "" when the plan has no network
	GatewayHost     string // the host name agents are given for the gateway
	Collector       bool   // run the agent event collector sidecar
	CollectorImage  string
	ImagePullSecret string
	mainPID         int // set at run time: the agent's main process in the pod's PID namespace
}

func runLabels(runID, expID string) map[string]string {
	l := map[string]string{LabelBench: "1", LabelRun: runID, LabelRole: RoleRun}
	if expID != "" {
		l[LabelExp] = expID
	}
	return l
}

// buildPod returns the run pod: the prep init container, the observers as
// native sidecars (started in order, each ready before the next, all before
// the agent), the agent and the trusted bench helper. It is pure.
func buildPod(in podInput, owner []OwnerReference) Pod {
	root, nonRoot := int64Ptr(0), boolPtr(true)
	noEsc, ro := boolPtr(false), boolPtr(true)
	helperSC := func(add ...string) *SecurityContext {
		return &SecurityContext{RunAsUser: root, RunAsNonRoot: boolPtr(false), AllowPrivilegeEscalation: noEsc, ReadOnlyRootFilesystem: ro,
			Privileged: boolPtr(false), Capabilities: &Capabilities{Drop: []string{"ALL"}, Add: add}}
	}
	mem := func(m string) *ResourceRequirements {
		return &ResourceRequirements{Requests: map[string]string{"memory": m}, Limits: map[string]string{"memory": m}}
	}
	vols := []Volume{
		{Name: volWorkspace, EmptyDir: &EmptyDirVolumeSource{}},
		{Name: volAux, EmptyDir: &EmptyDirVolumeSource{}},
		{Name: volHome, EmptyDir: &EmptyDirVolumeSource{Medium: "Memory", SizeLimit: tmpfsSize}},
		{Name: volTmp, EmptyDir: &EmptyDirVolumeSource{Medium: "Memory", SizeLimit: tmpfsSize}},
		{Name: volObs, EmptyDir: &EmptyDirVolumeSource{}},
		{Name: volCtl, EmptyDir: &EmptyDirVolumeSource{Medium: "Memory", SizeLimit: "1Mi"}},
		{Name: volHelperTmp, EmptyDir: &EmptyDirVolumeSource{}},
		{Name: volScripts, ConfigMap: &ConfigMapVolumeSource{Name: scriptsMap, DefaultMode: int32Ptr(0o444)}},
	}
	prep := Container{Name: ctrPrep, Image: in.HelperImage, ImagePullPolicy: in.PullPolicy, Command: []string{"/bin/sh", "-c", prepScript},
		SecurityContext: helperSC("CHOWN", "DAC_OVERRIDE", "FOWNER"), Resources: mem("2Gi"),
		VolumeMounts: []VolumeMount{{Name: volWorkspace, MountPath: "/workspace"}, {Name: volAux, MountPath: environments.AuxMount},
			{Name: volHome, MountPath: "/bench-home"}, {Name: volTmp, MountPath: "/bench-tmp"}, {Name: volCtl, MountPath: ctlDir},
			{Name: volHelperTmp, MountPath: "/tmp"}}}
	inits := []Container{prep}
	if in.FSWatch {
		inits = append(inits, Container{Name: ctrFSWatch, Image: in.HelperImage, ImagePullPolicy: in.PullPolicy, RestartPolicy: "Always",
			Command: observerCommand(ctrFSWatch, strconv.Itoa(environments.FSWatchMaxEvents)), StartupProbe: readyProbe(ctrFSWatch),
			SecurityContext: helperSC("DAC_OVERRIDE"), Resources: mem("128Mi"),
			VolumeMounts: []VolumeMount{{Name: volWorkspace, MountPath: "/w", ReadOnly: true}, {Name: volObs, MountPath: obsDir},
				{Name: volScripts, MountPath: scriptsDir, ReadOnly: true}}})
	}
	if in.ProcWatch {
		interval := in.IntervalMS
		if interval <= 0 {
			interval = environments.DefaultWatchIntervalMS
		}
		inits = append(inits, Container{Name: ctrProcWatch, Image: in.HelperImage, ImagePullPolicy: in.PullPolicy, RestartPolicy: "Always",
			Command: observerCommand(ctrProcWatch, strconv.Itoa(interval)), StartupProbe: readyProbe(ctrProcWatch),
			SecurityContext: helperSC("SYS_PTRACE"), Resources: mem("128Mi"),
			VolumeMounts: []VolumeMount{{Name: volObs, MountPath: obsDir}, {Name: volScripts, MountPath: scriptsDir, ReadOnly: true}}})
	}
	if in.Collector {
		cuid := int64(collectorUID)
		vols = append(vols, Volume{Name: volLedger, EmptyDir: &EmptyDirVolumeSource{}},
			Volume{Name: volCollTmp, EmptyDir: &EmptyDirVolumeSource{Medium: "Memory", SizeLimit: "16Mi"}})
		inits = append(inits, Container{Name: ctrCollector, Image: in.CollectorImage, ImagePullPolicy: in.PullPolicy, RestartPolicy: "Always",
			Command: collectorCommand(), StartupProbe: &Probe{Exec: &ExecAction{Command: []string{"/bin/sh", "-c",
				fmt.Sprintf(`wget -q -O /dev/null http://127.0.0.1:%d/healthz || ! kill -0 "$(cat /ledger/collector.pid)"`, environments.ToolProxyPort)}},
				PeriodSeconds: 1, FailureThreshold: 60, TimeoutSeconds: 2},
			SecurityContext: &SecurityContext{RunAsUser: &cuid, RunAsGroup: &cuid, RunAsNonRoot: nonRoot, AllowPrivilegeEscalation: noEsc,
				ReadOnlyRootFilesystem: ro, Privileged: boolPtr(false), Capabilities: &Capabilities{Drop: []string{"ALL"}}},
			Resources:    mem("128Mi"),
			VolumeMounts: []VolumeMount{{Name: volLedger, MountPath: "/ledger"}, {Name: volCollTmp, MountPath: "/tmp"}}})
	}
	agentRes := &ResourceRequirements{Requests: map[string]string{}, Limits: map[string]string{}}
	if in.CPUs > 0 {
		q := strconv.FormatFloat(in.CPUs, 'f', -1, 64)
		agentRes.Requests["cpu"], agentRes.Limits["cpu"] = q, q
	}
	if in.MemGB > 0 {
		q := strconv.FormatInt(int64(in.MemGB*(1<<30)), 10)
		agentRes.Requests["memory"], agentRes.Limits["memory"] = q, q
	}
	if len(agentRes.Limits) == 0 {
		agentRes = nil
	}
	uid, gid := in.UID, in.GID
	agent := Container{Name: ctrAgent, Image: in.AgentImage, ImagePullPolicy: in.PullPolicy,
		Command:    append([]string{"/bin/sh", "-c", gateScript, "sh"}, in.Argv...),
		WorkingDir: in.Workdir, Stdin: true, StdinOnce: true,
		EnvFrom: []EnvFromSource{{SecretRef: &LocalObjectReference{Name: envSecret}}},
		SecurityContext: &SecurityContext{RunAsUser: &uid, RunAsGroup: &gid, RunAsNonRoot: nonRoot, AllowPrivilegeEscalation: noEsc,
			ReadOnlyRootFilesystem: ro, Privileged: boolPtr(false), Capabilities: &Capabilities{Drop: []string{"ALL"}}},
		Resources: agentRes,
		VolumeMounts: []VolumeMount{{Name: volWorkspace, MountPath: "/workspace"}, {Name: volAux, MountPath: environments.AuxMount},
			{Name: volHome, MountPath: environments.AgentHome}, {Name: volTmp, MountPath: "/tmp"}}}
	bench := Container{Name: ctrBench, Image: in.HelperImage, ImagePullPolicy: in.PullPolicy, Command: []string{"sleep", "2147483647"},
		SecurityContext: helperSC("DAC_OVERRIDE", "KILL"), Resources: mem("2Gi"),
		VolumeMounts: []VolumeMount{{Name: volWorkspace, MountPath: "/workspace", ReadOnly: true}, {Name: volObs, MountPath: obsDir},
			{Name: volHelperTmp, MountPath: "/tmp"}, {Name: volAux, MountPath: "/bench-aux", ReadOnly: true}}}
	if in.Collector {
		bench.VolumeMounts = append(bench.VolumeMounts, VolumeMount{Name: volLedger, MountPath: ledgerDir, ReadOnly: true})
	}
	deadline := int64(in.WallS + activeDeadlineMargin)
	spec := PodSpec{
		RestartPolicy:                 "Never",
		ShareProcessNamespace:         boolPtr(true),
		AutomountServiceAccountToken:  boolPtr(false),
		ServiceAccountName:            in.ServiceAccount,
		EnableServiceLinks:            boolPtr(false),
		DNSPolicy:                     "None",
		DNSConfig:                     &PodDNSConfig{Nameservers: []string{"127.0.0.1"}},
		InitContainers:                inits,
		Containers:                    []Container{agent, bench},
		Volumes:                       vols,
		NodeSelector:                  in.NodeSelector,
		Tolerations:                   in.Tolerations,
		SecurityContext:               &PodSecurityContext{SeccompProfile: &SeccompProfile{Type: "RuntimeDefault"}},
		TerminationGracePeriodSeconds: int64Ptr(5),
		ActiveDeadlineSeconds:         &deadline,
	}
	if in.GatewayIP != "" && in.GatewayHost != "" {
		spec.HostAliases = []HostAlias{{IP: in.GatewayIP, Hostnames: []string{in.GatewayHost}}}
	}
	if in.Collector {
		// The collector listens on the pod's loopback; the agent reaches it
		// by the name it has under Docker.
		spec.HostAliases = append(spec.HostAliases, HostAlias{IP: "127.0.0.1", Hostnames: []string{environments.ToolProxyHost}})
	}
	return Pod{APIVersion: "v1", Kind: "Pod", Spec: spec,
		Metadata: ObjectMeta{Name: podName, Labels: runLabels(in.RunID, in.ExpID), OwnerReferences: owner}}
}

// denyAllPolicy refuses every connection into and out of every pod of the
// run namespace. It exists from the moment the namespace does, long before
// the pod, so a policy that is still being programmed fails closed.
func denyAllPolicy(labels map[string]string, owner []OwnerReference) NetworkPolicy {
	return NetworkPolicy{APIVersion: "networking.k8s.io/v1", Kind: "NetworkPolicy",
		Metadata: ObjectMeta{Name: denyPolicy, Labels: labels, OwnerReferences: owner},
		Spec:     NetworkPolicySpec{PodSelector: LabelSelector{}, PolicyTypes: []string{"Ingress", "Egress"}}}
}

// gatewayPolicy allows egress from the run's pods to the gateway's pods on
// one port, and nothing else: no DNS (the gateway name is an /etc/hosts
// entry), no other namespace, no internet.
func gatewayPolicy(labels map[string]string, owner []OwnerReference, gwNamespace string, gwPods map[string]string, port int) NetworkPolicy {
	return NetworkPolicy{APIVersion: "networking.k8s.io/v1", Kind: "NetworkPolicy",
		Metadata: ObjectMeta{Name: egressPolicy, Labels: labels, OwnerReferences: owner},
		Spec: NetworkPolicySpec{PodSelector: LabelSelector{}, PolicyTypes: []string{"Egress"},
			Egress: []NetworkPolicyRule{{
				To:    []NetworkPolicyPeer{{NamespaceSelector: &LabelSelector{MatchLabels: map[string]string{nsNameKey: gwNamespace}}, PodSelector: &LabelSelector{MatchLabels: gwPods}}},
				Ports: []NetworkPolicyPort{{Protocol: "TCP", Port: port}},
			}}}}
}

var nsBad = regexp.MustCompile(`[^a-z0-9-]+`)

// NamespaceFor is a run's namespace: prefix-<run id>, lowercased, with every
// character a namespace name cannot hold replaced by a hyphen, at most 63
// characters. It is pure.
func NamespaceFor(prefix, runID string) string {
	name := nsBad.ReplaceAllString(strings.ToLower(prefix+"-"+runID), "-")
	name = strings.Trim(name, "-")
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-")
	}
	return name
}

// parseUser reads a numeric uid[:gid]; the default user is 1000.
func parseUser(user string) (int64, int64, error) {
	if user == "" {
		user = environments.DefaultUser
	}
	u, g, hasGroup := strings.Cut(user, ":")
	uid, err := strconv.ParseInt(u, 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("plan user %q is not numeric; the kubernetes driver runs agents by uid", user)
	}
	gid := uid
	if hasGroup {
		if gid, err = strconv.ParseInt(g, 10, 64); err != nil {
			return 0, 0, fmt.Errorf("plan user %q: group is not numeric", user)
		}
	}
	if uid == 0 {
		return 0, 0, fmt.Errorf("plan user %q is root; agents run as non-root", user)
	}
	if uid == 65535 || uid == collectorUID {
		return 0, 0, fmt.Errorf("plan user %q is the uid of the pause container or the event collector, which the process observer filters out", user)
	}
	return uid, gid, nil
}
