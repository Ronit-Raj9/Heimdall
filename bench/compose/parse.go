// Package compose reads the subset of the docker compose file format that
// real multi-container tasks use and maps it onto the bench's own model: one
// service becomes the agent's workspace image (schemas.TaskEnvironment), the
// others become services (schemas.ServiceSpec) on the run's private network.
// It never runs docker compose and does not depend on compose-go: the file is
// read with gopkg.in/yaml.v3 and every key is either mapped, reported as
// changed or ignored, or refused with a reason. docs/multi-container.md lists
// the subset.
package compose

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"gopkg.in/yaml.v3"
)

// Finding levels.
const (
	// LevelRefused is a setting the bench cannot run honestly (privileged
	// mode, a host mount, a host variable). A task with one does not load.
	LevelRefused = "refused"
	// LevelChanged is a setting the bench runs differently from docker
	// compose (a published port kept internal, a read-write bind copied in).
	LevelChanged = "changed"
	// LevelIgnored is a setting that has no effect under the bench (restart,
	// container_name, labels).
	LevelIgnored = "ignored"
)

// Conventions name the variables and the workspace service a compose file
// was written for.
const (
	// ConventionsCompose is a plain compose file: no variable is known
	// unless the file's own .env sets it.
	ConventionsCompose = ""
	// ConventionsHarbor is a task-authored compose file of the Harbor task
	// format (Terminal-Bench 2, SkillsBench): Harbor merges it over its own
	// base file, which defines the workspace service "main", and fills
	// CONTEXT_DIR, MAIN_IMAGE_NAME, CPUS, MEMORY and the log mount
	// variables itself.
	ConventionsHarbor = "harbor"
)

const maxComposeBytes = 1 << 20

// maxExpandedNodes caps the YAML nodes a file may expand to through
// aliases, so a file of nested aliases cannot exhaust memory.
const maxExpandedNodes = 200000

// Finding is one thing the mapping refused, changed or ignored, with where
// in the file it is (for example services.db.ports[0]).
type Finding struct {
	Level   string `json:"level"`
	Where   string `json:"where"`
	Message string `json:"message"`
}

// String implements fmt.Stringer.
func (f Finding) String() string { return f.Where + ": " + f.Level + ": " + f.Message }

// Options configure Parse.
type Options struct {
	Conventions string
}

// Project is a parsed compose file.
type Project struct {
	TaskDir  string // absolute task folder; every path of the file must stay inside it
	File     string // the compose file, relative to TaskDir (slash separated)
	Dir      string // the compose file's folder, relative to TaskDir ("." for the task folder)
	Services []*Service
	Volumes  map[string]bool // declared top-level named volumes
	Heimdall ProjectExtension
	Findings []Finding

	opts   Options
	dotenv map[string]string
}

// ProjectExtension is the file's top-level x-heimdall block.
type ProjectExtension struct {
	// VerifierServices lets the verifier reach every service by name while
	// it grades (ComposeService.Verifier on each).
	VerifierServices bool `yaml:"verifier_services"`
	// BuildNetwork lets image builds use the network (TaskEnvironment.BuildNetwork).
	BuildNetwork bool `yaml:"build_network"`
	// Profiles are the compose profiles to start; services of other
	// profiles are left out, as docker compose does without --profile.
	Profiles []string `yaml:"profiles"`
}

// ServiceExtension is one service's x-heimdall block.
type ServiceExtension struct {
	Workspace     bool                  // this service is the agent's container
	Verifier      bool                  // the verifier may reach it by name
	Role          string                // data | tool | mcp (ServiceSpec.Role)
	Hidden        bool                  // off the agent's network
	Proxy         *bool                 // through the tool proxy
	RunOnce       bool                  // a one-shot service that must exit 0
	ReadyTimeoutS int                   // overrides the timeout derived from the healthcheck
	Export        []schemas.StateExport // state read after the agent exits (ServiceSpec.Export)
}

// Service is one parsed compose service.
type Service struct {
	Name          string
	Image         string
	Build         *Build
	Command       []string
	HasCommand    bool
	Entrypoint    []string
	HasEntrypoint bool
	Env           map[string]string
	DependsOn     []Dependency
	Health        *Health
	Mounts        []Mount
	Ports         []int // internal ports: expose, then the container side of ports
	WorkingDir    string
	User          string
	Hostname      string
	Tmpfs         []string
	Aliases       []string
	Profiles      []string
	MemoryBytes   int64
	CPUs          float64
	Pids          int
	X             ServiceExtension
}

// Build is a service's build section. Context and Dockerfile are relative
// to the task folder.
type Build struct {
	Context    string
	Dockerfile string
}

// Dependency is one depends_on entry.
type Dependency struct {
	Service   string
	Condition string // service_started | service_healthy | service_completed_successfully
}

// Health is a service's healthcheck, translated: Argv runs inside the
// service container and exit 0 means healthy.
type Health struct {
	Argv        []string
	Interval    float64 // seconds
	Timeout     float64
	Retries     int
	StartPeriod float64
	Disabled    bool
}

// Mount is one volumes entry.
type Mount struct {
	Type     string // bind | volume | tmpfs
	Source   string // bind: path relative to the task folder; volume: the volume name (empty for an anonymous volume)
	Target   string
	ReadOnly bool
	Harness  string // why a harness mount is dropped, when it is one (Harbor's log folders, a model provider's credentials)
}

// Dependency conditions.
const (
	CondStarted   = "service_started"
	CondHealthy   = "service_healthy"
	CondCompleted = "service_completed_successfully"
)

// harborVars are the variables Harbor fills in a task's compose file
// (harbor 0.23.0, environments/docker/compose_env.py ComposeInfraEnvVars and
// legacy_log_mount_env_vars). Host paths are unknown here: a mount that uses
// one is Harbor's own log plumbing, which the bench replaces.
var harborVars = map[string]string{
	"CONTEXT_DIR":             ".",
	"MAIN_IMAGE_NAME":         "",
	"PREBUILT_IMAGE_NAME":     "",
	"CPUS":                    "",
	"MEMORY":                  "",
	"ENV_VERIFIER_LOGS_PATH":  "/logs/verifier",
	"ENV_AGENT_LOGS_PATH":     "/logs/agent",
	"ENV_ARTIFACTS_PATH":      "/logs/artifacts",
	"HOST_VERIFIER_LOGS_PATH": "",
	"HOST_AGENT_LOGS_PATH":    "",
	"HOST_ARTIFACTS_PATH":     "",
	// Harbor's Terminal-Bench mapper writes TEST_DIR=${TEST_DIR} for the
	// main service and ENV TEST_DIR=/tests in the Dockerfile; /tests is
	// where Harbor uploads the tests.
	"TEST_DIR": "/tests",
}

// harborHostVars are the harborVars that stand for a host folder.
var harborHostVars = map[string]bool{"HOST_VERIFIER_LOGS_PATH": true, "HOST_AGENT_LOGS_PATH": true, "HOST_ARTIFACTS_PATH": true}

// harnessEnv are agent harness variables that SkillsBench compose files pass
// to the main service so Claude Code can reach a model on Vertex AI. The
// bench gives the agent its model through the gateway, so under Harbor
// conventions they are dropped rather than read from the host.
var harnessEnv = map[string]bool{
	"CLAUDE_CODE_USE_VERTEX": true, "CLOUD_ML_REGION": true, "ANTHROPIC_VERTEX_PROJECT_ID": true, "GOOGLE_APPLICATION_CREDENTIALS": true,
}

// Parse reads the compose file rel (relative to taskDir) and returns the
// parsed project with every finding. An error means the file could not be
// read or is not a compose file at all; anything the bench cannot honour is a
// refused finding instead, so lint can list them all.
func Parse(taskDir, rel string, opts Options) (*Project, error) {
	rel = filepath.ToSlash(rel)
	abs, err := utils.ResolveInside(taskDir, rel)
	if err != nil {
		return nil, fmt.Errorf("compose file: %w", err)
	}
	st, err := os.Stat(abs)
	switch {
	case err != nil:
		return nil, fmt.Errorf("compose file: %w", err)
	case !st.Mode().IsRegular():
		return nil, fmt.Errorf("compose file %s is not a regular file", rel)
	case st.Size() > maxComposeBytes:
		return nil, fmt.Errorf("compose file %s is %d bytes, over the %d byte limit", rel, st.Size(), maxComposeBytes)
	}
	data, err := utils.ReadFileInside(taskDir, rel)
	if err != nil {
		return nil, fmt.Errorf("compose file: %w", err)
	}
	p := &Project{TaskDir: taskDir, File: rel, Dir: path.Dir(rel), Volumes: map[string]bool{}, opts: opts}
	if err := p.loadDotenv(); err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("compose file %s: %w", rel, err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("compose file %s: want a mapping with a services key", rel)
	}
	budget := maxExpandedNodes
	root, err := expandAliases(doc.Content[0], &budget)
	if err != nil {
		return nil, fmt.Errorf("compose file %s: %w", rel, err)
	}
	p.parseTop(root)
	if len(p.Services) == 0 && !p.refused() {
		p.refuse("services", "the file declares no service")
	}
	return p, nil
}

// loadDotenv reads .env beside the compose file, which compose reads for
// interpolation. It is optional.
func (p *Project) loadDotenv() error {
	rel := path.Join(p.Dir, ".env")
	b, err := utils.ReadFileInside(p.TaskDir, rel)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("compose .env: %w", err)
	}
	env, _, err := parseDotenv(string(b))
	if err != nil {
		return fmt.Errorf("compose .env %s: %w", rel, err)
	}
	p.dotenv = env
	return nil
}

// lookup is a variable's value: Harbor's own variables under Harbor
// conventions, else the file's .env. The host environment is never read.
func (p *Project) lookup(name string) (string, bool) {
	if p.opts.Conventions == ConventionsHarbor {
		if v, ok := harborVars[name]; ok {
			return v, true
		}
	}
	v, ok := p.dotenv[name]
	return v, ok
}

func (p *Project) add(level, where, format string, args ...any) {
	p.Findings = append(p.Findings, Finding{Level: level, Where: where, Message: fmt.Sprintf(format, args...)})
}

func (p *Project) refuse(where, format string, args ...any) {
	p.add(LevelRefused, where, format, args...)
}

func (p *Project) refused() bool {
	for _, f := range p.Findings {
		if f.Level == LevelRefused {
			return true
		}
	}
	return false
}

// Refused returns the refused findings.
func (p *Project) Refused() []Finding { return filterLevel(p.Findings, LevelRefused) }

func filterLevel(fs []Finding, level string) []Finding {
	var out []Finding
	for _, f := range fs {
		if f.Level == level {
			out = append(out, f)
		}
	}
	return out
}

// scalar expands one scalar value. A variable compose would take from the
// host is refused at where.
func (p *Project) scalar(n *yaml.Node, where string) (string, []string, bool) {
	if n.Kind != yaml.ScalarNode {
		p.refuse(where, "want a single value, got a %s", kindName(n))
		return "", nil, false
	}
	if n.Tag == "!!null" {
		return "", nil, true
	}
	res := interpolate(n.Value, p.lookup)
	switch {
	case res.err != nil:
		p.refuse(where, "%v", res.err)
		return "", res.used, false
	case len(res.unset) > 0:
		p.refuse(where, "variable %s is not set; docker compose would read it from the host environment, which a run never passes in (set it in the .env beside the compose file, or give a default with ${%s:-value})",
			strings.Join(dedupe(res.unset), ", "), res.unset[0])
		return "", res.used, false
	}
	return res.value, res.used, true
}

func (p *Project) str(n *yaml.Node, where string) string {
	v, _, _ := p.scalar(n, where)
	return v
}

// strList reads a sequence of values, or a single value as a list of one.
func (p *Project) strList(n *yaml.Node, where string) []string {
	if n.Kind == yaml.ScalarNode {
		if n.Tag == "!!null" {
			return nil
		}
		return []string{p.str(n, where)}
	}
	if n.Kind != yaml.SequenceNode {
		p.refuse(where, "want a list, got a %s", kindName(n))
		return nil
	}
	out := make([]string, 0, len(n.Content))
	for i, c := range n.Content {
		out = append(out, p.str(c, fmt.Sprintf("%s[%d]", where, i)))
	}
	return out
}

// isNull reports whether n is YAML null (null, ~ or an empty value), which
// Compose treats as an unset key, unlike an explicit "" or [].
func isNull(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && n.Tag == "!!null"
}

// command reads command or entrypoint: a string split like a shell would,
// or a list taken as it is.
func (p *Project) command(n *yaml.Node, where string) []string {
	if n.Kind == yaml.ScalarNode {
		if isNull(n) {
			return nil
		}
		s := p.str(n, where)
		words, err := splitShell(s)
		if err != nil {
			p.refuse(where, "%v", err)
		}
		return words
	}
	return p.strList(n, where)
}

func (p *Project) boolean(n *yaml.Node, where string) bool {
	s := strings.ToLower(p.str(n, where))
	switch s {
	case "true", "yes", "1", "on":
		return true
	case "false", "no", "0", "off", "":
		return false
	}
	p.refuse(where, "%q is not true or false", s)
	return false
}

func (p *Project) integer(n *yaml.Node, where string) int {
	s := p.str(n, where)
	if s == "" {
		return 0
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		p.refuse(where, "%q is not a whole number", s)
	}
	return v
}

// seconds reads a duration in seconds.
func (p *Project) seconds(n *yaml.Node, where string) float64 {
	s := p.str(n, where)
	if s == "" {
		return 0
	}
	d, err := parseDuration(s)
	if err != nil {
		p.refuse(where, "%v", err)
	}
	return d.Seconds()
}

// mapping walks a mapping node's keys in file order.
func (p *Project) mapping(n *yaml.Node, where string, fn func(key string, v *yaml.Node, at string)) {
	if n.Kind != yaml.MappingNode {
		if n.Kind == yaml.ScalarNode && n.Tag == "!!null" {
			return
		}
		p.refuse(where, "want a mapping, got a %s", kindName(n))
		return
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i].Value
		fn(k, n.Content[i+1], where+"."+k)
	}
}

func kindName(n *yaml.Node) string {
	switch n.Kind {
	case yaml.MappingNode:
		return "mapping"
	case yaml.SequenceNode:
		return "list"
	case yaml.AliasNode:
		return "YAML alias"
	}
	return "value"
}

// parseTop reads the top-level keys.
func (p *Project) parseTop(root *yaml.Node) {
	var services *yaml.Node
	p.mapping(root, "", func(key string, v *yaml.Node, _ string) {
		at := key
		switch {
		case key == "services":
			services = v
		case key == "volumes":
			p.parseVolumes(v)
		case key == "networks":
			p.parseNetworks(v)
		case key == "version" || key == "name":
			p.add(LevelIgnored, at, "has no effect; the bench names everything per run")
		case key == "x-heimdall":
			p.parseProjectExtension(v)
		case strings.HasPrefix(key, "x-"):
			// Extension fields are for anchors and tools; compose ignores them too.
		case key == "secrets" || key == "configs":
			p.refuse(at, "is not supported; put the file inside the task folder and mount it read-only, or use env_file")
		case key == "include" || key == "extends":
			p.refuse(at, "is not supported; write the services into this one file")
		default:
			p.refuse(at, "is not a supported top-level key (supported: services, volumes, networks, version, name, x-heimdall)")
		}
	})
	if services == nil {
		p.refuse("services", "missing")
		return
	}
	p.mapping(services, "services", func(name string, v *yaml.Node, at string) {
		if s := p.parseService(name, v, at); s != nil {
			p.Services = append(p.Services, s)
		}
	})
}

func (p *Project) parseProjectExtension(v *yaml.Node) {
	p.mapping(v, "x-heimdall", func(key string, n *yaml.Node, at string) {
		switch key {
		case "verifier_services":
			p.Heimdall.VerifierServices = p.boolean(n, at)
		case "build_network":
			p.Heimdall.BuildNetwork = p.boolean(n, at)
		case "profiles":
			p.Heimdall.Profiles = p.strList(n, at)
		default:
			p.refuse(at, "is not a known x-heimdall key (verifier_services, build_network, profiles)")
		}
	})
}

// parseVolumes reads the top-level volumes: every one becomes a fresh
// volume per run.
func (p *Project) parseVolumes(v *yaml.Node) {
	p.mapping(v, "volumes", func(name string, n *yaml.Node, at string) {
		p.Volumes[name] = true
		p.mapping(n, at, func(key string, val *yaml.Node, kat string) {
			switch key {
			case "external":
				if p.boolean(val, kat) {
					p.refuse(kat, "an external volume lives outside the run; declare it without external so each run gets a fresh one")
				}
			case "driver":
				if d := p.str(val, kat); d != "" && d != "local" {
					p.refuse(kat, "volume driver %q is not supported; only local volumes are created", d)
				}
			case "driver_opts":
				p.refuse(kat, "is not supported: local driver options can bind a host folder")
			case "name", "labels":
				p.add(LevelIgnored, kat, "the bench names and labels the volume per run")
			default:
				p.refuse(kat, "is not a supported volume key (external, driver, name, labels)")
			}
		})
	})
}

// parseNetworks reads the top-level networks. Every network of the file
// collapses into the run's one internal network.
func (p *Project) parseNetworks(v *yaml.Node) {
	n := 0
	p.mapping(v, "networks", func(name string, nv *yaml.Node, at string) {
		n++
		p.mapping(nv, at, func(key string, val *yaml.Node, kat string) {
			switch key {
			case "external":
				if p.boolean(val, kat) {
					p.refuse(kat, "an external network lives outside the run")
				}
			case "driver":
				if d := p.str(val, kat); d != "" && d != "bridge" {
					p.refuse(kat, "network driver %q is not supported", d)
				}
			case "internal", "name", "labels", "attachable", "ipam", "driver_opts", "enable_ipv6", "enable_ipv4":
				p.add(LevelIgnored, kat, "the run's network is created by the bench: internal, no route out")
			default:
				p.refuse(kat, "is not a supported network key")
			}
		})
	})
	if n > 0 {
		p.add(LevelChanged, "networks", "every network of the file becomes the run's one internal network, so services on different compose networks can reach each other")
	}
}

// expandAliases returns a copy of n with every alias replaced by what it
// names and every merge key (<<: *anchor, or a list of them) folded into its
// mapping, explicit keys winning, as docker compose reads them. budget
// bounds the nodes made.
func expandAliases(n *yaml.Node, budget *int) (*yaml.Node, error) {
	if *budget--; *budget < 0 {
		return nil, errors.New("the file expands to too many values through YAML aliases")
	}
	switch n.Kind {
	case yaml.AliasNode:
		if n.Alias == nil {
			return nil, errors.New("an alias names no anchor")
		}
		return expandAliases(n.Alias, budget)
	case yaml.SequenceNode:
		cp := *n
		cp.Content = make([]*yaml.Node, 0, len(n.Content))
		for _, c := range n.Content {
			e, err := expandAliases(c, budget)
			if err != nil {
				return nil, err
			}
			cp.Content = append(cp.Content, e)
		}
		return &cp, nil
	case yaml.MappingNode:
		cp := *n
		cp.Content = nil
		var merged []*yaml.Node
		explicit := map[string]bool{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			if !mergeKey(n.Content[i]) {
				explicit[n.Content[i].Value] = true
			}
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if mergeKey(k) {
				src, err := expandAliases(v, budget)
				if err != nil {
					return nil, err
				}
				maps := []*yaml.Node{src}
				if src.Kind == yaml.SequenceNode {
					maps = src.Content
				}
				for _, m := range maps {
					if m.Kind != yaml.MappingNode {
						return nil, errors.New("a merge key (<<) must name a mapping")
					}
					for j := 0; j+1 < len(m.Content); j += 2 {
						if !explicit[m.Content[j].Value] {
							explicit[m.Content[j].Value] = true
							merged = append(merged, m.Content[j], m.Content[j+1])
						}
					}
				}
				continue
			}
			e, err := expandAliases(v, budget)
			if err != nil {
				return nil, err
			}
			cp.Content = append(cp.Content, k, e)
		}
		cp.Content = append(cp.Content, merged...)
		return &cp, nil
	}
	return n, nil
}

// mergeKey reports an unquoted << key: a quoted "<<" is an ordinary key.
func mergeKey(k *yaml.Node) bool { return k.Value == "<<" && k.Style == 0 }
