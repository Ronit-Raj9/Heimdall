package k8s

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
)

// DriverVersion is the Kubernetes driver's own version, recorded in each
// run's kubernetes.json. The environment part keeps the docker_workspace
// name and version: it is the same environment type, run elsewhere.
const DriverVersion = "1.0.0"

// FileRecord is the run folder file where the driver records where and how
// the run ran: cluster, namespace, node, image ids, events.
const FileRecord = "kubernetes.json"

// Timeouts of the run pod's lifecycle.
const (
	podStartTimeout   = 5 * time.Minute
	agentStartTimeout = 15 * time.Minute // the agent image may still be pulled; the wall budget starts at release
	exitTimeout       = 60 * time.Second
	streamGrace       = 15 * time.Second
	execTimeout       = 5 * time.Minute
	nsGoneTimeout     = 2 * time.Minute
	pollEvery         = 250 * time.Millisecond
	observerMaxBytes  = 64 << 20
	killGrace         = 30 * time.Second
)

// Config configures the driver.
type Config struct {
	Client          *Client
	NamespacePrefix string // run namespaces are <prefix>-<run id>; default heimdall-run
	// GatewayURL is the in-cluster URL of the gateway's Service,
	// http://<service>.<namespace>.svc[.cluster.local]:<port>. Agents are
	// still given AgentGatewayURL; its host maps to the Service's cluster
	// IP through /etc/hosts, so the pod needs no DNS.
	GatewayURL      string
	AgentGatewayURL string // gateway.agent_url, what agents are told
	EvalNetwork     string // networks.eval: the plan network name meaning "the evaluation network"
	HelperImage     string
	CollectorImage  string // images.toolproxy: runs the agent event collector beside the agent
	ImagePullPolicy string // IfNotPresent by default
	ServiceAccount  string // the run pods' service account; its token is never mounted
	NodeSelector    map[string]string
	Tolerations     []Toleration
	ProcessWatch    bool
	WorkspaceWatch  bool
	WatchIntervalMS int
	EventsCollector bool
	OutputCapBytes  int64
	// Install is this install's owner id (InstallID), set as a label on
	// every namespace the driver creates, so `k8s cleanup` of another
	// install with the same namespace prefix never selects them (QC2-10-02).
	Install string
	// Local is the Docker driver on the controller: image metadata (PATH,
	// entrypoint, ids, digests) is read from the controller's daemon, where
	// the images are built and from where they are loaded or pushed.
	Local  environments.Driver
	Docker utils.Docker
	Logger schemas.Logger
}

// Driver runs each run as one pod in its own namespace.
type Driver struct {
	cfg Config
	// policySettle is how long the egress probe waits after creating its
	// policies, for the network plugin to program them.
	policySettle time.Duration
	// attachDrain bounds the agent's attach stream after a wall expiry or
	// an interrupt (2-D-06).
	attachDrain time.Duration
}

var _ environments.Driver = (*Driver)(nil)

// New returns the driver with config defaults filled.
func New(cfg Config) (*Driver, error) {
	if cfg.Client == nil {
		return nil, errors.New("kubernetes driver: no API client")
	}
	if cfg.Local == nil {
		return nil, errors.New("kubernetes driver: no local driver for image metadata")
	}
	if cfg.NamespacePrefix == "" {
		cfg.NamespacePrefix = DefaultNamespacePrefix
	}
	if cfg.ImagePullPolicy == "" {
		cfg.ImagePullPolicy = "IfNotPresent"
	}
	if cfg.OutputCapBytes <= 0 {
		cfg.OutputCapBytes = environments.DefaultOutputCap
	}
	if cfg.Logger == nil {
		cfg.Logger = schemas.NoopLogger{}
	}
	switch cfg.ImagePullPolicy {
	case "IfNotPresent", "Never", "Always":
	default:
		return nil, fmt.Errorf("kubernetes.image_pull_policy %q must be IfNotPresent, Never or Always", cfg.ImagePullPolicy)
	}
	return &Driver{cfg: cfg, policySettle: 2 * time.Second, attachDrain: killGrace + streamGrace}, nil
}

// GetName implements schemas.Part: the environment type the suite names.
func (d *Driver) GetName() string { return environments.DockerWorkspaceType }

// GetVersion implements schemas.Part: the environment type's version.
func (d *Driver) GetVersion() string { return d.cfg.Local.GetVersion() }

// Driver implements environments.Driver.
func (d *Driver) Driver() string { return environments.DriverKubernetes }

// EvalIdentity is what the run's handle and the gateway manifest record as
// the evaluation network: the run namespaces' policy on this API server.
// The egress gate requires both to be equal.
func (d *Driver) EvalIdentity() string {
	return "kubernetes:" + d.cfg.NamespacePrefix + "@" + d.cfg.Client.Server
}

// ImageID implements engine.ImageInspector through the controller's Docker:
// the image a pod runs is the one loaded or pushed from there, and Exec
// checks the id the node reports against the frozen one.
func (d *Driver) ImageID(ctx context.Context, ref string) (string, error) {
	if ins, ok := d.cfg.Local.(interface {
		ImageID(context.Context, string) (string, error)
	}); ok {
		return ins.ImageID(ctx, ref)
	}
	return "", errors.New("local driver cannot inspect images")
}

// ImageRepoDigests implements engine.ImageDigester the same way.
func (d *Driver) ImageRepoDigests(ctx context.Context, ref string) ([]string, error) {
	if dg, ok := d.cfg.Local.(interface {
		ImageRepoDigests(context.Context, string) ([]string, error)
	}); ok {
		return dg.ImageRepoDigests(ctx, ref)
	}
	return nil, nil
}

// ImageRoles implements engine.ImageRoler: the helper image starts beside
// every agent here too (prep, observers, bench helper).
//
// The helper and collector roles name this driver's own images, so the
// manifest freezes what the pod actually runs; the collector is a role of
// its own, frozen whenever the driver runs it, even in a suite without
// services or external APIs.
func (d *Driver) ImageRoles() map[string]string {
	out := map[string]string{}
	if r, ok := d.cfg.Local.(interface{ ImageRoles() map[string]string }); ok {
		for k, v := range r.ImageRoles() {
			out[k] = v
		}
	}
	if d.cfg.HelperImage != "" {
		out[suite.ImageHelper] = d.cfg.HelperImage
	}
	if d.cfg.EventsCollector && d.cfg.CollectorImage != "" {
		out[roleCollector] = d.cfg.CollectorImage
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// runRef names a run's objects.
type runRef struct {
	expID, runID, ns string
}

func (d *Driver) ref(rc *schemas.RunContext) (runRef, error) {
	expID, runID, err := environments.RunIDs(rc)
	if err != nil {
		return runRef{}, err
	}
	return runRef{expID: expID, runID: runID, ns: NamespaceFor(d.cfg.NamespacePrefix, runID)}, nil
}

// Create implements schemas.Environment. It makes the run's namespace, the
// anchor every other object is owned by and the deny-all network policy,
// and records the protected and notes hashes from the trusted host task
// folder, as the Docker driver does. The workspace itself is seeded in
// Exec, inside the run pod (an emptyDir lives only as long as its pod), and
// Exec sets the handle's baseline commit then, the way a session reset
// does; nothing reads the workspace between the two.
func (d *Driver) Create(ctx context.Context, rc *schemas.RunContext) (*schemas.EnvironmentHandle, error) {
	if err := environments.CheckFrozenDriver(rc, environments.DriverKubernetes); err != nil {
		return nil, err
	}
	r, err := d.ref(rc)
	if err != nil {
		return nil, err
	}
	if rc.Task == nil || rc.Task.Dir == "" {
		return nil, fmt.Errorf("create workspace for run %s: run context has no task folder", r.runID)
	}
	if why := refusals(environments.DriverSlot{Experiment: rc.Experiment, Suite: rc.Suite, Task: rc.Task, Probe: rc.Probe, Agent: rc.Agent}); len(why) > 0 {
		return nil, fmt.Errorf("create workspace for run %s: the kubernetes driver cannot run it: %s", r.runID, strings.Join(why, "; "))
	}
	repoDir, err := environments.TaskRepo(rc)
	if err != nil {
		return nil, fmt.Errorf("create workspace for run %s: %w", r.runID, err)
	}
	notesRel, notesHost, err := environments.CleanNotes(rc)
	if err != nil {
		return nil, fmt.Errorf("create workspace for run %s: %w", r.runID, err)
	}
	protected, err := environments.ProtectedHashes(repoDir, rc.Task.Protected)
	if err != nil {
		return nil, fmt.Errorf("create workspace for run %s: %w", r.runID, err)
	}
	if err := d.checkFrozenRoles(ctx, rc); err != nil { // Lock fingerprints: frozen evaluator images (suiteimages.go)
		return nil, fmt.Errorf("create workspace for run %s: %w", r.runID, err)
	}
	h := &schemas.EnvironmentHandle{
		Network:    d.EvalIdentity(),
		Workdir:    environments.Workdir(rc),
		Labels:     runLabels(r.runID, r.expID),
		FileHashes: map[string]string{},
		Protected:  protected,
	}
	if notesRel != "" {
		sum, err := utils.SHA256File(notesHost)
		if err != nil {
			return nil, fmt.Errorf("create workspace for run %s: %w", r.runID, err)
		}
		h.FileHashes[notesRel] = sum
	}
	if err := d.freshNamespace(ctx, r); err != nil {
		return nil, fmt.Errorf("create workspace for run %s: %w", r.runID, err)
	}
	owner, err := d.createAnchor(ctx, r)
	if err != nil {
		return nil, fmt.Errorf("create workspace for run %s: %w", r.runID, err)
	}
	if err := d.createServiceAccount(ctx, r.ns, runLabels(r.runID, r.expID), owner); err != nil {
		return nil, fmt.Errorf("create workspace for run %s: %w", r.runID, err)
	}
	if err := d.cfg.Client.Create(ctx, netpolPath(r.ns), denyAllPolicy(runLabels(r.runID, r.expID), owner), nil); err != nil {
		return nil, fmt.Errorf("create workspace for run %s: deny-all network policy: %w", r.runID, err)
	}
	if stagedLayer(rc) {
		if err := d.createStaging(ctx, r, h); err != nil {
			return nil, fmt.Errorf("create workspace for run %s: %w", r.runID, err)
		}
	}
	d.cfg.Logger.Debug("run namespace created", "run", r.runID, "namespace", r.ns)
	return h, nil
}

// freshNamespace replaces a leftover namespace of the same run (a crashed
// earlier attempt) with a new, labelled, empty one.
func (d *Driver) freshNamespace(ctx context.Context, r runRef) error {
	ns := Namespace{APIVersion: "v1", Kind: "Namespace", Metadata: ObjectMeta{Name: r.ns, Labels: d.ownedLabels(runLabels(r.runID, r.expID))}}
	err := d.cfg.Client.Create(ctx, "/api/v1/namespaces", ns, nil)
	if !IsConflict(err) {
		if err != nil {
			return fmt.Errorf("namespace %s: %w", r.ns, err)
		}
		return nil
	}
	// 2-D-15: replace only a namespace that is provably an earlier attempt
	// of this very run; anything else is someone else's and is left alone.
	var cur Namespace
	if err := d.cfg.Client.Get(ctx, nsPath(r.ns), &cur); err != nil {
		return fmt.Errorf("namespace %s exists and cannot be read: %w", r.ns, err)
	}
	if !ownsRunNamespace(cur, r.runID) {
		return fmt.Errorf("namespace %s already exists and does not belong to run %s; refusing to replace it", r.ns, r.runID)
	}
	d.cfg.Logger.Warn("leftover run namespace; replacing it", "namespace", r.ns)
	if err := d.cfg.Client.Delete(ctx, nsPath(r.ns)); err != nil {
		return fmt.Errorf("remove leftover namespace %s: %w", r.ns, err)
	}
	if err := d.cfg.Client.WaitGone(ctx, nsPath(r.ns), nsGoneTimeout); err != nil {
		return fmt.Errorf("remove leftover namespace %s: %w", r.ns, err)
	}
	if err := d.cfg.Client.Create(ctx, "/api/v1/namespaces", ns, nil); err != nil {
		return fmt.Errorf("namespace %s: %w", r.ns, err)
	}
	return nil
}

// ownedLabels adds this install's owner label to a namespace's labels.
func (d *Driver) ownedLabels(l map[string]string) map[string]string {
	if d.cfg.Install != "" {
		l[LabelInstall] = d.cfg.Install
	}
	return l
}

// InstallID is the owner id of the install whose root folder is root: a
// stable label value derived from the folder's absolute path.
func InstallID(root string) string {
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	return utils.SHA256Bytes([]byte(root))[:16]
}

// ownsRunNamespace reports whether ns carries the labels of runID's own
// run namespace (an earlier or the current attempt of that run).
func ownsRunNamespace(ns Namespace, runID string) bool {
	l := ns.Metadata.Labels
	return l[LabelBench] == "1" && l[LabelRole] == RoleRun && l[LabelRun] == runID
}

// createServiceAccount creates kubernetes.service_account in a fresh run or
// probe namespace (2-D-05): a pod may only name an account of its own
// namespace, and every bench namespace is new. The account mounts no token
// and gets no role binding, so it only carries the name (and any image pull
// secrets a cluster admission policy attaches to it). With no configured
// account, or one named "default", the pods use the namespace's default
// account, which Kubernetes creates itself (QC2-10-11). An account that
// already exists in the fresh namespace was put there by cluster admission
// policy and is used as it is; the pods still opt out of its token.
func (d *Driver) createServiceAccount(ctx context.Context, ns string, labels map[string]string, owner []OwnerReference) error {
	if !ownServiceAccount(d.cfg.ServiceAccount) {
		return nil
	}
	sa := ServiceAccount{APIVersion: "v1", Kind: "ServiceAccount", AutomountServiceAccountToken: boolPtr(false),
		Metadata: ObjectMeta{Name: d.cfg.ServiceAccount, Labels: labels, OwnerReferences: owner}}
	err := d.cfg.Client.Create(ctx, corePath(ns, "serviceaccounts"), sa, nil)
	if IsConflict(err) {
		d.cfg.Logger.Warn("service account already exists in the new namespace; using it", "namespace", ns, "account", d.cfg.ServiceAccount)
		return nil
	}
	if err != nil {
		return fmt.Errorf("service account %s: %w", d.cfg.ServiceAccount, err)
	}
	return nil
}

// ownServiceAccount reports whether the driver creates the configured
// account itself: any name but empty or the namespace's "default".
func ownServiceAccount(name string) bool { return name != "" && name != "default" }

// createAnchor creates the run's anchor config map and returns the owner
// reference every other object of the run carries.
func (d *Driver) createAnchor(ctx context.Context, r runRef) ([]OwnerReference, error) {
	cm := ConfigMap{APIVersion: "v1", Kind: "ConfigMap", Metadata: ObjectMeta{Name: anchorName, Labels: runLabels(r.runID, r.expID)},
		Data: map[string]string{"run_id": r.runID, "exp_id": r.expID, "driver_version": DriverVersion}}
	var got ConfigMap
	if err := d.cfg.Client.Create(ctx, corePath(r.ns, "configmaps"), cm, &got); err != nil {
		return nil, fmt.Errorf("anchor: %w", err)
	}
	if got.Metadata.UID == "" {
		return nil, errors.New("anchor: the API server returned no uid")
	}
	return []OwnerReference{{APIVersion: "v1", Kind: "ConfigMap", Name: anchorName, UID: got.Metadata.UID, BlockOwnerDeletion: boolPtr(false)}}, nil
}

// owner reads the anchor's owner reference again (Exec after a resume, or
// a second call).
func (d *Driver) owner(ctx context.Context, r runRef) ([]OwnerReference, error) {
	var cm ConfigMap
	if err := d.cfg.Client.Get(ctx, corePath(r.ns, "configmaps")+"/"+anchorName, &cm); err != nil {
		return nil, fmt.Errorf("anchor of run %s: %w", r.runID, err)
	}
	return []OwnerReference{{APIVersion: "v1", Kind: "ConfigMap", Name: anchorName, UID: cm.Metadata.UID, BlockOwnerDeletion: boolPtr(false)}}, nil
}

// record is kubernetes.json: where the run ran and what the cluster said.
type record struct {
	DriverVersion string            `json:"driver_version"`
	Context       string            `json:"context"`
	Server        string            `json:"server"`
	Namespace     string            `json:"namespace"`
	Pod           string            `json:"pod"`
	Node          string            `json:"node,omitempty"`
	ImageIDs      map[string]string `json:"image_ids,omitempty"`
	BaselineRef   string            `json:"baseline_ref,omitempty"`
	GatewayIP     string            `json:"gateway_ip,omitempty"`
	Network       string            `json:"network"`
	Restarts      map[string]int    `json:"restarts,omitempty"`
	Events        []string          `json:"events,omitempty"`
	Notes         []string          `json:"notes,omitempty"`
}

// Exec implements schemas.Environment: it creates the run pod, seeds the
// workspace inside it, starts the agent once its observers are ready,
// captures its output through attach, enforces the wall budget, stops the
// observers and writes their records. The pod stays for ExportDiff; Destroy
// removes it.
func (d *Driver) Exec(ctx context.Context, rc *schemas.RunContext, plan *schemas.RunPlan) (*schemas.ExecResult, error) {
	r, err := d.ref(rc)
	if err != nil {
		return nil, err
	}
	if rc.Handle == nil || rc.Dir == "" || plan == nil {
		return nil, fmt.Errorf("exec run %s: needs a workspace handle, a run folder and a plan", r.runID)
	}
	if rc.Session != nil {
		return nil, fmt.Errorf("exec run %s: %s", r.runID, RefuseSessions)
	}
	rec := &record{DriverVersion: DriverVersion, Context: d.cfg.Client.Context, Server: d.cfg.Client.Server, Namespace: r.ns, Pod: podName, Network: "none"}
	defer d.writeRecord(context.WithoutCancel(ctx), rc, r, rec)
	in, gw, err := d.podInput(ctx, rc, r, plan)
	if err != nil {
		return nil, fmt.Errorf("exec run %s: %w", r.runID, err)
	}
	owner, err := d.owner(ctx, r)
	if err != nil {
		return nil, fmt.Errorf("exec run %s: %w", r.runID, err)
	}
	// The task environment's variables (environment.env) reach the agent
	// under the plan's own, as under Docker; plan.json keeps the plan.
	launch, err := d.launch(ctx, rc, r, environments.WithTaskEnv(rc, plan), in.Collector)
	if err != nil {
		return nil, fmt.Errorf("exec run %s: %w", r.runID, err)
	}
	in.Argv = launch.argv
	if err := d.createRunObjects(ctx, r, owner, launch.env, gw); err != nil {
		return nil, fmt.Errorf("exec run %s: %w", r.runID, err)
	}
	if gw != nil {
		rec.GatewayIP, rec.Network = gw.ip, d.EvalIdentity()
	}
	if err := d.cfg.Client.Create(ctx, corePath(r.ns, "pods"), buildPod(in, owner), nil); err != nil {
		return nil, fmt.Errorf("exec run %s: create pod: %w", r.runID, err)
	}
	if err := d.waitContainer(ctx, r, ctrPrep, true, podStartTimeout); err != nil {
		return nil, fmt.Errorf("exec run %s: %w", r.runID, err)
	}
	if err := d.checkHelperOnNode(ctx, rc, r, in.HelperImage); err != nil { // before the helper seeds the workspace (suiteimages.go)
		return nil, fmt.Errorf("exec run %s: %w", r.runID, err)
	}
	ref, err := d.seed(ctx, rc, r, in)
	if err != nil {
		return nil, fmt.Errorf("exec run %s: seed workspace: %w", r.runID, err)
	}
	rc.Handle.BaselineRef, rec.BaselineRef = ref, ref
	if err := d.waitContainer(ctx, r, ctrAgent, false, agentStartTimeout); err != nil {
		return nil, fmt.Errorf("exec run %s: %w", r.runID, err)
	}
	if err := d.checkImages(ctx, rc, r, rec); err != nil {
		d.killAgent(ctx, r, in.UID)
		return nil, fmt.Errorf("exec run %s: %w", r.runID, err)
	}
	if err := d.waitIsolated(ctx, r); err != nil {
		d.killAgent(ctx, r, in.UID)
		return nil, fmt.Errorf("exec run %s: %w", r.runID, err)
	}
	in.mainPID = d.agentMainPID(ctx, r, in.UID)
	res, err := d.attach(ctx, rc, r, plan, in.UID)
	d.finishObservers(context.WithoutCancel(ctx), rc, r, in, res)
	d.copyCollector(context.WithoutCancel(ctx), rc, r, in)
	d.copyBackLog(context.WithoutCancel(ctx), rc, r, in.HelperImage)
	if err != nil {
		return res, fmt.Errorf("exec run %s: %w", r.runID, err)
	}
	return res, nil
}

// gatewayTarget is the in-cluster gateway a networked run may reach.
type gatewayTarget struct {
	namespace  string
	ip         string
	host       string
	targetPort PortRef // a number or a gateway container port name
	selector   map[string]string
}

// podInput checks the plan against the driver's baseline and resolves the
// gateway when the plan has a network.
func (d *Driver) podInput(ctx context.Context, rc *schemas.RunContext, r runRef, plan *schemas.RunPlan) (podInput, *gatewayTarget, error) {
	var in podInput
	if plan.Image == "" {
		return in, nil, errors.New("plan has no image")
	}
	if len(plan.Dirs) > 0 {
		return in, nil, errors.New(RefuseSkills)
	}
	if len(plan.Egress) > 0 {
		return in, nil, errors.New(RefuseRemote)
	}
	uid, gid, err := parseUser(plan.User)
	if err != nil {
		return in, nil, err
	}
	wd := plan.Workdir
	if wd == "" {
		wd = environments.DefaultWorkdir
	}
	wall := plan.WallS
	if wall <= 0 {
		wall = environments.DefaultWallS
	}
	in = podInput{RunID: r.runID, ExpID: r.expID, AgentImage: plan.Image, HelperImage: d.helperImage(rc), PullPolicy: d.cfg.ImagePullPolicy,
		Workdir: wd, UID: uid, GID: gid, CPUs: plan.CPUs, MemGB: plan.MemGB, WallS: wall,
		ProcWatch: d.cfg.ProcessWatch, FSWatch: d.cfg.WorkspaceWatch, IntervalMS: d.cfg.WatchIntervalMS,
		ServiceAccount: d.cfg.ServiceAccount, NodeSelector: d.cfg.NodeSelector, Tolerations: d.cfg.Tolerations,
		Collector: d.cfg.EventsCollector && d.cfg.CollectorImage != "", CollectorImage: d.cfg.CollectorImage}
	if in.HelperImage == "" {
		return in, nil, errors.New("no helper image (images.helper)")
	}
	switch n := plan.Network; {
	case n == "none":
		return in, nil, nil
	case n == "" || n == d.cfg.EvalNetwork:
	default:
		return in, nil, fmt.Errorf("plan network %q is not the evaluation network %q", n, d.cfg.EvalNetwork)
	}
	gw, err := d.ResolveGateway(ctx)
	if err != nil {
		return in, nil, err
	}
	in.GatewayIP, in.GatewayHost = gw.ip, gw.host
	return in, gw, nil
}

// ResolveGateway reads the gateway Service named by gateway_url: its
// cluster IP (the agents' /etc/hosts entry for the agent URL's host), its
// pod selector and target port (the egress policy). The agent URL's port
// must be the Service port, since /etc/hosts cannot remap ports.
func (d *Driver) ResolveGateway(ctx context.Context) (*gatewayTarget, error) {
	svc, ns, port, err := parseServiceURL(d.cfg.GatewayURL)
	if err != nil {
		return nil, err
	}
	au, err := url.Parse(d.cfg.AgentGatewayURL)
	if err != nil || au.Hostname() == "" {
		return nil, fmt.Errorf("gateway.agent_url %q is not a URL", d.cfg.AgentGatewayURL)
	}
	if net.ParseIP(au.Hostname()) != nil {
		return nil, fmt.Errorf("gateway.agent_url %q must use a host name, which the run pod maps to the gateway Service", d.cfg.AgentGatewayURL)
	}
	agentPort := urlPort(au)
	if agentPort != port {
		return nil, fmt.Errorf("gateway.agent_url port %d must equal the gateway Service port %d of kubernetes.gateway_url", agentPort, port)
	}
	var s Service
	if err := d.cfg.Client.Get(ctx, corePath(ns, "services")+"/"+url.PathEscape(svc), &s); err != nil {
		return nil, fmt.Errorf("gateway service %s/%s (kubernetes.gateway_url; run `bifrost-bench k8s gateway up`): %w", ns, svc, err)
	}
	if s.Spec.ClusterIP == "" || s.Spec.ClusterIP == "None" {
		return nil, fmt.Errorf("gateway service %s/%s has no cluster IP", ns, svc)
	}
	if len(s.Spec.Selector) == 0 {
		return nil, fmt.Errorf("gateway service %s/%s selects no pods, so no egress policy can name them", ns, svc)
	}
	var target PortRef
	for _, p := range s.Spec.Ports {
		if p.Port == port {
			target = p.TargetPort // a named port resolves on the gateway pods, as in a NetworkPolicy
			if target.IsZero() {
				target = Port(p.Port)
			}
		}
	}
	if target.IsZero() {
		return nil, fmt.Errorf("gateway service %s/%s has no port %d", ns, svc, port)
	}
	return &gatewayTarget{namespace: ns, ip: s.Spec.ClusterIP, host: au.Hostname(), targetPort: target, selector: s.Spec.Selector}, nil
}

// parseServiceURL reads http://<service>.<namespace>.svc[...]:<port>.
func parseServiceURL(raw string) (string, string, int, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "", "", 0, fmt.Errorf("kubernetes.gateway_url %q is not a URL", raw)
	}
	parts := strings.Split(u.Hostname(), ".")
	if len(parts) < 3 || parts[2] != "svc" {
		return "", "", 0, fmt.Errorf("kubernetes.gateway_url %q must name a Service as http://<service>.<namespace>.svc:<port>", raw)
	}
	return parts[0], parts[1], urlPort(u), nil
}

func urlPort(u *url.URL) int {
	if p, err := strconv.Atoi(u.Port()); err == nil {
		return p
	}
	if u.Scheme == "https" {
		return 443
	}
	return 80
}

func (d *Driver) helperImage(rc *schemas.RunContext) string {
	if d.cfg.HelperImage == "" && rc.Suite != nil {
		return rc.Suite.BaseImage.Tag
	}
	return d.cfg.HelperImage
}

// agentLaunch is the resolved agent argv and environment.
type agentLaunch struct {
	argv []string
	env  map[string]string
}

// launch builds the Docker driver's launch for the plan (same PATH with the
// shim folder first, same wrapper for files and long values) and resolves an
// image entrypoint the plan keeps from the controller's image config, since
// a pod's command replaces the image entrypoint.
func (d *Driver) launch(ctx context.Context, rc *schemas.RunContext, r runRef, plan *schemas.RunPlan, collector bool) (*agentLaunch, error) {
	img, err := inspectConfig(ctx, d.cfg.Docker, plan.Image)
	if err != nil {
		return nil, err
	}
	base := plan.Env["PATH"]
	if base == "" {
		base = img.path()
	}
	l, err := environments.BuildAgentLaunch(plan, environments.AuxMount+"/shim:"+base)
	if err != nil {
		return nil, err
	}
	var argv []string
	switch {
	case l.Entrypoint != "":
		argv = append([]string{l.Entrypoint}, l.Args...)
	case len(l.Args) > 0:
		argv = append(append([]string(nil), img.Entrypoint...), l.Args...)
	default:
		argv = append(append([]string(nil), img.Entrypoint...), img.Cmd...)
	}
	if len(argv) == 0 {
		return nil, fmt.Errorf("image %s has no entrypoint or command and the plan gives none", plan.Image)
	}
	env := map[string]string{}
	for _, kv := range environments.AgentEventEnv(rc, r.runID, l.Env, collector) {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	return &agentLaunch{argv: argv, env: env}, nil
}

// imageConfig is the part of an image's config the launch needs.
type imageConfig struct {
	Env        []string `json:"Env"`
	Entrypoint []string `json:"Entrypoint"`
	Cmd        []string `json:"Cmd"`
}

func (c imageConfig) path() string {
	for _, kv := range c.Env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok && v != "" {
			return v
		}
	}
	return environments.DefaultPath
}

func inspectConfig(ctx context.Context, d utils.Docker, ref string) (*imageConfig, error) {
	res, err := d.MustRun(ctx, "image", "inspect", "--format", "{{json .Config}}", ref)
	if err != nil {
		return nil, fmt.Errorf("inspect image %s on the controller (the driver reads PATH and the entrypoint there): %w", ref, err)
	}
	var c imageConfig
	if err := json.Unmarshal(res.Stdout, &c); err != nil {
		return nil, fmt.Errorf("inspect image %s: parse config: %w", ref, err)
	}
	return &c, nil
}

// createRunObjects creates the agent's env secret, the observers' scripts
// and, for a networked plan, the gateway egress policy.
func (d *Driver) createRunObjects(ctx context.Context, r runRef, owner []OwnerReference, env map[string]string, gw *gatewayTarget) error {
	labels := runLabels(r.runID, r.expID)
	data := make(map[string][]byte, len(env))
	for k, v := range env {
		data[k] = []byte(v)
	}
	sec := Secret{APIVersion: "v1", Kind: "Secret", Type: "Opaque", Immutable: boolPtr(true), Data: data,
		Metadata: ObjectMeta{Name: envSecret, Labels: labels, OwnerReferences: owner}}
	if err := d.cfg.Client.Create(ctx, corePath(r.ns, "secrets"), sec, nil); err != nil {
		return fmt.Errorf("agent env secret: %w", err)
	}
	cm := ConfigMap{APIVersion: "v1", Kind: "ConfigMap", Immutable: boolPtr(true),
		Metadata: ObjectMeta{Name: scriptsMap, Labels: labels, OwnerReferences: owner},
		Data:     map[string]string{ctrProcWatch + ".py": environments.ProcWatchScript, ctrFSWatch + ".py": environments.FSWatchScript}}
	if err := d.cfg.Client.Create(ctx, corePath(r.ns, "configmaps"), cm, nil); err != nil {
		return fmt.Errorf("observer scripts: %w", err)
	}
	if gw != nil {
		if err := d.cfg.Client.Create(ctx, netpolPath(r.ns), gatewayPolicy(labels, owner, gw.namespace, gw.selector, gw.targetPort), nil); err != nil {
			return fmt.Errorf("gateway egress policy: %w", err)
		}
	}
	return nil
}

// getPod reads the run pod.
func (d *Driver) getPod(ctx context.Context, r runRef) (*Pod, error) {
	var p Pod
	if err := d.cfg.Client.Get(ctx, corePath(r.ns, "pods")+"/"+podName, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// containerStatus finds a container's status (init or regular).
func containerStatus(p *Pod, name string, init bool) *ContainerStatus {
	list := p.Status.ContainerStatuses
	if init {
		list = p.Status.InitContainerStatuses
	}
	for i := range list {
		if list[i].Name == name {
			return &list[i]
		}
	}
	return nil
}

// waitContainer polls the pod until container runs. A container stuck
// waiting with an image or config error, a terminated pod, or the timeout
// fail with the reason and the namespace's events.
func (d *Driver) waitContainer(ctx context.Context, r runRef, name string, init bool, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	last := ""
	for {
		p, err := d.getPod(ctx, r)
		if err != nil && !isTransient(err) {
			return fmt.Errorf("read pod: %w", err)
		}
		if p != nil {
			if p.Status.Phase == "Failed" || p.Status.Phase == "Succeeded" {
				return d.withEvents(ctx, r, fmt.Errorf("pod ended (%s %s) before container %s ran", p.Status.Phase, p.Status.Reason, name))
			}
			if cs := containerStatus(p, name, init); cs != nil {
				switch {
				case cs.State.Running != nil:
					return nil
				case cs.State.Terminated != nil:
					return d.withEvents(ctx, r, fmt.Errorf("container %s exited %d (%s) before it was used", name, cs.State.Terminated.ExitCode, cs.State.Terminated.Reason))
				case cs.State.Waiting != nil:
					last = cs.State.Waiting.Reason + " " + cs.State.Waiting.Message
					switch cs.State.Waiting.Reason {
					case "ErrImagePull", "ImagePullBackOff", "ErrImageNeverPull", "InvalidImageName", "CreateContainerConfigError", "CreateContainerError":
						return d.withEvents(ctx, r, fmt.Errorf("container %s cannot start: %s", name, strings.TrimSpace(last)))
					}
				}
			}
			for _, c := range p.Status.Conditions {
				if c.Type == "PodScheduled" && c.Status == "False" && c.Reason == "Unschedulable" {
					last = "unschedulable: " + c.Message
				}
			}
		}
		if time.Now().After(deadline) {
			return d.withEvents(ctx, r, fmt.Errorf("container %s not running after %s (%s)", name, timeout, strings.TrimSpace(last)))
		}
		if err := sleepCtx(ctx, pollEvery); err != nil {
			return err
		}
	}
}

// withEvents adds the namespace's warning events to err.
func (d *Driver) withEvents(ctx context.Context, r runRef, err error) error {
	evs, eerr := d.cfg.Client.Events(context.WithoutCancel(ctx), r.ns)
	if eerr != nil || len(evs) == 0 {
		return err
	}
	var warn []Event
	for _, e := range evs {
		if e.Type == "Warning" {
			warn = append(warn, e)
		}
	}
	if len(warn) == 0 {
		return err
	}
	return fmt.Errorf("%w; events: %s", err, strings.Join(eventLines(warn), "; "))
}

// seed streams the task repo (and the clean notes file on top) as a tar into
// the prep container, which runs the Docker driver's populate script on it
// and prints the baseline commit id, then releases the pod.
func (d *Driver) seed(ctx context.Context, rc *schemas.RunContext, r runRef, in podInput) (string, error) {
	repoDir, err := environments.TaskRepo(rc)
	if err != nil {
		return "", err
	}
	notesRel, notesHost, err := environments.CleanNotes(rc)
	if err != nil {
		return "", err
	}
	tarball, err := seedTar(repoDir, notesRel, notesHost)
	if err != nil {
		return "", err
	}
	script := environments.PopulateScript + fmt.Sprintf(seedTail, in.UID, in.GID)
	cctx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()
	out, err := d.cfg.Client.ExecOutput(cctx, r.ns, podName, ctrPrep,
		[]string{"env", "BENCH_DATE=" + environments.BaselineDate, "BENCH_SRC_TAR=1", "/bin/sh", "-c", script}, bytes.NewReader(tarball), 1<<20)
	if err != nil {
		return "", err
	}
	ref, err := environments.LastRef(out)
	if err != nil {
		return "", err
	}
	if err := d.layStaged(cctx, rc, r, in.HelperImage); err != nil {
		return "", err
	}
	if _, err := d.cfg.Client.ExecOutput(cctx, r.ns, podName, ctrPrep, []string{"touch", seededFile}, nil, 1<<10); err != nil {
		return "", fmt.Errorf("release the pod: %w", err)
	}
	return ref, nil
}

// seedTar is the workspace's starting state as a tar: every file, folder and
// symlink of the repo folder with its mode, in path order, then the notes
// file at its workspace path. It holds what the Docker driver's
// `cp -dR --preserve=mode` copies, so the baseline commit is the same.
func seedTar(repoDir, notesRel, notesHost string) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	var paths []string
	err := filepath.WalkDir(repoDir, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != repoDir {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read task repo: %w", err)
	}
	sort.Strings(paths)
	for _, p := range paths {
		rel, err := filepath.Rel(repoDir, p)
		if err != nil {
			return nil, err
		}
		st, err := os.Lstat(p)
		if err != nil {
			return nil, err
		}
		h := &tar.Header{Name: filepath.ToSlash(rel), Mode: int64(st.Mode().Perm()), ModTime: st.ModTime(), Format: tar.FormatPAX}
		switch {
		case st.IsDir():
			h.Typeflag, h.Name = tar.TypeDir, h.Name+"/"
		case st.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return nil, err
			}
			h.Typeflag, h.Linkname = tar.TypeSymlink, target
		case st.Mode().IsRegular():
			h.Typeflag, h.Size = tar.TypeReg, st.Size()
		default:
			return nil, fmt.Errorf("task repo file %s is not a file, folder or symlink", rel)
		}
		if err := tw.WriteHeader(h); err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg {
			f, err := os.Open(p)
			if err != nil {
				return nil, err
			}
			_, cerr := io.Copy(tw, f)
			f.Close()
			if cerr != nil {
				return nil, cerr
			}
		}
	}
	if notesRel != "" {
		b, err := os.ReadFile(notesHost)
		if err != nil {
			return nil, fmt.Errorf("read clean notes: %w", err)
		}
		for _, dir := range parentDirs(notesRel) {
			if err := tw.WriteHeader(&tar.Header{Name: dir + "/", Typeflag: tar.TypeDir, Mode: 0o755, Format: tar.FormatPAX}); err != nil {
				return nil, err
			}
		}
		if err := tw.WriteHeader(&tar.Header{Name: notesRel, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(b)), Format: tar.FormatPAX}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(b); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// parentDirs lists a relative path's parent folders, outermost first.
func parentDirs(rel string) []string {
	var out []string
	parts := strings.Split(rel, "/")
	for i := 1; i < len(parts); i++ {
		out = append(out, strings.Join(parts[:i], "/"))
	}
	return out
}

// checkImages records the image ids the node runs and refuses an agent
// image that is not the one the experiment froze: the id the node reports
// must be the frozen local id or one of the image's registry digests.
func (d *Driver) checkImages(ctx context.Context, rc *schemas.RunContext, r runRef, rec *record) error {
	p, err := d.getPod(ctx, r)
	if err != nil {
		return fmt.Errorf("read pod: %w", err)
	}
	rec.Node = p.Spec.NodeName
	rec.ImageIDs = map[string]string{}
	var agentID string
	helper := d.helperImage(rc)
	for _, cs := range append(append([]ContainerStatus(nil), p.Status.InitContainerStatuses...), p.Status.ContainerStatuses...) {
		if cs.ImageID != "" {
			rec.ImageIDs[cs.Name] = cs.ImageID
		}
		var err error
		switch cs.Name {
		case ctrAgent:
			agentID = cs.ImageID
		case ctrPrep, ctrFSWatch, ctrProcWatch, ctrBench: // Lock fingerprints: frozen helper image (suiteimages.go)
			err = d.checkNodeRole(ctx, rc, rec, d.helperRole(), helper, cs.Name, cs.ImageID)
		case ctrCollector:
			err = d.checkNodeRole(ctx, rc, rec, roleCollector, d.cfg.CollectorImage, cs.Name, cs.ImageID)
		}
		if err != nil {
			return err
		}
	}
	want := environments.FrozenImageID(rc)
	if want == "" || rc.Agent == nil || rc.Agent.Image == "" {
		return nil
	}
	if d.agentImageFrozen(ctx, rec, agentID, want, rc.Agent.Image) {
		return nil
	}
	return fmt.Errorf("agent image on node %s is %s, but experiment %s was planned with %s; load or push the planned image", rec.Node, agentID, rc.Manifest.ExpID, want)
}

// agentImageFrozen reports whether the node's agent image id is the frozen
// image: the frozen id itself, a node image pinned to the frozen digest, or,
// only while the local tag still names the frozen id, one of that image's
// registry digests or its config digest. Digests read from the tag are never
// trusted once the tag has moved, since they then describe another image.
func (d *Driver) agentImageFrozen(ctx context.Context, rec *record, agentID, want, ref string) bool {
	if imageMatches(agentID, want, nil) {
		return true
	}
	if d.nodeImageHasDigest(ctx, rec.Node, agentID, want) {
		return true
	}
	local, err := d.ImageID(ctx, ref)
	if err != nil || local != want {
		return false
	}
	// QC2-10-07: the digests are read from the checked immutable id, never
	// the tag again, which may have moved since the line above.
	digests, _ := d.ImageRepoDigests(ctx, want)
	if imageMatches(agentID, want, digests) {
		return true
	}
	cfg, cerr := d.configDigest(ctx, want, want, "linux/"+d.nodeArch(ctx, rec.Node))
	if cerr == nil && cfg == agentID {
		rec.Notes = append(rec.Notes, "agent image "+agentID+" is the config of the frozen image "+want)
		return true
	}
	if cerr != nil {
		rec.Notes = append(rec.Notes, "config digest of "+ref+": "+cerr.Error())
	}
	return false
}

// nodeImageHasDigest reports whether the node lists the container's image
// (by the id it reported) under a name carrying the frozen digest: a loaded
// image is reported by its config id, while the frozen id of a Docker with
// the containerd image store is the image index digest, which the node keeps
// as the image's repo digest.
func (d *Driver) nodeImageHasDigest(ctx context.Context, node, imageID, frozen string) bool {
	if node == "" || imageID == "" {
		return false
	}
	var n Node
	if err := d.cfg.Client.Get(ctx, "/api/v1/nodes/"+url.PathEscape(node), &n); err != nil {
		return false
	}
	return nodeImageMatches(n, imageID, frozen)
}

// nodeArch is the node's CPU architecture, amd64 when it cannot be read.
func (d *Driver) nodeArch(ctx context.Context, node string) string {
	var n Node
	if node != "" && d.cfg.Client.Get(ctx, "/api/v1/nodes/"+url.PathEscape(node), &n) == nil && n.Status.NodeInfo.Architecture != "" {
		return n.Status.NodeInfo.Architecture
	}
	return "amd64"
}

// nodeImageMatches is nodeImageHasDigest's test on a node's image list. It
// is pure.
func nodeImageMatches(n Node, imageID, frozen string) bool {
	for _, img := range n.Status.Images {
		has, pinned := false, false
		for _, name := range img.Names {
			if name == imageID || strings.HasSuffix(imageID, "@"+name) || strings.HasSuffix(name, "@"+strings.TrimPrefix(imageID, "sha256:")) {
				has = true
			}
			if strings.HasSuffix(name, "@"+frozen) || name == frozen {
				pinned = true
			}
		}
		if has && pinned {
			return true
		}
	}
	return false
}

// imageMatches reports whether a container's imageID is the frozen local id
// (sha256:..., as containerd reports a loaded image) or carries one of the
// image's registry digests (repo@sha256:..., as it reports a pulled one).
// It is pure.
func imageMatches(got, frozen string, digests []string) bool {
	if got == "" {
		return false
	}
	if got == frozen || strings.TrimPrefix(got, "docker.io/") == frozen {
		return true
	}
	_, gd, ok := strings.Cut(got, "@")
	if !ok {
		return false
	}
	for _, dg := range digests {
		if _, dd, ok := strings.Cut(dg, "@"); ok && dd == gd {
			return true
		}
	}
	return false
}

// attach starts the agent: it attaches to the gated agent container,
// releases it with one line on stdin and copies stdout and stderr into the
// run folder, capped and with the run key redacted, until the streams end.
// The wall budget kills every process of the agent's uid on expiry.
func (d *Driver) attach(ctx context.Context, rc *schemas.RunContext, r runRef, plan *schemas.RunPlan, uid int64) (*schemas.ExecResult, error) {
	stdoutPath, stderrPath, _ := environments.StreamPaths(rc)
	res := &schemas.ExecResult{StdoutPath: stdoutPath, StderrPath: stderrPath}
	outF, err := os.OpenFile(stdoutPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", stdoutPath, err)
	}
	defer outF.Close()
	errF, err := os.OpenFile(stderrPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", stderrPath, err)
	}
	defer errF.Close()
	var secrets []string
	if rc.Key != nil {
		secrets = append(secrets, rc.Key.Value)
	}
	stdout := environments.NewOutputWriter(outF, d.cfg.OutputCapBytes, secrets...)
	stderr := environments.NewOutputWriter(errF, d.cfg.OutputCapBytes, secrets...)
	wall := time.Duration(plan.WallS) * time.Second
	if plan.WallS <= 0 {
		wall = environments.DefaultWallS * time.Second
	}
	actx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	// 2-D-06: the attach stream has its own context. After a wall expiry or
	// an interrupt it gets attachDrain to deliver the killed agent's last
	// output, then it is closed even when the kill request failed or the API
	// proxy stalls the stream, so the run always reaches waitExit and
	// cleanup.
	attCtx, attCancel := context.WithCancel(actx)
	defer attCancel()
	var drain *time.Timer
	var timedOut, interrupted, drained bool
	var mu sync.Mutex
	stopStream := func() { // called with mu held
		if drain == nil {
			drain = time.AfterFunc(d.attachDrain, func() {
				mu.Lock()
				drained = true
				mu.Unlock()
				attCancel()
			})
		}
	}
	timer := time.AfterFunc(wall, func() {
		mu.Lock()
		timedOut = true
		stopStream()
		mu.Unlock()
		d.killAgent(actx, r, uid)
	})
	stopCtx := context.AfterFunc(ctx, func() {
		mu.Lock()
		interrupted = true
		stopStream()
		mu.Unlock()
		d.killAgent(actx, r, uid)
	})
	res.Started = time.Now().UTC()
	aerr := d.cfg.Client.Attach(attCtx, r.ns, podName, ctrAgent, StreamOptions{Stdin: strings.NewReader("\n"), Stdout: stdout, Stderr: stderr})
	timer.Stop()
	stopCtx()
	mu.Lock()
	if drain != nil {
		drain.Stop()
	}
	mu.Unlock()
	res.Ended = time.Now().UTC()
	code, oom, early, werr := d.waitExit(actx, r, uid)
	if early {
		d.cfg.Logger.Warn("agent output stream ended before the agent did; later output may be missing", "run", r.runID)
		res.Truncated = true
	}
	mu.Lock()
	res.TimedOut = timedOut && !interrupted
	// QC2-10-08: the drain timer closing a stalled stream after the wall
	// expiry is this driver's own doing, not a transport failure; the run
	// is an ordinary timeout once the pod reported its exit.
	ownClose := drained && res.TimedOut && errors.Is(aerr, context.Canceled)
	mu.Unlock()
	res.Truncated = res.Truncated || stdout.Truncated() || stderr.Truncated()
	if err := firstErr(stdout.Close(), stderr.Close(), outF.Sync(), errF.Sync()); err != nil {
		return res, fmt.Errorf("capture agent output: %w", err)
	}
	if interrupted {
		return res, fmt.Errorf("agent run interrupted: %w", ctx.Err())
	}
	if aerr != nil && !early && !ownClose {
		return res, fmt.Errorf("attach agent: %w", aerr)
	}
	if werr != nil {
		return res, werr
	}
	res.ExitCode, res.OOMKilled = code, oom
	if oom {
		d.cfg.Logger.Warn("agent container ended by the out-of-memory killer", "run", r.runID, "exit_code", code)
	}
	return res, nil
}

// waitExit waits for the agent container to terminate after its streams
// ended and returns its exit code and whether the kubelet reported it
// OOMKilled (oomKilled). The streams end when the process does,
// and the kubelet reports it within a second or two; a container still
// running after streamGrace means the attach connection broke (early): it
// is waited for until exitTimeout, then killed.
func (d *Driver) waitExit(ctx context.Context, r runRef, uid int64) (int, bool, bool, error) {
	start := time.Now()
	early, killed := false, false
	for {
		p, err := d.getPod(ctx, r)
		if err != nil && !isTransient(err) {
			return -1, false, early, fmt.Errorf("read agent exit code: %w", err)
		}
		if err == nil {
			if cs := containerStatus(p, ctrAgent, false); cs != nil && cs.State.Terminated != nil {
				return cs.State.Terminated.ExitCode, oomKilled(cs.State), early, nil
			}
		}
		waited := time.Since(start)
		if waited > streamGrace {
			early = true
		}
		if waited > exitTimeout && !killed {
			d.killAgent(ctx, r, uid)
			killed = true
		}
		if waited > exitTimeout+killGrace {
			return -1, false, early, errors.New("the agent container did not end after its output stream did, nor after it was killed")
		}
		if err := sleepCtx(ctx, pollEvery); err != nil {
			return -1, false, early, err
		}
	}
}

// oomKilled reports whether a terminated container was ended by the
// out-of-memory killer: the kubelet's reason OOMKilled, read from the
// container cgroup's memory events by the runtime.
func oomKilled(st ContainerState) bool {
	return st.Terminated != nil && st.Terminated.Reason == "OOMKilled"
}

// killScript kills every process of the agent's uid in the pod's shared PID
// namespace, from the trusted bench helper (root, CAP_KILL). Bench processes
// run as root and the pause process as 65535, so none of them is hit.
const killScript = `for d in /proc/[0-9]*; do
  u=$(sed -n 's/^Uid:[[:space:]]*\([0-9]*\).*/\1/p' "$d/status" 2>/dev/null)
  [ "$u" = "$1" ] && kill -KILL "${d#/proc/}" 2>/dev/null
done
exit 0`

func (d *Driver) killAgent(ctx context.Context, r runRef, uid int64) {
	kctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), killGrace)
	defer cancel()
	if _, err := d.cfg.Client.ExecOutput(kctx, r.ns, podName, ctrBench, []string{"/bin/sh", "-c", killScript, "kill", strconv.FormatInt(uid, 10)}, nil, 1<<16); err != nil {
		d.cfg.Logger.Warn("kill agent processes", "run", r.runID, "error", err.Error())
	}
}

// ExportDiff implements schemas.Environment: the Docker driver's export
// script runs in the trusted bench helper, on its read-only view of the
// workspace, and the allowlist runs here.
func (d *Driver) ExportDiff(ctx context.Context, rc *schemas.RunContext) (*schemas.Diff, error) {
	r, err := d.ref(rc)
	if err != nil {
		return nil, err
	}
	if rc.Handle == nil || rc.Dir == "" {
		return nil, fmt.Errorf("export diff for run %s: needs a workspace handle and a run folder", r.runID)
	}
	if !refOK(rc.Handle.BaselineRef) {
		return nil, fmt.Errorf("export diff for run %s: invalid baseline ref %q", r.runID, rc.Handle.BaselineRef)
	}
	cctx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()
	raw, err := d.cfg.Client.ExecOutput(cctx, r.ns, podName, ctrBench,
		[]string{"env", "BENCH_BASE=" + rc.Handle.BaselineRef, "BENCH_CAP=" + strconv.Itoa(environments.MaxDiffBytes), "/bin/sh", "-c", environments.ExportScript},
		nil, int64(environments.MaxDiffBytes)+1)
	if err != nil {
		return nil, fmt.Errorf("export diff for run %s: %w", r.runID, err)
	}
	f, err := environments.FilterDiff(raw)
	if err != nil {
		return nil, fmt.Errorf("export diff for run %s: %w", r.runID, err)
	}
	for _, rj := range f.Rejected {
		if rj.Reason == environments.RejectCache {
			continue
		}
		d.cfg.Logger.Warn("diff path rejected", "run", r.runID, "path", rj.Path, "reason", rj.Reason)
	}
	out := filepath.Join(rc.Dir, "workspace.diff")
	if err := os.WriteFile(out, f.Kept, 0o644); err != nil {
		return nil, fmt.Errorf("export diff for run %s: write %s: %w", r.runID, out, err)
	}
	return &schemas.Diff{Path: out, SHA256: utils.SHA256Bytes(f.Kept), Files: f.Files, Rejected: f.Rejected}, nil
}

func refOK(ref string) bool {
	if len(ref) != 40 && len(ref) != 64 {
		return false
	}
	for _, c := range ref {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// Destroy implements schemas.Environment: the anchor goes first, which
// garbage-collects every object it owns, then the namespace, which takes
// anything else with it. Missing objects are fine, so it is idempotent and
// works after a failed Create.
func (d *Driver) Destroy(ctx context.Context, rc *schemas.RunContext) error {
	r, err := d.ref(rc)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), killGrace)
	defer cancel()
	var errs []error
	// QC2-10-01: cleanup runs even when Create refused a namespace of the
	// same name that belongs to someone else (or failed before creating
	// one). Delete only a namespace that is provably this run's, and only
	// that very object (UID precondition), never one that replaced it.
	var cur Namespace
	switch err := d.cfg.Client.Get(cctx, nsPath(r.ns), &cur); {
	case IsNotFound(err):
	case err != nil:
		errs = append(errs, fmt.Errorf("namespace %s: %w", r.ns, err))
	case !ownsRunNamespace(cur, r.runID):
		d.cfg.Logger.Warn("namespace does not belong to this run; not deleting it", "namespace", r.ns, "run", r.runID)
	default:
		if err := d.cfg.Client.Delete(cctx, corePath(r.ns, "configmaps")+"/"+anchorName); err != nil {
			errs = append(errs, err)
		}
		if err := d.cfg.Client.DeleteUID(cctx, nsPath(r.ns), cur.Metadata.UID); err != nil {
			errs = append(errs, err)
		}
	}
	if stagedLayer(rc) || (rc.Handle != nil && rc.Handle.Volume != "") {
		if err := d.removeStaging(cctx, r); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("destroy run %s: %w", r.runID, err)
	}
	return nil
}

// writeRecord writes kubernetes.json with the pod's restarts and events.
func (d *Driver) writeRecord(ctx context.Context, rc *schemas.RunContext, r runRef, rec *record) {
	if p, err := d.getPod(ctx, r); err == nil {
		rec.Node = p.Spec.NodeName
		for _, cs := range append(append([]ContainerStatus(nil), p.Status.InitContainerStatuses...), p.Status.ContainerStatuses...) {
			if cs.RestartCount > 0 {
				if rec.Restarts == nil {
					rec.Restarts = map[string]int{}
				}
				rec.Restarts[cs.Name] = cs.RestartCount
			}
		}
	}
	if evs, err := d.cfg.Client.Events(ctx, r.ns); err == nil {
		rec.Events = eventLines(evs)
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return
	}
	if err := os.WriteFile(filepath.Join(rc.Dir, FileRecord), append(b, '\n'), 0o644); err != nil {
		d.cfg.Logger.Warn("write kubernetes record", "run", r.runID, "error", err.Error())
	}
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
