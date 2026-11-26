package sandbox

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// DriverVersion is the sandbox driver's own version, recorded in each run's
// sandbox.json.
const DriverVersion = "1.0.0"

// FileRecord is the run folder file where the driver records where and how
// the run ran: platform, sandbox id, image, egress, isolation check.
const FileRecord = "sandbox.json"

// Paths inside the sandbox. benchDir is root's own folder (mode 0700): the
// agent, which runs as another user, can neither read the launch
// environment nor touch its own captured output or exit code.
const (
	benchDir    = "/tmp/.heimdall"
	srcTar      = benchDir + "/src.tar"
	populateSh  = benchDir + "/populate.sh"
	launchSh    = benchDir + "/launch.sh"
	agentEnv    = benchDir + "/agent.env"
	exportSh    = benchDir + "/export.sh"
	outDir      = benchDir + "/out"
	stdoutFile  = outDir + "/stdout"
	stderrFile  = outDir + "/stderr"
	exitFile    = outDir + "/exit"
	pidFile     = outDir + "/pid"
	diffFile    = benchDir + "/workspace.diff"
	setupWindow = 45 * time.Minute // added to the wall budget for the sandbox's own lifetime
)

// Timeouts of the run's steps.
const (
	createTimeout = 30 * time.Minute // a first build or pull of the image
	stepTimeout   = 5 * time.Minute
	killGrace     = 30 * time.Second
	defaultPoll   = 2 * time.Second
)

// ImageConfig is the part of an image's config the launch needs.
type ImageConfig struct {
	Env        []string `json:"Env"`
	Entrypoint []string `json:"Entrypoint"`
	Cmd        []string `json:"Cmd"`
}

func (c ImageConfig) path() string {
	for _, kv := range c.Env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok && v != "" {
			return v
		}
	}
	return environments.DefaultPath
}

// DriverConfig configures a sandbox driver.
type DriverConfig struct {
	Platform Platform
	// Local is the Docker driver on this machine: image ids and registry
	// digests are read there (the images are built and pushed from here).
	Local environments.Driver
	// AgentGatewayURL is gateway.agent_url, what the agent configs tell
	// agents; PublicGatewayURL is gateway.public_url, how a sandbox in the
	// cloud reaches the same gateway. Every agent variable that names the
	// former is rewritten to the latter.
	AgentGatewayURL  string
	PublicGatewayURL string
	// HelperImage is a registry image with python3, used by the egress
	// probe that the gateway preflight runs (egress.go).
	HelperImage string
	// Install is this install's id, set as a label on every sandbox.
	Install        string
	OutputCapBytes int64
	// ImageConfig reads an image's config; nil reads it from the local
	// Docker daemon (docker image inspect).
	ImageConfig func(ctx context.Context, ref string) (*ImageConfig, error)
	// RemoteImage maps a local image to the reference the platform pulls;
	// nil uses the image's registry digest from the local daemon.
	RemoteImage func(ctx context.Context, ref string) (string, error)
	Docker      utils.Docker
	Logger      schemas.Logger
	// PollEvery is how often the driver asks whether the agent ended.
	PollEvery time.Duration
	// GPUType is the platform GPU a GPU task's sandbox gets (Z05, gpu.go):
	// a Modal GPU name such as L4 or H100. "" picks the type of the GPUs
	// the run holds, else the cheapest type with the memory the task asks
	// for. Platforms that are not GPUCapable refuse GPU tasks.
	GPUType string
}

// Driver runs each run in one sandbox of the organization's platform. It
// is an environments.Placement: it runs the docker_workspace plan.
type Driver struct {
	cfg DriverConfig

	mu    sync.Mutex
	boxes map[string]*Box // run id -> its sandbox
}

var (
	_ environments.Driver    = (*Driver)(nil)
	_ environments.Placement = (*Driver)(nil)
)

// NewDriver returns the driver with defaults filled.
func NewDriver(cfg DriverConfig) (*Driver, error) {
	if cfg.Platform == nil {
		return nil, errors.New("sandbox driver: no platform client")
	}
	if cfg.Local == nil {
		return nil, errors.New("sandbox driver: no local driver for image metadata")
	}
	if cfg.OutputCapBytes <= 0 {
		cfg.OutputCapBytes = environments.DefaultOutputCap
	}
	if cfg.Logger == nil {
		cfg.Logger = schemas.NoopLogger{}
	}
	if cfg.PollEvery <= 0 {
		cfg.PollEvery = defaultPoll
	}
	return &Driver{cfg: cfg, boxes: map[string]*Box{}}, nil
}

// GetName implements schemas.Part: the environment type the suite names.
func (d *Driver) GetName() string { return environments.DockerWorkspaceType }

// GetVersion implements schemas.Part.
func (d *Driver) GetVersion() string { return d.cfg.Local.GetVersion() }

// Driver implements environments.Driver: the platform name.
func (d *Driver) Driver() string { return d.cfg.Platform.Name() }

// PlansAs implements environments.Placement.
func (d *Driver) PlansAs() string { return environments.DriverDocker }

// Platform is the driver's platform client.
func (d *Driver) Platform() Platform { return d.cfg.Platform }

// EvalIdentity is what the run's handle and the gateway manifest record as
// the evaluation network: the platform's egress rule toward the gateway.
func (d *Driver) EvalIdentity() string {
	return "sandbox:" + d.cfg.Platform.Name() + "@" + gatewayHost(d.cfg.PublicGatewayURL)
}

// ImageID implements engine.ImageInspector through the local daemon.
func (d *Driver) ImageID(ctx context.Context, ref string) (string, error) {
	if ins, ok := d.cfg.Local.(interface {
		ImageID(context.Context, string) (string, error)
	}); ok {
		return ins.ImageID(ctx, ref)
	}
	return "", errors.New("local driver cannot inspect images")
}

// ImageRepoDigests implements engine.ImageDigester through the local daemon.
func (d *Driver) ImageRepoDigests(ctx context.Context, ref string) ([]string, error) {
	if dg, ok := d.cfg.Local.(interface {
		ImageRepoDigests(context.Context, string) ([]string, error)
	}); ok {
		return dg.ImageRepoDigests(ctx, ref)
	}
	return nil, nil
}

// ImageRoles implements engine.ImageRoler: the same roles as the local
// driver, so the manifest hashes as the server planned it.
func (d *Driver) ImageRoles() map[string]string {
	if r, ok := d.cfg.Local.(interface{ ImageRoles() map[string]string }); ok {
		return r.ImageRoles()
	}
	return nil
}

func gatewayHost(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// Refusals implements environments.Driver. It is pure: the platform-free
// refusals of Refuse plus the two that depend on this driver (a public
// gateway URL and whether the platform can place a GPU task).
func (d *Driver) Refusals(s environments.DriverSlot) []string {
	out := Refuse(s)
	if strings.TrimSpace(d.cfg.PublicGatewayURL) == "" {
		out = append(out, RefuseNoPublicGateway)
	}
	if t := s.Task; t != nil {
		if n := t.GPUCount(); n > 0 {
			if gc, ok := d.cfg.Platform.(GPUCapable); !ok {
				out = append(out, RefuseGPU)
			} else if n > MaxGPUsPerSandbox {
				out = append(out, fmt.Sprintf(RefuseGPUFit, fmt.Sprintf("the task asks for %d GPUs and one sandbox takes at most %d", n, MaxGPUsPerSandbox)))
			} else if _, err := gpuTypeFor(gc.GPUTypes(), d.cfg.GPUType, nil, t.Resources.GPU); err != nil {
				out = append(out, fmt.Sprintf(RefuseGPUFit, err.Error()))
			}
		}
	}
	sort.Strings(out)
	return dedupe(out)
}

// isZeroTaskEnv reports whether a task environment, its variables left
// out, sets nothing: variables alone reach the agent's launch.
func isZeroTaskEnv(e schemas.TaskEnvironment) bool {
	return reflect.DeepEqual(e, schemas.TaskEnvironment{})
}

func dedupe(in []string) []string {
	var out []string
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}

// runRef names a run.
type runRef struct{ expID, runID string }

func (d *Driver) ref(rc *schemas.RunContext) (runRef, error) {
	expID, runID, err := environments.RunIDs(rc)
	return runRef{expID: expID, runID: runID}, err
}

// Create implements schemas.Environment. It records the protected and notes
// hashes from the trusted host task folder, as the Docker driver does. The
// sandbox itself is created in Exec, where the plan names its image, and
// Exec sets the handle's baseline commit then.
func (d *Driver) Create(ctx context.Context, rc *schemas.RunContext) (*schemas.EnvironmentHandle, error) {
	if err := environments.CheckFrozenDriver(rc, d.PlansAs()); err != nil {
		return nil, err
	}
	r, err := d.ref(rc)
	if err != nil {
		return nil, err
	}
	if rc.Task == nil || rc.Task.Dir == "" {
		return nil, fmt.Errorf("create workspace for run %s: run context has no task folder", r.runID)
	}
	if why := d.Refusals(environments.DriverSlot{Experiment: rc.Experiment, Suite: rc.Suite, Task: rc.Task, Probe: rc.Probe, Agent: rc.Agent}); len(why) > 0 {
		return nil, fmt.Errorf("create workspace for run %s: the %s platform cannot run it: %s", r.runID, d.Driver(), strings.Join(why, "; "))
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
	h := &schemas.EnvironmentHandle{
		Network:    d.EvalIdentity(),
		Workdir:    environments.Workdir(rc),
		Labels:     d.labels(r),
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
	return h, nil
}

func (d *Driver) labels(r runRef) map[string]string {
	l := map[string]string{"heimdall.run": r.runID, "heimdall.exp": r.expID, "heimdall.driver": DriverVersion}
	if d.cfg.Install != "" {
		l["heimdall.install"] = d.cfg.Install
	}
	return l
}

// record is sandbox.json.
type record struct {
	DriverVersion string   `json:"driver_version"`
	Platform      string   `json:"platform"`
	SandboxID     string   `json:"sandbox_id,omitempty"`
	Image         string   `json:"image,omitempty"`
	RemoteImage   string   `json:"remote_image,omitempty"`
	ImageID       string   `json:"image_id,omitempty"`
	Network       string   `json:"network"`
	AllowHosts    []string `json:"allow_hosts,omitempty"`
	Isolation     string   `json:"isolation,omitempty"` // blocked | open | untested
	BaselineRef   string   `json:"baseline_ref,omitempty"`
	Destroyed     bool     `json:"destroyed,omitempty"`
	Notes         []string `json:"notes,omitempty"`
	GPU           string   `json:"gpu,omitempty"` // Z05: the platform GPU request, e.g. L4 or H100:2
}

func (d *Driver) writeRecord(rc *schemas.RunContext, rec *record) {
	if rc == nil || rc.Dir == "" {
		return
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(rc.Dir, FileRecord), append(b, '\n'), 0o644)
}

func readRecord(rc *schemas.RunContext) *record {
	if rc == nil || rc.Dir == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(rc.Dir, FileRecord))
	if err != nil {
		return nil
	}
	var rec record
	if json.Unmarshal(b, &rec) != nil {
		return nil
	}
	return &rec
}

// user is the agent's uid and gid; root when uid is 0.
type user struct{ uid, gid int }

func parseUser(s string) (user, error) {
	if s == "" {
		s = environments.DefaultUser
	}
	if s == "root" {
		return user{}, nil
	}
	u, g, hasGroup := strings.Cut(s, ":")
	uid, err := strconv.Atoi(u)
	if err != nil || uid < 0 {
		return user{}, fmt.Errorf("plan user %q is not numeric; sandbox runs start agents by uid", s)
	}
	gid := uid
	if hasGroup {
		if gid, err = strconv.Atoi(g); err != nil || gid < 0 {
			return user{}, fmt.Errorf("plan user %q: group is not numeric", s)
		}
	}
	return user{uid: uid, gid: gid}, nil
}

// Exec implements schemas.Environment: it creates the run's sandbox from
// the agent image with the run's egress rule, seeds the workspace in it,
// checks that the internet is unreachable, starts the agent in the
// background and waits for its exit code file under the wall budget, then
// copies its output into the run folder. The sandbox stays for ExportDiff;
// Destroy removes it.
func (d *Driver) Exec(ctx context.Context, rc *schemas.RunContext, plan *schemas.RunPlan) (*schemas.ExecResult, error) {
	r, err := d.ref(rc)
	if err != nil {
		return nil, err
	}
	if rc.Handle == nil || rc.Dir == "" || plan == nil {
		return nil, fmt.Errorf("exec run %s: needs a workspace handle, a run folder and a plan", r.runID)
	}
	fail := func(err error) (*schemas.ExecResult, error) {
		return nil, fmt.Errorf("exec run %s on %s: %w", r.runID, d.Driver(), err)
	}
	switch {
	case rc.Session != nil:
		return fail(errors.New(RefuseSessions))
	case plan.Image == "":
		return fail(errors.New("plan has no image"))
	case len(plan.Dirs) > 0:
		return fail(errors.New(RefuseSkills))
	case len(plan.Egress) > 0:
		return fail(errors.New(RefuseRemote))
	}
	usr, err := parseUser(plan.User)
	if err != nil {
		return fail(err)
	}
	rec := &record{DriverVersion: DriverVersion, Platform: d.Driver(), Image: plan.Image, Network: "none"}
	defer d.writeRecord(rc, rec)
	remote, err := d.resolveImage(ctx, rc, plan.Image, rec)
	if err != nil {
		return fail(err)
	}
	launch, err := d.launch(ctx, environments.WithTaskEnv(rc, plan))
	if err != nil {
		return fail(err)
	}
	spec := Spec{Name: sandboxName(r.runID), Image: remote, Labels: d.labels(r), CPUs: plan.CPUs, MemGB: plan.MemGB,
		Lifetime: wallOf(plan) + setupWindow}
	if n := rc.Task.GPUCount(); n > 0 { // Z05: GPU placement (gpu.go)
		gc, ok := d.cfg.Platform.(GPUCapable)
		if !ok {
			return fail(errors.New(RefuseGPU))
		}
		gt, err := gpuTypeFor(gc.GPUTypes(), d.cfg.GPUType, rc.GPU, rc.Task.Resources.GPU)
		if err != nil {
			return fail(fmt.Errorf(RefuseGPUFit, err.Error()))
		}
		spec.GPU = gpuSpec(gt, n)
		rec.GPU = spec.GPU
	}
	switch n := plan.Network; n {
	case "none":
		spec.Egress = Egress{BlockAll: true}
	default:
		eg, err := d.gatewayEgress()
		if err != nil {
			return fail(err)
		}
		spec.Egress = eg
		rec.Network, rec.AllowHosts = d.EvalIdentity(), append(eg.AllowHosts, eg.AllowCIDRs...)
	}
	cctx, cancel := context.WithTimeout(ctx, createTimeout)
	box, err := d.cfg.Platform.Create(cctx, spec)
	cancel()
	if err != nil {
		return fail(fmt.Errorf("create sandbox: %w", err))
	}
	d.mu.Lock()
	d.boxes[r.runID] = box
	d.mu.Unlock()
	rec.SandboxID = box.ID
	d.writeRecord(rc, rec) // the id is on disk before anything else can fail, for Destroy after a crash
	ref, err := d.seed(ctx, rc, box, usr)
	if err != nil {
		return fail(fmt.Errorf("seed workspace: %w", err))
	}
	rc.Handle.BaselineRef, rec.BaselineRef = ref, ref
	iso, err := d.isolation(ctx, box)
	rec.Isolation = iso
	if err != nil {
		return fail(err)
	}
	if iso == "untested" {
		rec.Notes = append(rec.Notes, "the agent image has no python3, curl, wget or bash to test the egress rule from inside the sandbox; the gateway preflight probe tested it")
	}
	rec.Notes = append(rec.Notes, "observers and the event collector do not run in a cloud sandbox; the diff is exported inside the sandbox after every agent process was stopped")
	environments.WriteCollectorState(rc, "off", "not available on cloud sandbox platforms")
	_ = environments.AppendObserverStatus(rc.Dir,
		schemas.ObserverStatus{Observer: "process", State: "off", Reason: "not available on cloud sandbox platforms"},
		schemas.ObserverStatus{Observer: "workspace", State: "off", Reason: "not available on cloud sandbox platforms"})
	return d.runAgent(ctx, rc, box, plan, launch, usr)
}

func sandboxName(runID string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(runID) {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' {
			b.WriteRune(c)
		} else {
			b.WriteByte('-')
		}
	}
	name := "heimdall-" + b.String()
	if len(name) > 63 {
		name = name[:63]
	}
	return strings.TrimRight(name, "-")
}

func wallOf(plan *schemas.RunPlan) time.Duration {
	if plan.WallS > 0 {
		return time.Duration(plan.WallS) * time.Second
	}
	return environments.DefaultWallS * time.Second
}

// gatewayEgress is the egress rule of a networked run: the public gateway
// host only.
func (d *Driver) gatewayEgress() (Egress, error) {
	host := gatewayHost(d.cfg.PublicGatewayURL)
	if host == "" {
		return Egress{}, errors.New(RefuseNoPublicGateway)
	}
	if ip := net.ParseIP(host); ip != nil {
		bits := "/32"
		if ip.To4() == nil {
			bits = "/128"
		}
		return Egress{AllowCIDRs: []string{ip.String() + bits}}, nil
	}
	return Egress{AllowHosts: []string{host}}, nil
}

// resolveImage checks the local image is the one the experiment froze and
// returns the reference the platform pulls.
func (d *Driver) resolveImage(ctx context.Context, rc *schemas.RunContext, ref string, rec *record) (string, error) {
	if id, err := d.ImageID(ctx, ref); err == nil {
		rec.ImageID = id
		if want := environments.FrozenImageID(rc); want != "" && id != want {
			return "", fmt.Errorf("image %s is %s here, but the experiment froze %s; rebuild it or plan a new experiment", ref, id, want)
		}
	}
	if d.cfg.RemoteImage != nil {
		out, err := d.cfg.RemoteImage(ctx, ref)
		rec.RemoteImage = out
		return out, err
	}
	if strings.Contains(ref, "@sha256:") {
		rec.RemoteImage = ref
		return ref, nil
	}
	digests, err := d.ImageRepoDigests(ctx, ref)
	if err != nil {
		return "", fmt.Errorf("image %s: %w", ref, err)
	}
	repo := ref
	if i := strings.LastIndex(repo, ":"); i > strings.LastIndex(repo, "/") {
		repo = repo[:i]
	}
	sort.Strings(digests)
	for _, dg := range digests {
		if strings.HasPrefix(dg, repo+"@") {
			rec.RemoteImage = dg
			return dg, nil
		}
	}
	if len(digests) > 0 {
		rec.RemoteImage = digests[0]
		return digests[0], nil
	}
	// Fix wave 3 (N216): the image has no registry copy; this plain sentence
	// is what the comparison page shows.
	return "", errors.New(NeedsRegistry(d.Driver()))
}

// agentLaunch is the agent's argv and environment.
type agentLaunch struct {
	argv []string
	env  map[string]string
}

// launch builds the Docker driver's launch for the plan (same PATH with the
// shim folder first, same wrapper for files and long values), resolves the
// image entrypoint the plan keeps, and points every gateway URL at the
// public one.
func (d *Driver) launch(ctx context.Context, plan *schemas.RunPlan) (*agentLaunch, error) {
	img, err := d.imageConfig(ctx, plan.Image)
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
	from := strings.TrimRight(strings.TrimSpace(d.cfg.AgentGatewayURL), "/")
	to := strings.TrimRight(strings.TrimSpace(d.cfg.PublicGatewayURL), "/")
	env := map[string]string{}
	for _, kv := range l.Env {
		k, v, _ := strings.Cut(kv, "=")
		if from != "" && to != "" {
			v = strings.ReplaceAll(v, from, to)
		}
		env[k] = v
	}
	return &agentLaunch{argv: argv, env: env}, nil
}

func (d *Driver) imageConfig(ctx context.Context, ref string) (*ImageConfig, error) {
	if d.cfg.ImageConfig != nil {
		return d.cfg.ImageConfig(ctx, ref)
	}
	res, err := d.cfg.Docker.MustRun(ctx, "image", "inspect", "--format", "{{json .Config}}", ref)
	if err != nil {
		return nil, fmt.Errorf("inspect image %s on this machine (the driver reads PATH and the entrypoint there): %w", ref, err)
	}
	var c ImageConfig
	if err := json.Unmarshal(res.Stdout, &c); err != nil {
		return nil, fmt.Errorf("inspect image %s: parse config: %w", ref, err)
	}
	return &c, nil
}

// run runs a short root command and returns its output; a non-zero exit is
// an error carrying the output.
func (d *Driver) run(ctx context.Context, box *Box, timeout time.Duration, argv ...string) ([]byte, error) {
	res, err := d.cfg.Platform.Exec(ctx, box, Command{Argv: argv, Timeout: timeout})
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return res.Output, fmt.Errorf("%s exited %d: %s", argv[0], res.ExitCode, strings.TrimSpace(tail(res.Output, 2048)))
	}
	return res.Output, nil
}

func tail(b []byte, n int) string {
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return string(b)
}

// seedTail runs after the populate script: the agent's home gets its owner,
// and root's own folder its mode. It prints nothing, so the baseline commit
// stays the last line.
const seedTail = `
mkdir -p %[3]s && chown %[1]d:%[2]d %[3]s && chmod 0700 %[3]s
chmod 1777 /tmp
mkdir -p ` + outDir + ` && chmod 0700 ` + benchDir + `
`

// seed lays the workspace down from the task repo (the same tar the
// Kubernetes driver streams), commits the baseline and returns its id.
func (d *Driver) seed(ctx context.Context, rc *schemas.RunContext, box *Box, u user) (string, error) {
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
	if _, err := d.run(ctx, box, stepTimeout, "/bin/sh", "-c", "mkdir -p /workspace "+benchDir+" && chmod 0700 "+benchDir); err != nil {
		return "", err
	}
	if err := d.cfg.Platform.Upload(ctx, box, srcTar, tarball); err != nil {
		return "", err
	}
	script := environments.PopulateScript + fmt.Sprintf(seedTail, u.uid, u.gid, environments.AgentHome)
	if err := d.cfg.Platform.Upload(ctx, box, populateSh, []byte(script)); err != nil {
		return "", err
	}
	out, err := d.run(ctx, box, stepTimeout, "/bin/sh", "-c",
		"BENCH_DATE="+shellQuote(environments.BaselineDate)+" BENCH_SRC_TAR=1 BENCH_CLEAR=1 /bin/sh "+populateSh+" < "+srcTar+" && rm -f "+srcTar)
	if err != nil {
		return "", err
	}
	return environments.LastRef(out)
}

// isolationScript tests, from inside the sandbox, that the internet is
// unreachable: it prints open, blocked or untested (no tool to test with).
const isolationScript = `reach() {
	if command -v python3 >/dev/null 2>&1; then python3 -I -c 'import socket; socket.create_connection(("1.1.1.1", 443), timeout=3).close()' 2>/dev/null; return $?; fi
	if command -v curl >/dev/null 2>&1; then curl -sk -m 3 -o /dev/null https://1.1.1.1/ 2>/dev/null; return $?; fi
	if command -v wget >/dev/null 2>&1; then wget -q -T 3 --no-check-certificate -O /dev/null https://1.1.1.1/ 2>/dev/null; return $?; fi
	if command -v bash >/dev/null 2>&1 && command -v timeout >/dev/null 2>&1; then timeout 3 bash -c 'exec 3<>/dev/tcp/1.1.1.1/443' 2>/dev/null; return $?; fi
	return 200
}
reach; rc=$?
if [ "$rc" -eq 0 ]; then echo open; elif [ "$rc" -eq 200 ]; then echo untested; else echo blocked; fi
`

// isolation runs the in-sandbox egress check before the agent starts. A
// sandbox that reaches the internet fails the run as infrastructure.
func (d *Driver) isolation(ctx context.Context, box *Box) (string, error) {
	out, err := d.run(ctx, box, time.Minute, "/bin/sh", "-c", isolationScript)
	if err != nil {
		return "", fmt.Errorf("egress check in the sandbox: %w", err)
	}
	switch v := strings.TrimSpace(tail(out, 64)); {
	case strings.HasSuffix(v, "blocked"):
		return "blocked", nil
	case strings.HasSuffix(v, "untested"):
		return "untested", nil
	case strings.HasSuffix(v, "open"):
		return "open", fmt.Errorf("the sandbox reaches the internet (1.1.1.1:443) despite its egress rule; the %s platform does not enforce it, so the run cannot be isolated", d.Driver())
	default:
		return "", fmt.Errorf("egress check in the sandbox printed %q", v)
	}
}

// launchScript is root's wrapper around the agent: it reads and deletes the
// launch environment, drops to the agent's uid (setpriv, or chroot
// --userspec), captures both streams into root's folder and writes the
// exit code last, atomically.
const launchScript = `#!/bin/sh
set -a
. ` + agentEnv + `
set +a
rm -f ` + agentEnv + `
echo $$ > ` + pidFile + `
U=%[1]d; G=%[2]d; WD=%[3]s
if [ "$U" -eq 0 ]; then
	/bin/sh -c 'cd "$1" && shift && exec "$@"' sh "$WD" %[4]s > ` + stdoutFile + ` 2> ` + stderrFile + ` < /dev/null
elif command -v setpriv >/dev/null 2>&1; then
	setpriv --reuid="$U" --regid="$G" --clear-groups -- /bin/sh -c 'cd "$1" && shift && exec "$@"' sh "$WD" %[4]s > ` + stdoutFile + ` 2> ` + stderrFile + ` < /dev/null
elif chroot --help 2>&1 | grep -q userspec; then
	chroot --userspec="$U:$G" / /bin/sh -c 'cd "$1" && shift && exec "$@"' sh "$WD" %[4]s > ` + stdoutFile + ` 2> ` + stderrFile + ` < /dev/null
else
	echo "the agent image has neither setpriv nor chroot --userspec to start the agent as uid $U" > ` + stderrFile + `
	echo 126 > ` + exitFile + `.tmp && mv ` + exitFile + `.tmp ` + exitFile + `
	exit 0
fi
echo $? > ` + exitFile + `.tmp && mv ` + exitFile + `.tmp ` + exitFile + `
`

// killScript stops every agent process: the launcher's process group, then
// everything the agent's uid still runs.
const killScript = `P="$(cat ` + pidFile + ` 2>/dev/null)"
[ -n "$P" ] && kill -KILL -"$P" 2>/dev/null
U=%[1]d; G=%[2]d
if [ "$U" -ne 0 ]; then
	if command -v setpriv >/dev/null 2>&1; then setpriv --reuid="$U" --regid="$G" --clear-groups -- /bin/sh -c 'kill -KILL -1' 2>/dev/null
	elif chroot --help 2>&1 | grep -q userspec; then chroot --userspec="$U:$G" / /bin/sh -c 'kill -KILL -1' 2>/dev/null; fi
fi
exit 0
`

// envFile renders the launch environment as shell assignments.
func envFile(env map[string]string) []byte {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b bytes.Buffer
	for _, k := range keys {
		if !validEnvName(k) {
			continue
		}
		fmt.Fprintf(&b, "%s=%s\n", k, shellQuote(env[k]))
	}
	return b.Bytes()
}

func validEnvName(k string) bool {
	if k == "" {
		return false
	}
	for i, c := range k {
		if !(c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// runAgent starts the agent and waits for it under the wall budget.
func (d *Driver) runAgent(ctx context.Context, rc *schemas.RunContext, box *Box, plan *schemas.RunPlan, l *agentLaunch, u user) (*schemas.ExecResult, error) {
	r, _ := d.ref(rc)
	wd := plan.Workdir
	if wd == "" {
		wd = environments.DefaultWorkdir
	}
	if err := d.cfg.Platform.Upload(ctx, box, agentEnv, envFile(l.env)); err != nil {
		return nil, fmt.Errorf("exec run %s: launch environment: %w", r.runID, err)
	}
	script := fmt.Sprintf(launchScript, u.uid, u.gid, shellQuote(wd), shellJoin(l.argv))
	if err := d.cfg.Platform.Upload(ctx, box, launchSh, []byte(script)); err != nil {
		return nil, fmt.Errorf("exec run %s: launch script: %w", r.runID, err)
	}
	if _, err := d.run(ctx, box, time.Minute, "/bin/sh", "-c",
		"chmod 0600 "+agentEnv+" && chmod 0700 "+launchSh+" && rm -f "+exitFile+" && "+
			"if command -v setsid >/dev/null 2>&1; then setsid /bin/sh "+launchSh+" >/dev/null 2>&1 & else /bin/sh "+launchSh+" >/dev/null 2>&1 & fi"); err != nil {
		return nil, fmt.Errorf("exec run %s: start agent: %w", r.runID, err)
	}
	stdoutPath, stderrPath, _ := environments.StreamPaths(rc)
	res := &schemas.ExecResult{StdoutPath: stdoutPath, StderrPath: stderrPath, Started: time.Now().UTC()}
	wctx := context.WithoutCancel(ctx)
	deadline := time.Now().Add(wallOf(plan))
	code, ended := 0, false
	var interrupted bool
	for !ended {
		c, ok, err := d.exitCode(wctx, box)
		if err != nil {
			d.cfg.Logger.Warn("sandbox: agent state not read; asking again", "run", r.runID, "error", err.Error())
		}
		if ok {
			code, ended = c, true
			break
		}
		if ctx.Err() != nil {
			interrupted = true
			break
		}
		if time.Now().After(deadline) {
			res.TimedOut = true
			break
		}
		t := time.NewTimer(d.cfg.PollEvery)
		select {
		case <-ctx.Done():
		case <-t.C:
		}
		t.Stop()
	}
	if !ended {
		if _, err := d.run(wctx, box, time.Minute, "/bin/sh", "-c", fmt.Sprintf(killScript, u.uid, u.gid)); err != nil {
			d.cfg.Logger.Warn("sandbox: stop agent", "run", r.runID, "error", err.Error())
		}
		gctx, cancel := context.WithTimeout(wctx, killGrace)
		_ = poll(gctx, d.cfg.PollEvery, func() (bool, error) {
			c, ok, _ := d.exitCode(gctx, box)
			if ok {
				code = c
			}
			return ok, nil
		})
		cancel()
		if code == 0 {
			code = 137
		}
	}
	res.Ended = time.Now().UTC()
	res.ExitCode = code
	trunc, err := d.copyStreams(wctx, rc, box)
	res.Truncated = trunc
	if err != nil {
		return res, fmt.Errorf("exec run %s: copy agent output: %w", r.runID, err)
	}
	if interrupted {
		return res, fmt.Errorf("exec run %s: %w", r.runID, ctx.Err())
	}
	return res, nil
}

// exitCode reads the agent's exit code file.
func (d *Driver) exitCode(ctx context.Context, box *Box) (int, bool, error) {
	res, err := d.cfg.Platform.Exec(ctx, box, Command{Argv: []string{"/bin/sh", "-c", "cat " + exitFile + " 2>/dev/null || true"}, Timeout: 30 * time.Second})
	if err != nil {
		return 0, false, err
	}
	v := strings.TrimSpace(string(res.Output))
	if v == "" {
		return 0, false, nil
	}
	c, err := strconv.Atoi(v)
	if err != nil {
		return 0, false, fmt.Errorf("exit code file holds %q", v)
	}
	return c, true, nil
}

// copyStreams copies the captured streams into the run folder: at most the
// cap of each (cut inside the sandbox first), secrets removed.
func (d *Driver) copyStreams(ctx context.Context, rc *schemas.RunContext, box *Box) (bool, error) {
	stdoutPath, stderrPath, _ := environments.StreamPaths(rc)
	secrets := utils.RunSecrets(rc)
	limit := d.cfg.OutputCapBytes
	truncated := false
	for _, s := range []struct{ remote, local string }{{stdoutFile, stdoutPath}, {stderrFile, stderrPath}} {
		capped := s.remote + ".cap"
		if _, err := d.run(ctx, box, time.Minute, "/bin/sh", "-c",
			fmt.Sprintf("touch %s && head -c %d %s > %s", s.remote, limit+1, s.remote, capped)); err != nil {
			return truncated, err
		}
		data, err := d.cfg.Platform.Download(ctx, box, capped, limit+1)
		if err != nil {
			return truncated, err
		}
		f, err := os.OpenFile(s.local, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return truncated, err
		}
		w := environments.NewOutputWriter(f, limit, secrets...)
		_, _ = w.Write(data)
		cerr := w.Close()
		if w.Truncated() {
			truncated = true
		}
		if err := firstErr(cerr, f.Close()); err != nil {
			return truncated, err
		}
	}
	return truncated, nil
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// box returns the run's sandbox: the one this process created, or the one
// sandbox.json names (a resumed run).
func (d *Driver) box(rc *schemas.RunContext, runID string) *Box {
	d.mu.Lock()
	b := d.boxes[runID]
	d.mu.Unlock()
	if b != nil {
		return b
	}
	if rec := readRecord(rc); rec != nil && rec.SandboxID != "" && rec.Platform == d.Driver() {
		return &Box{ID: rec.SandboxID, Platform: rec.Platform, Data: map[string]string{}}
	}
	return nil
}

// ExportDiff implements schemas.Environment with the Docker driver's export
// script, run as root in the sandbox after the agent's processes were
// stopped, then the same path allowlist on this machine.
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
	box := d.box(rc, r.runID)
	if box == nil {
		return nil, fmt.Errorf("export diff for run %s: the run's sandbox is not known to this process", r.runID)
	}
	if err := d.cfg.Platform.Upload(ctx, box, exportSh, []byte(environments.ExportScript)); err != nil {
		return nil, fmt.Errorf("export diff for run %s: %w", r.runID, err)
	}
	if _, err := d.run(ctx, box, stepTimeout, "/bin/sh", "-c",
		"rm -rf /tmp/w && BENCH_BASE="+rc.Handle.BaselineRef+" BENCH_CAP="+strconv.Itoa(environments.MaxDiffBytes)+" /bin/sh "+exportSh+" > "+diffFile); err != nil {
		return nil, fmt.Errorf("export diff for run %s: %w", r.runID, err)
	}
	raw, err := d.cfg.Platform.Download(ctx, box, diffFile, int64(environments.MaxDiffBytes)+1)
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

// Destroy implements schemas.Environment: the run's sandbox is deleted. It
// is safe to call more than once and after a failed Create or Exec.
func (d *Driver) Destroy(ctx context.Context, rc *schemas.RunContext) error {
	r, err := d.ref(rc)
	if err != nil {
		return err
	}
	box := d.box(rc, r.runID)
	if box == nil {
		return nil
	}
	if err := d.cfg.Platform.Destroy(ctx, box); err != nil {
		return fmt.Errorf("destroy run %s: %w", r.runID, err)
	}
	d.mu.Lock()
	delete(d.boxes, r.runID)
	d.mu.Unlock()
	if rec := readRecord(rc); rec != nil {
		rec.Destroyed = true
		d.writeRecord(rc, rec)
	}
	return nil
}

// seedTar is the workspace's starting state as a tar: every file, folder and
// symlink of the repo folder with its mode, in path order, then the notes
// file at its workspace path (the Kubernetes driver's seed, so the
// baseline commit is the same).
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
			b, err := os.ReadFile(p)
			if err != nil {
				return nil, err
			}
			if _, err := tw.Write(b); err != nil {
				return nil, err
			}
		}
	}
	if notesRel != "" {
		b, err := os.ReadFile(notesHost)
		if err != nil {
			return nil, fmt.Errorf("read clean notes: %w", err)
		}
		parts := strings.Split(notesRel, "/")
		for i := 1; i < len(parts); i++ {
			if err := tw.WriteHeader(&tar.Header{Name: strings.Join(parts[:i], "/") + "/", Typeflag: tar.TypeDir, Mode: 0o755, Format: tar.FormatPAX}); err != nil {
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
