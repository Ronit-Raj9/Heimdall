package compose

import (
	"bufio"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Limits of one compose file.
const (
	maxServices        = 16
	maxFilesPerService = 2000
	// Bench caps every service at these (environments/services.go); a
	// compose file may lower them, never raise them.
	benchMemoryBytes = 4 << 30
	benchPids        = 1024
	minReadyS        = 10
	maxReadyS        = 600
)

// Workspace conventions, in the order they are tried when no service says
// x-heimdall workspace: true.
const (
	// HarborMain is the service Harbor runs the agent in (harbor 0.23.0,
	// harbor/constants.py MAIN_SERVICE_NAME).
	HarborMain = "main"
	// TerminalBenchClient is the agent's service in Terminal-Bench 1 compose
	// files (harbor 0.23.0, harbor/mappers/terminal_bench.py takes "client",
	// else the first service, as the main service).
	TerminalBenchClient = "client"
)

var (
	hostNameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	aliasRE    = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9_.-]{0,62})$`)
	workdirRE  = regexp.MustCompile(`(?i)^\s*WORKDIR\s+(\S+)\s*$`)
	fromRE     = regexp.MustCompile(`(?i)^\s*FROM\s+`)
)

// Result is a compose file mapped onto the bench's model.
type Result struct {
	// Environment is the task's own image: the workspace service's, merged
	// under what task.yaml's environment already sets.
	Environment *schemas.TaskEnvironment
	// Services are the other services, dependencies first.
	Services []schemas.ServiceSpec
	// Workspace is the compose name of the workspace service, empty when the
	// file has none and task.yaml's environment is the workspace.
	Workspace string
	Findings  []Finding
}

// Refused returns the refused findings.
func (r *Result) Refused() []Finding { return filterLevel(r.Findings, LevelRefused) }

// Notes returns the changed and ignored findings.
func (r *Result) Notes() []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Level != LevelRefused {
			out = append(out, f)
		}
	}
	return out
}

// Expand parses the compose file rel inside taskDir and maps it. env is
// the task.yaml environment (nil when it sets none): what it sets wins, and
// when it names an image or Dockerfile the file may leave out the workspace
// service.
func Expand(taskDir, rel string, env *schemas.TaskEnvironment, opts Options) (*Result, error) {
	p, err := Parse(taskDir, rel, opts)
	if err != nil {
		return nil, err
	}
	return p.Map(env), nil
}

// Map turns the parsed file into a task environment and services.
func (p *Project) Map(env *schemas.TaskEnvironment) *Result {
	m := &mapper{p: p, res: &Result{}}
	m.run(env)
	m.res.Findings = append(append([]Finding{}, p.Findings...), m.findings...)
	return m.res
}

type mapper struct {
	p        *Project
	res      *Result
	findings []Finding
	active   []*Service
	byName   map[string]*Service
}

func (m *mapper) add(level, where, format string, args ...any) {
	m.findings = append(m.findings, Finding{Level: level, Where: where, Message: fmt.Sprintf(format, args...)})
}

func (m *mapper) refuse(where, format string, args ...any) {
	m.add(LevelRefused, where, format, args...)
}

func (m *mapper) run(env *schemas.TaskEnvironment) {
	p := m.p
	m.byName = map[string]*Service{}
	for _, s := range p.Services {
		if len(s.Profiles) > 0 && !overlaps(s.Profiles, p.Heimdall.Profiles) {
			m.add(LevelIgnored, "services."+s.Name+".profiles", "profile %s is not active (x-heimdall profiles), so the service is not started, as docker compose leaves it out without --profile", strings.Join(s.Profiles, ", "))
			continue
		}
		m.active = append(m.active, s)
		m.byName[s.Name] = s
	}
	hasImage := env != nil && (env.Image != "" || env.Dockerfile != "")
	ws := m.workspace(hasImage)
	if ws == nil && !hasImage {
		if len(m.active) > 0 {
			m.refuse("services", "no workspace service: mark the service the agent works in with x-heimdall: {workspace: true} (or name it %s, as Harbor does)", HarborMain)
		}
		return
	}
	others := make([]*Service, 0, len(m.active))
	for _, s := range m.active {
		if s != ws {
			others = append(others, s)
		}
	}
	if len(others) > maxServices {
		m.refuse("services", "%d services besides the workspace; at most %d", len(others), maxServices)
		return
	}
	m.checkDeps(ws)
	runOnce := m.runOnce(ws)
	order, ok := m.order(others)
	if !ok {
		return
	}
	ids := map[string]string{}
	for _, s := range order {
		spec, ok := m.service(s, runOnce[s.Name])
		if !ok {
			continue
		}
		if prev, dup := ids[spec.ID]; dup {
			m.refuse("services."+s.Name, "its host name %q is also the host name of service %s", spec.ID, prev)
			continue
		}
		ids[spec.ID] = s.Name
		m.res.Services = append(m.res.Services, spec)
	}
	m.res.Environment = m.environment(ws, env, hasImage)
}

func overlaps(a, b []string) bool {
	for _, x := range a {
		if slices.Contains(b, x) {
			return true
		}
	}
	return false
}

// workspace picks the agent's service: the one marked x-heimdall workspace,
// else Harbor's main, else Terminal-Bench's client. With none, task.yaml's
// own image is the workspace and every service of the file runs beside it.
func (m *mapper) workspace(hasImage bool) *Service {
	var marked []*Service
	for _, s := range m.active {
		if s.X.Workspace {
			marked = append(marked, s)
		}
	}
	switch {
	case len(marked) == 1:
		m.res.Workspace = marked[0].Name
		return marked[0]
	case len(marked) > 1:
		names := make([]string, 0, len(marked))
		for _, s := range marked {
			names = append(names, s.Name)
		}
		m.refuse("services", "services %s are all marked x-heimdall workspace; mark exactly one", strings.Join(names, ", "))
		return nil
	}
	for _, conv := range []struct{ name, why string }{
		{HarborMain, "Harbor's convention"},
		{TerminalBenchClient, "Terminal-Bench's convention"},
	} {
		if s := m.byName[conv.name]; s != nil {
			m.res.Workspace = s.Name
			if m.p.opts.Conventions != ConventionsHarbor || conv.name != HarborMain {
				m.add(LevelChanged, "services."+s.Name, "taken as the agent's workspace by %s (mark it x-heimdall: {workspace: true} to say so)", conv.why)
			}
			return s
		}
	}
	if hasImage && len(m.active) > 0 {
		m.add(LevelChanged, "services", "no service is the workspace: the agent runs in task.yaml's environment image and every service of the file runs beside it")
	}
	return nil
}

// checkDeps refuses dependencies the bench cannot order: unknown or inactive
// services, a service that waits for the workspace (which starts last), and
// service_healthy on a service without a healthcheck.
func (m *mapper) checkDeps(ws *Service) {
	all := map[string]bool{}
	for _, s := range m.p.Services {
		all[s.Name] = true
	}
	for _, s := range m.active {
		for i, d := range s.DependsOn {
			at := fmt.Sprintf("services.%s.depends_on[%d]", s.Name, i)
			dep := m.byName[d.Service]
			switch {
			case dep == nil && all[d.Service]:
				m.refuse(at, "depends on %s, whose profile is not active", d.Service)
			case dep == nil:
				m.refuse(at, "depends on %s, which the file does not declare", d.Service)
			case dep == ws:
				m.refuse(at, "depends on the workspace service %s; the agent's container starts after every service", d.Service)
			case d.Condition == CondHealthy && (dep.Health == nil || dep.Health.Disabled):
				m.refuse(at, "waits for %s to be healthy, but %s has no healthcheck", d.Service, d.Service)
			}
		}
	}
}

// runOnce names the one-shot services: marked run_once, or waited for with
// service_completed_successfully by any dependant.
func (m *mapper) runOnce(ws *Service) map[string]bool {
	out := map[string]bool{}
	for _, s := range m.active {
		if s.X.RunOnce {
			out[s.Name] = true
		}
		for _, d := range s.DependsOn {
			if d.Condition == CondCompleted {
				out[d.Service] = true
			}
		}
	}
	return out
}

// order sorts services so that every dependency starts first; ties keep
// file order. A cycle is refused.
func (m *mapper) order(svcs []*Service) ([]*Service, bool) {
	state := map[string]int{} // 0 new, 1 visiting, 2 done
	var out []*Service
	in := map[string]*Service{}
	for _, s := range svcs {
		in[s.Name] = s
	}
	var visit func(s *Service, trail []string) bool
	visit = func(s *Service, trail []string) bool {
		switch state[s.Name] {
		case 1:
			m.refuse("services."+s.Name+".depends_on", "dependency cycle: %s", strings.Join(append(trail, s.Name), " -> "))
			return false
		case 2:
			return true
		}
		state[s.Name] = 1
		for _, d := range s.DependsOn {
			if dep := in[d.Service]; dep != nil && !visit(dep, append(trail, s.Name)) {
				return false
			}
		}
		state[s.Name] = 2
		out = append(out, s)
		return true
	}
	for _, s := range svcs {
		if !visit(s, nil) {
			return nil, false
		}
	}
	return out, true
}

// serviceID makes a compose service name a host name: lower case, '_' and
// '.' as '-'.
func serviceID(name string) string {
	return strings.NewReplacer("_", "-", ".", "-").Replace(strings.ToLower(name))
}

// service maps one non-workspace service.
func (m *mapper) service(s *Service, runOnce bool) (schemas.ServiceSpec, bool) {
	at := "services." + s.Name
	id := serviceID(s.Name)
	switch {
	case !hostNameRE.MatchString(id):
		m.refuse(at, "the name %q cannot be made a host name (letters, digits, '-', '_' or '.', at most 63)", s.Name)
		return schemas.ServiceSpec{}, false
	case reservedIDs[id]:
		m.refuse(at, "the name %q is reserved for the bench's own services", s.Name)
		return schemas.ServiceSpec{}, false
	}
	spec := schemas.ServiceSpec{ID: id, Role: s.X.Role, Env: map[string]string{}, Hidden: s.X.Hidden, Proxy: s.X.Proxy, Export: s.X.Export}
	x := &schemas.ComposeService{Name: s.Name, User: s.User, Workdir: s.WorkingDir, RunOnce: runOnce,
		// Harbor runs test.sh inside main, which reaches every service, so
		// under Harbor's conventions the verifier reaches them too.
		Verifier: s.X.Verifier || m.p.Heimdall.VerifierServices || m.p.opts.Conventions == ConventionsHarbor}
	switch {
	case s.Build != nil:
		spec.Build = &schemas.ImageSpec{Dockerfile: s.Build.Dockerfile, Context: s.Build.Context}
		if s.Image != "" {
			m.add(LevelIgnored, at+".image", "the image is built from build; the tag %q is not used", s.Image)
		}
	case s.Image != "":
		spec.Image = s.Image
	default:
		m.refuse(at, "needs an image or a build")
		return spec, false
	}
	if s.HasCommand {
		spec.Argv = s.Command
	}
	if s.HasEntrypoint {
		x.Entrypoint, x.EntrypointSet = s.Entrypoint, true
	}
	for k, v := range s.Env {
		spec.Env[k] = v
	}
	if len(spec.Env) == 0 {
		spec.Env = nil
	}
	if len(s.Ports) > 0 {
		spec.Port = s.Ports[0]
		if len(s.Ports) > 1 {
			m.add(LevelChanged, at+".ports", "readiness without a healthcheck checks port %d only; the other ports are reachable as well", s.Ports[0])
		}
	}
	if s.Health != nil && !s.Health.Disabled && !runOnce {
		spec.Ready = &schemas.ReadyCheck{Argv: s.Health.Argv, TimeoutS: readyTimeout(s.Health)}
	}
	if s.X.ReadyTimeoutS > 0 {
		if spec.Ready != nil {
			spec.Ready.TimeoutS = s.X.ReadyTimeoutS
		} else {
			m.add(LevelIgnored, at+".x-heimdall.ready_timeout_s", "applies only to a healthcheck; without one the default 60 s port or running check is used")
		}
	}
	if runOnce {
		for i, e := range spec.Export {
			if len(e.Argv) > 0 {
				m.refuse(fmt.Sprintf("%s.x-heimdall.export[%d]", at, i), "a one-shot service has exited when state is exported, so only path exports work")
			}
		}
		if spec.Port > 0 || spec.Role == schemas.ServiceRoleTool || spec.Role == schemas.ServiceRoleMCP {
			m.refuse(at, "is one-shot (a dependant waits for service_completed_successfully), so it cannot also serve a port or tools")
		}
		spec.Port = 0
	}
	m.mounts(s, &spec, x, at)
	m.caps(s, x, at)
	x.Aliases = m.aliases(s, id, at)
	spec.Compose = x
	return spec, true
}

// readyTimeout is how long the bench waits for a healthcheck to pass: the
// start period plus every retry's interval and timeout, as docker would
// take to call the service unhealthy, kept between 10 s and 10 minutes.
func readyTimeout(h *Health) int {
	retries := h.Retries
	if retries <= 0 {
		retries = 3
	}
	t := int(h.StartPeriod+float64(retries)*(h.Interval+h.Timeout)) + 1
	return min(max(t, minReadyS), maxReadyS)
}

// mounts maps volumes entries: binds inside the task folder become copied
// files, named and anonymous volumes per-run volumes, tmpfs mounts tmpfs.
func (m *mapper) mounts(s *Service, spec *schemas.ServiceSpec, x *schemas.ComposeService, at string) {
	files := 0
	for i, mt := range s.Mounts {
		mat := fmt.Sprintf("%s.volumes[%d]", at, i)
		if mt.Harness != "" {
			m.add(LevelIgnored, mat, "%s", mt.Harness)
			continue
		}
		switch mt.Type {
		case "tmpfs":
			t := mt.Target
			if mt.Source != "" {
				t += ":" + mt.Source
			}
			x.Tmpfs = append(x.Tmpfs, t)
		case "volume":
			if mt.Source != "" && !m.p.Volumes[mt.Source] {
				m.refuse(mat, "volume %q is not declared under the top-level volumes", mt.Source)
				continue
			}
			x.Volumes = append(x.Volumes, schemas.ComposeVolume{Name: mt.Source, Target: mt.Target, ReadOnly: mt.ReadOnly})
		case "bind":
			if spec.Files == nil {
				spec.Files = map[string]string{}
			}
			n, err := bindFiles(m.p.TaskDir, mt.Source, mt.Target, spec.Files)
			if err != nil {
				m.refuse(mat, "%v", err)
				continue
			}
			files += n
			if files > maxFilesPerService {
				m.refuse(mat, "binds over %d files into one service; mount a smaller folder or bake the files into the image", maxFilesPerService)
				return
			}
			if !mt.ReadOnly {
				m.add(LevelChanged, mat, "%s is copied into the container before it starts; writes stay in the container and never reach the task folder", mt.Source)
			}
		}
	}
	x.Tmpfs = append(x.Tmpfs, s.Tmpfs...)
}

// bindFiles adds the regular files under src (relative to taskDir) to files
// at their container paths under target. Symlinks are skipped, as the
// service file copy skips them.
func bindFiles(taskDir, src, target string, files map[string]string) (int, error) {
	abs, err := utils.ResolveInside(taskDir, src)
	if err != nil {
		return 0, err
	}
	n := 0
	err = filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 || !d.Type().IsRegular() && !d.IsDir() {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		r, err := filepath.Rel(abs, p)
		if err != nil {
			return err
		}
		dst := target
		if r != "." {
			dst = path.Join(target, filepath.ToSlash(r))
		}
		files[dst] = path.Join(src, filepath.ToSlash(r))
		if r == "." {
			files[dst] = src
		}
		n++
		if n > maxFilesPerService {
			return fmt.Errorf("%s holds over %d files", src, maxFilesPerService)
		}
		return nil
	})
	return n, err
}

// caps lowers the bench's resource caps where the file asks for less.
func (m *mapper) caps(s *Service, x *schemas.ComposeService, at string) {
	if s.MemoryBytes > 0 {
		x.MemoryBytes = s.MemoryBytes
		if s.MemoryBytes > benchMemoryBytes {
			x.MemoryBytes = 0
			m.add(LevelChanged, at+".mem_limit", "capped at the bench's 4 GiB per service")
		}
	}
	if s.Pids > 0 {
		x.Pids = s.Pids
		if s.Pids > benchPids {
			x.Pids = 0
			m.add(LevelChanged, at+".pids_limit", "capped at the bench's %d processes per service", benchPids)
		}
	}
	x.CPUs = s.CPUs
}

// aliases are the extra names the service answers to: its compose name
// when the id differs, its hostname and its network aliases.
func (m *mapper) aliases(s *Service, id, at string) []string {
	var out []string
	for _, a := range append([]string{s.Name, s.Hostname}, s.Aliases...) {
		if a == "" || a == id || slices.Contains(out, a) {
			continue
		}
		if !aliasRE.MatchString(a) || reservedIDs[strings.ToLower(a)] {
			m.refuse(at, "alias %q is not a usable host name", a)
			continue
		}
		out = append(out, a)
	}
	return out
}

// environment maps the workspace service onto the task environment: its
// image or build, working_dir as the workspace path, root when it runs as
// root, and its variables, each under what task.yaml already sets.
func (m *mapper) environment(ws *Service, env *schemas.TaskEnvironment, hasImage bool) *schemas.TaskEnvironment {
	out := &schemas.TaskEnvironment{}
	if env != nil {
		*out = *env
		out.Env = nil
		if len(env.Env) > 0 {
			out.Env = map[string]string{}
			for k, v := range env.Env {
				out.Env[k] = v
			}
		}
	}
	out.BuildNetwork = out.BuildNetwork || m.p.Heimdall.BuildNetwork
	if ws == nil {
		return out
	}
	at := "services." + ws.Name
	switch {
	case hasImage && (ws.Build != nil || ws.Image != ""):
		if m.p.opts.Conventions != ConventionsHarbor {
			m.add(LevelChanged, at, "the workspace image comes from task.yaml's environment; the service's image and build are not used")
		}
	case ws.Build != nil:
		out.Dockerfile, out.Context = ws.Build.Dockerfile, ws.Build.Context
	case ws.Image != "":
		out.Image = ws.Image
	default:
		m.refuse(at, "the workspace service needs an image or a build")
	}
	if out.WorkspacePath == "" {
		out.WorkspacePath = ws.WorkingDir
	}
	if out.WorkspacePath == "" && out.Dockerfile != "" {
		out.WorkspacePath = dockerfileWorkdir(m.p.TaskDir, out.Dockerfile)
	}
	if out.WorkspacePath == "" || out.WorkspacePath == "/" {
		m.refuse(at+".working_dir", "the workspace service needs a working_dir (or a literal WORKDIR in its Dockerfile): the folder the agent works in")
	}
	switch ws.User {
	case "":
	case "root", "0", "0:0", "root:root":
		out.User = utils.TaskUserRoot
	default:
		m.add(LevelChanged, at+".user", "the agent runs as uid 1000; only root is honoured for the workspace")
	}
	for _, k := range sortedKeys(ws.Env) {
		if strings.HasPrefix(k, "BENCH_") || strings.HasPrefix(k, "AGENT_") || strings.HasPrefix(k, "OTEL_") {
			m.refuse(at+".environment."+k, "names starting BENCH_, AGENT_ or OTEL_ are the bench's own variables for the agent")
			continue
		}
		if out.Env == nil {
			out.Env = map[string]string{}
		}
		if _, set := out.Env[k]; !set {
			out.Env[k] = ws.Env[k]
		}
	}
	if ws.HasCommand || ws.HasEntrypoint {
		if !isSleep(ws.Command) || ws.HasEntrypoint {
			m.add(LevelIgnored, at+".command", "the agent's own command runs in the workspace container")
		}
	}
	if len(ws.Ports) > 0 {
		m.add(LevelIgnored, at+".ports", "the workspace's ports are not published, and other services cannot call the agent's container by name")
	}
	if ws.Health != nil {
		m.add(LevelIgnored, at+".healthcheck", "the workspace container is the agent's; it is not checked")
	}
	if ws.MemoryBytes > 0 || ws.CPUs > 0 || ws.Pids > 0 {
		m.add(LevelIgnored, at+".deploy", "the agent's budgets (budgets.cpus, mem_gb) apply to the workspace container")
	}
	if len(ws.Tmpfs) > 0 || ws.Hostname != "" || len(ws.Aliases) > 0 {
		m.add(LevelIgnored, at, "tmpfs, hostname and aliases of the workspace are not applied")
	}
	if ws.X.Verifier || ws.X.Role != "" || len(ws.X.Export) > 0 || ws.X.Hidden || ws.X.RunOnce {
		m.refuse(at+".x-heimdall", "verifier, role, export, hidden and run_once apply to services, not to the workspace")
	}
	for i, mt := range ws.Mounts {
		mat := fmt.Sprintf("%s.volumes[%d]", at, i)
		switch {
		case mt.Harness != "":
			m.add(LevelIgnored, mat, "%s", mt.Harness)
		case mt.Type == "bind" && mt.Target == out.WorkspacePath:
			m.add(LevelChanged, mat, "dropped: the workspace is seeded from %s in the image, so the code must be in the image (COPY it in the Dockerfile)", out.WorkspacePath)
		case mt.Type == "tmpfs":
			m.add(LevelIgnored, mat, "the agent's container has its own tmpfs mounts")
		default:
			m.refuse(mat, "the workspace container mounts only the bench's workspace; put shared data in a service, or COPY the files in the Dockerfile")
		}
	}
	return out
}

func isSleep(cmd []string) bool {
	s := strings.Join(cmd, " ")
	return s == "sh -c sleep infinity" || s == "sleep infinity" || s == "tail -f /dev/null"
}

// dockerfileWorkdir is the last literal WORKDIR of the Dockerfile's final
// stage, or "" when it sets none or uses a variable.
func dockerfileWorkdir(taskDir, rel string) string {
	b, err := utils.ReadFileInside(taskDir, rel)
	if err != nil {
		return ""
	}
	wd := ""
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	for sc.Scan() {
		line := sc.Text()
		if fromRE.MatchString(line) {
			wd = ""
			continue
		}
		if mm := workdirRE.FindStringSubmatch(line); mm != nil {
			v := strings.Trim(mm[1], `"'`)
			switch {
			case strings.Contains(v, "$"):
				wd = ""
			case path.IsAbs(v):
				wd = path.Clean(v)
			case wd != "":
				wd = path.Join(wd, v)
			}
		}
	}
	return wd
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func dedupe(in []string) []string {
	var out []string
	for _, s := range in {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// VerifierServices names the services of t the verifier may reach by name
// while it grades (x-heimdall verifier, or verifier_services for the whole
// file). It is pure.
func VerifierServices(t *schemas.Task) []string {
	if t == nil {
		return nil
	}
	var out []string
	for _, sv := range t.Services {
		if sv.Compose != nil && sv.Compose.Verifier {
			out = append(out, sv.ID)
		}
	}
	return out
}
