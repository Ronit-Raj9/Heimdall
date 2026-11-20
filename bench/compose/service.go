package compose

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"gopkg.in/yaml.v3"
)

// refusedServiceKeys are compose keys that would weaken the run's isolation
// or reach the host, with the reason given to the task author.
var refusedServiceKeys = map[string]string{
	"privileged":          "privileged mode gives the container the host's devices and every capability",
	"cap_add":             "added capabilities are not allowed; services keep docker's default set",
	"devices":             "host devices are not passed into a run",
	"device_cgroup_rules": "host devices are not passed into a run",
	"gpus":                "GPUs are not scheduled for services",
	"pid":                 "sharing a process namespace (pid) is not allowed",
	"ipc":                 "sharing an IPC namespace is not allowed",
	"uts":                 "sharing the host's UTS namespace is not allowed",
	"userns_mode":         "changing the user namespace is not allowed",
	"cgroup":              "changing the cgroup namespace is not allowed",
	"cgroup_parent":       "changing the cgroup parent is not allowed",
	"security_opt":        "security options can turn off seccomp or AppArmor; services always run with no-new-privileges",
	"sysctls":             "kernel parameters are not changed for a run",
	"volumes_from":        "mounting another container's volumes is not supported; use a named volume",
	"extra_hosts":         "extra host entries can point at the host; services reach each other by name",
	"dns":                 "custom DNS servers are not used; the run's network resolves service names only",
	"dns_search":          "custom DNS settings are not used",
	"dns_opt":             "custom DNS settings are not used",
	"mac_address":         "fixed addresses are not supported",
	"runtime":             "only the default container runtime is used",
	"isolation":           "only the default isolation is used",
	"oom_kill_disable":    "the OOM killer stays on",
	"oom_score_adj":       "OOM scores are not changed",
	"extends":             "extends is not supported; write the service out in full",
	"external_links":      "links to containers outside the run are not allowed",
	"links":               "links are not supported; services reach each other by service name",
	"secrets":             "secrets are not supported; mount a file inside the task folder read-only",
	"configs":             "configs are not supported; mount a file inside the task folder read-only",
	"ulimits":             "ulimits are not changed for a run",
	"storage_opt":         "storage options are not supported",
	"blkio_config":        "block IO settings are not supported",
	"post_start":          "lifecycle hooks are not supported",
	"pre_stop":            "lifecycle hooks are not supported",
	"credential_spec":     "credential specs are not supported",
	"group_add":           "extra groups are not supported",
	"use_api_socket":      "the docker API socket is never given to a container",
	"models":              "model runners are not supported; agents reach models through the gateway",
	"provider":            "provider services are not supported",
	"volume_driver":       "only local volumes are created",
}

// ignoredServiceKeys have no effect under the bench.
var ignoredServiceKeys = map[string]string{
	"restart":           "services are not restarted; a service that stops ends the run as an infrastructure error",
	"container_name":    "the bench names containers per run",
	"labels":            "the bench sets its own labels",
	"label_file":        "the bench sets its own labels",
	"annotations":       "annotations are not set",
	"stdin_open":        "no terminal is attached to a service",
	"tty":               "no terminal is attached to a service",
	"init":              "no init process is added",
	"logging":           "the bench captures each service's log itself (services/<id>.log in the run folder)",
	"platform":          "images run on the machine's own platform",
	"pull_policy":       "a missing image is pulled, a present one used; nothing else",
	"stop_signal":       "services are removed, not stopped",
	"stop_grace_period": "services are removed, not stopped",
	"domainname":        "the bench sets host names",
	"attach":            "service output is captured into the run folder",
	"develop":           "watch mode is not used",
	"memswap_limit":     "swap follows the memory cap",
	"mem_reservation":   "reservations are not used; only caps",
	"mem_swappiness":    "swap settings are not used",
	"cpu_shares":        "only a CPU cap (cpus) is used",
	"cpu_quota":         "only a CPU cap (cpus) is used",
	"cpu_period":        "only a CPU cap (cpus) is used",
	"cpu_rt_runtime":    "only a CPU cap (cpus) is used",
	"cpu_rt_period":     "only a CPU cap (cpus) is used",
	"cpuset":            "only a CPU cap (cpus) is used",
	"cpu_count":         "only a CPU cap (cpus) is used",
	"cpu_percent":       "only a CPU cap (cpus) is used",
	"shm_size":          "the default shared memory size is used",
	"cap_drop":          "services keep docker's default capabilities; dropping more is not applied",
	"read_only":         "the root file system is left as the image has it",
}

// reservedIDs are host names the bench itself uses on a run's network.
var reservedIDs = map[string]bool{"toolproxy": true, "mailbox": true, "tools-internal": true}

// parseService reads one service. Keys it cannot honour are refused.
func (p *Project) parseService(name string, v *yaml.Node, at string) *Service {
	s := &Service{Name: name, Env: map[string]string{}}
	if v.Kind != yaml.MappingNode {
		p.refuse(at, "want a mapping")
		return nil
	}
	p.mapping(v, at, func(key string, n *yaml.Node, kat string) {
		if why, ok := refusedServiceKeys[key]; ok {
			p.refuse(kat, "%s", why)
			return
		}
		if why, ok := ignoredServiceKeys[key]; ok {
			p.add(LevelIgnored, kat, "%s", why)
			return
		}
		switch key {
		case "image":
			s.Image = p.str(n, kat)
		case "build":
			s.Build = p.parseBuild(n, kat)
		case "command":
			s.Command, s.HasCommand = p.command(n, kat), true
		case "entrypoint":
			s.Entrypoint, s.HasEntrypoint = p.command(n, kat), true
		case "environment":
			p.parseEnvironment(s, n, kat)
		case "env_file":
			p.parseEnvFile(s, n, kat)
		case "depends_on":
			s.DependsOn = p.parseDependsOn(n, kat)
		case "healthcheck":
			s.Health = p.parseHealth(n, kat)
		case "volumes":
			s.Mounts = p.parseMounts(n, kat)
		case "expose":
			for i, e := range p.strList(n, kat) {
				ports, err := portRange(e)
				if err != nil {
					p.refuse(fmt.Sprintf("%s[%d]", kat, i), "%v", err)
				}
				s.Ports = appendPorts(s.Ports, ports)
			}
		case "ports":
			p.parsePorts(s, n, kat)
		case "working_dir":
			s.WorkingDir = p.str(n, kat)
			if s.WorkingDir != "" && !path.IsAbs(s.WorkingDir) {
				p.refuse(kat, "%q must be an absolute path", s.WorkingDir)
			}
		case "user":
			s.User = p.str(n, kat)
		case "hostname":
			s.Hostname = p.str(n, kat)
			p.add(LevelChanged, kat, "the service also answers to %q on the run's network; its own host name is its service id", s.Hostname)
		case "tmpfs":
			for i, t := range p.strList(n, kat) {
				if !path.IsAbs(strings.SplitN(t, ":", 2)[0]) {
					p.refuse(fmt.Sprintf("%s[%d]", kat, i), "%q must start with an absolute path", t)
					continue
				}
				s.Tmpfs = append(s.Tmpfs, t)
			}
		case "networks":
			p.parseServiceNetworks(s, n, kat)
		case "network_mode":
			switch m := p.str(n, kat); m {
			case "", "bridge", "default":
				p.add(LevelChanged, kat, "the service joins the run's internal network")
			default:
				p.refuse(kat, "network_mode %q is not allowed; services join only the run's internal network", m)
			}
		case "profiles":
			s.Profiles = p.strList(n, kat)
		case "mem_limit":
			s.MemoryBytes = p.bytes(n, kat)
		case "cpus":
			s.CPUs = p.cpus(n, kat)
		case "pids_limit":
			s.Pids = p.integer(n, kat)
		case "deploy":
			p.parseDeploy(s, n, kat)
		case "scale":
			if p.integer(n, kat) > 1 {
				p.refuse(kat, "more than one replica of a service is not supported")
			}
		case "x-heimdall":
			p.parseServiceExtension(s, n, kat)
		default:
			if strings.HasPrefix(key, "x-") {
				return
			}
			p.refuse(kat, "is not a supported service key")
		}
	})
	return s
}

func appendPorts(have, add []int) []int {
	for _, a := range add {
		dup := false
		for _, h := range have {
			dup = dup || h == a
		}
		if !dup {
			have = append(have, a)
		}
	}
	return have
}

func (p *Project) bytes(n *yaml.Node, at string) int64 {
	s := p.str(n, at)
	if s == "" {
		return 0
	}
	b, err := parseBytes(s)
	if err != nil {
		p.refuse(at, "%v", err)
	}
	return b
}

func (p *Project) cpus(n *yaml.Node, at string) float64 {
	s := p.str(n, at)
	if s == "" {
		return 0
	}
	var f float64
	if _, err := fmt.Sscanf(s, "%g", &f); err != nil || f < 0 {
		p.refuse(at, "%q is not a number of CPUs", s)
	}
	return f
}

// rel turns a path written in the compose file into a path relative to the
// task folder, refusing one that leaves it. It does not check existence.
func (p *Project) rel(at, src string) (string, bool) {
	if strings.HasPrefix(src, "~") || path.IsAbs(src) || filepath.IsAbs(src) {
		p.refuse(at, "%q is outside the task folder; only paths inside it are used", src)
		return "", false
	}
	r := path.Clean(path.Join(p.Dir, src))
	if !filepath.IsLocal(filepath.FromSlash(r)) {
		p.refuse(at, "%q leaves the task folder", src)
		return "", false
	}
	return r, true
}

// exists checks rel inside the task folder, through symlinks too.
func (p *Project) exists(at, rel string, dir bool) bool {
	abs, err := utils.ResolveInside(p.TaskDir, rel)
	if err != nil {
		p.refuse(at, "%v", err)
		return false
	}
	st, err := statPath(abs)
	switch {
	case err != nil:
		p.refuse(at, "%s not found", rel)
		return false
	case dir && !st.IsDir():
		p.refuse(at, "%s is not a folder", rel)
		return false
	case !dir && !st.Mode().IsRegular():
		p.refuse(at, "%s is not a regular file", rel)
		return false
	}
	return true
}

func (p *Project) parseBuild(n *yaml.Node, at string) *Build {
	b := &Build{}
	ctx, dockerfile := ".", "Dockerfile"
	if n.Kind == yaml.ScalarNode {
		ctx = p.str(n, at)
	} else {
		p.mapping(n, at, func(key string, v *yaml.Node, kat string) {
			switch key {
			case "context":
				ctx = p.str(v, kat)
			case "dockerfile":
				dockerfile = p.str(v, kat)
			case "network":
				if m := p.str(v, kat); m != "" && m != "default" && m != "none" {
					p.refuse(kat, "build network %q is not allowed; a build gets the network only with x-heimdall build_network", m)
				}
			case "args":
				p.refuse(kat, "build arguments are not supported yet; write their values into the Dockerfile")
			case "target":
				p.refuse(kat, "build targets are not supported yet; give the stage its own Dockerfile")
			case "dockerfile_inline":
				p.refuse(kat, "inline Dockerfiles are not supported; write it to a file in the task folder")
			case "ssh", "secrets":
				p.refuse(kat, "build-time secrets and SSH agents are not passed to builds")
			case "extra_hosts", "privileged", "entitlements":
				p.refuse(kat, "is not allowed in a build")
			case "cache_from", "cache_to", "labels", "no_cache", "pull", "tags", "platforms", "shm_size", "isolation", "additional_contexts", "ulimits":
				p.add(LevelIgnored, kat, "the bench builds with its own settings")
			default:
				p.refuse(kat, "is not a supported build key (context, dockerfile)")
			}
		})
	}
	if strings.Contains(ctx, "://") || strings.HasPrefix(ctx, "git@") {
		p.refuse(at+".context", "a remote build context is not supported; the context must be a folder inside the task folder")
		return nil
	}
	c, ok := p.rel(at+".context", ctx)
	if !ok || !p.exists(at+".context", c, true) {
		return nil
	}
	b.Context = c
	df, ok := p.relTo(at+".dockerfile", c, dockerfile)
	if !ok || !p.exists(at+".dockerfile", df, false) {
		return nil
	}
	b.Dockerfile = df
	return b
}

// relTo resolves a path relative to another task-relative folder (a
// Dockerfile inside its context).
func (p *Project) relTo(at, base, src string) (string, bool) {
	if path.IsAbs(src) {
		p.refuse(at, "%q must be relative to the build context", src)
		return "", false
	}
	r := path.Clean(path.Join(base, src))
	if !filepath.IsLocal(filepath.FromSlash(r)) {
		p.refuse(at, "%q leaves the task folder", src)
		return "", false
	}
	return r, true
}

// parseEnvironment reads environment as a list of KEY=VALUE or a mapping.
func (p *Project) parseEnvironment(s *Service, n *yaml.Node, at string) {
	set := func(k string, v *yaml.Node, kat string, hasValue bool) {
		if !envName(k) {
			p.refuse(kat, "%q is not a variable name", k)
			return
		}
		if p.opts.Conventions == ConventionsHarbor && harnessEnv[k] {
			p.add(LevelIgnored, kat, "%s configures the agent harness's own model provider; the bench gives the agent its model through the gateway", k)
			return
		}
		if !hasValue {
			p.refuse(kat, "%s has no value, so docker compose would take it from the host environment, which a run never passes in", k)
			return
		}
		s.Env[k] = p.str(v, kat)
	}
	switch n.Kind {
	case yaml.SequenceNode:
		for i, c := range n.Content {
			kat := fmt.Sprintf("%s[%d]", at, i)
			if c.Kind != yaml.ScalarNode {
				p.refuse(kat, "want KEY=VALUE")
				continue
			}
			k, val, ok := strings.Cut(c.Value, "=")
			set(k, &yaml.Node{Kind: yaml.ScalarNode, Value: val, Tag: "!!str"}, kat, ok)
		}
	case yaml.MappingNode:
		p.mapping(n, at, func(k string, v *yaml.Node, kat string) {
			set(k, v, kat, !(v.Kind == yaml.ScalarNode && v.Tag == "!!null"))
		})
	default:
		p.refuse(at, "want a list of KEY=VALUE or a mapping")
	}
}

// parseEnvFile reads env_file entries (a path, a list of paths, or entries
// with path and required), each inside the task folder. Values in the file
// are taken as written; files apply in listed order with later files
// overriding earlier ones, and the environment key wins over them all.
func (p *Project) parseEnvFile(s *Service, n *yaml.Node, at string) {
	type entry struct {
		path     string
		required bool
		raw      bool
		at       string
	}
	var entries []entry
	if n.Kind == yaml.ScalarNode {
		entries = append(entries, entry{path: p.str(n, at), required: true, at: at})
	} else if n.Kind == yaml.SequenceNode {
		for i, c := range n.Content {
			kat := fmt.Sprintf("%s[%d]", at, i)
			if c.Kind == yaml.ScalarNode {
				entries = append(entries, entry{path: p.str(c, kat), required: true, at: kat})
				continue
			}
			e := entry{required: true, at: kat}
			p.mapping(c, kat, func(key string, v *yaml.Node, kkat string) {
				switch key {
				case "path":
					e.path = p.str(v, kkat)
				case "required":
					e.required = p.boolean(v, kkat)
				case "format":
					switch f := p.str(v, kkat); f {
					case "":
					case "raw":
						e.raw = true
					default:
						p.refuse(kkat, "env_file format %q is not supported", f)
					}
				default:
					p.refuse(kkat, "is not a supported env_file key (path, required, format)")
				}
			})
			entries = append(entries, e)
		}
	} else {
		p.refuse(at, "want a path or a list of paths")
	}
	explicit := map[string]bool{}
	for k := range s.Env {
		explicit[k] = true
	}
	for _, e := range entries {
		r, ok := p.rel(e.at, e.path)
		if !ok {
			continue
		}
		b, err := utils.ReadFileInside(p.TaskDir, r)
		if err != nil {
			if !e.required {
				p.add(LevelIgnored, e.at, "optional env_file %s is missing", r)
				continue
			}
			p.refuse(e.at, "env_file %s: %v", r, err)
			continue
		}
		vals, order, err := parseDotenvMode(string(b), e.raw)
		if err != nil {
			p.refuse(e.at, "env_file %s: %v", r, err)
			continue
		}
		for _, k := range order {
			if !explicit[k] {
				s.Env[k] = vals[k]
			}
		}
	}
}

func (p *Project) parseDependsOn(n *yaml.Node, at string) []Dependency {
	var out []Dependency
	switch n.Kind {
	case yaml.SequenceNode:
		for i, c := range n.Content {
			out = append(out, Dependency{Service: p.str(c, fmt.Sprintf("%s[%d]", at, i)), Condition: CondStarted})
		}
	case yaml.MappingNode:
		p.mapping(n, at, func(name string, v *yaml.Node, kat string) {
			d := Dependency{Service: name, Condition: CondStarted}
			p.mapping(v, kat, func(key string, val *yaml.Node, kkat string) {
				switch key {
				case "condition":
					d.Condition = p.str(val, kkat)
					switch d.Condition {
					case CondStarted, CondHealthy, CondCompleted:
					default:
						p.refuse(kkat, "condition %q is not one of %s, %s, %s", d.Condition, CondStarted, CondHealthy, CondCompleted)
					}
				case "required":
					if !p.boolean(val, kkat) {
						p.add(LevelChanged, kkat, "an optional dependency is still started and waited for")
					}
				case "restart":
					p.add(LevelIgnored, kkat, "services are not restarted")
				default:
					p.refuse(kkat, "is not a supported depends_on key (condition, required, restart)")
				}
			})
			out = append(out, d)
		})
	default:
		p.refuse(at, "want a list or a mapping of services")
	}
	return out
}

func (p *Project) parseHealth(n *yaml.Node, at string) *Health {
	h := &Health{Interval: 30, Timeout: 30, Retries: 3}
	p.mapping(n, at, func(key string, v *yaml.Node, kat string) {
		switch key {
		case "test":
			h.Argv = p.healthTest(v, kat, h)
		case "interval":
			h.Interval = p.seconds(v, kat)
		case "timeout":
			h.Timeout = p.seconds(v, kat)
		case "retries":
			h.Retries = p.integer(v, kat)
		case "start_period":
			h.StartPeriod = p.seconds(v, kat)
		case "start_interval":
			p.add(LevelIgnored, kat, "the bench polls the check every 0.5 s")
		case "disable":
			h.Disabled = p.boolean(v, kat)
		default:
			p.refuse(kat, "is not a supported healthcheck key")
		}
	})
	if !h.Disabled && len(h.Argv) == 0 {
		p.refuse(at+".test", "a healthcheck needs a test")
	}
	return h
}

// healthTest translates a healthcheck test: [CMD, args...] runs as is,
// [CMD-SHELL, cmd] and a plain string run in /bin/sh -c, [NONE] disables.
func (p *Project) healthTest(v *yaml.Node, at string, h *Health) []string {
	if v.Kind == yaml.ScalarNode {
		return []string{"/bin/sh", "-c", p.str(v, at)}
	}
	list := p.strList(v, at)
	if len(list) == 0 {
		return nil
	}
	switch list[0] {
	case "NONE":
		h.Disabled = true
		return nil
	case "CMD":
		if len(list) < 2 {
			p.refuse(at, "CMD needs a command")
		}
		return list[1:]
	case "CMD-SHELL":
		if len(list) != 2 {
			p.refuse(at, "CMD-SHELL takes one command string")
			return nil
		}
		return []string{"/bin/sh", "-c", list[1]}
	}
	p.refuse(at, "a test list must start with CMD, CMD-SHELL or NONE")
	return nil
}

// parseMounts reads volumes entries in the short (src:dst[:mode]) or long
// syntax.
func (p *Project) parseMounts(n *yaml.Node, at string) []Mount {
	if n.Kind != yaml.SequenceNode {
		p.refuse(at, "want a list")
		return nil
	}
	var out []Mount
	for i, c := range n.Content {
		kat := fmt.Sprintf("%s[%d]", at, i)
		var m Mount
		var used []string
		ok := true
		if c.Kind == yaml.ScalarNode {
			var val string
			val, used, ok = p.scalarHarbor(c, kat)
			m = shortMount(val)
		} else {
			m, used, ok = p.longMount(c, kat)
		}
		if !ok {
			continue
		}
		if why := p.harnessMount(m, used); why != "" {
			m.Harness = why
			out = append(out, m)
			continue
		}
		if mm, keep := p.checkMount(m, kat); keep {
			out = append(out, mm)
		}
	}
	return out
}

// scalarHarbor is scalar, except that under Harbor conventions a value that
// reads a Harbor host folder variable is returned as it is, so the mount can
// be recognised as Harbor's own log plumbing.
func (p *Project) scalarHarbor(n *yaml.Node, at string) (string, []string, bool) {
	if p.opts.Conventions == ConventionsHarbor && n.Kind == yaml.ScalarNode {
		res := interpolate(n.Value, p.lookup)
		for _, u := range res.used {
			if harborHostVars[u] || u == "HOME" {
				return res.value, res.used, true
			}
		}
	}
	return p.scalar(n, at)
}

// harnessMount reports why a mount is harness plumbing the bench replaces:
// Harbor's log folders (the bench gives the verifier its own /logs/verifier)
// and a model provider's credentials folder.
func (p *Project) harnessMount(m Mount, used []string) string {
	if p.opts.Conventions != ConventionsHarbor {
		return ""
	}
	for _, u := range used {
		if harborHostVars[u] {
			return "Harbor's log folder mount; the bench gives the verifier its own /logs/verifier and keeps the agent's logs itself"
		}
	}
	if strings.HasSuffix(m.Target, "/.config/gcloud") {
		return "the agent harness's Google Cloud credentials; the bench gives the agent its model through the gateway"
	}
	return ""
}

// shortMount parses src:dst[:mode] or a bare container path.
func shortMount(v string) Mount {
	parts := strings.Split(v, ":")
	switch len(parts) {
	case 1:
		return Mount{Type: "volume", Target: parts[0]}
	}
	m := Mount{Source: parts[0], Target: parts[1]}
	if len(parts) > 2 {
		for _, o := range strings.Split(parts[2], ",") {
			if o == "ro" {
				m.ReadOnly = true
			}
		}
	}
	m.Type = "volume"
	if strings.HasPrefix(m.Source, ".") || strings.HasPrefix(m.Source, "/") || strings.HasPrefix(m.Source, "~") {
		m.Type = "bind"
	}
	return m
}

func (p *Project) longMount(c *yaml.Node, at string) (Mount, []string, bool) {
	var m Mount
	var used []string
	ok := true
	p.mapping(c, at, func(key string, v *yaml.Node, kat string) {
		switch key {
		case "type":
			m.Type = p.str(v, kat)
		case "source":
			var u []string
			m.Source, u, ok = p.scalarHarbor(v, kat)
			used = append(used, u...)
		case "target":
			m.Target = p.str(v, kat)
		case "read_only":
			m.ReadOnly = p.boolean(v, kat)
		case "consistency":
		case "bind":
			p.mapping(v, kat, func(bk string, bv *yaml.Node, bat string) {
				switch bk {
				case "propagation":
					p.refuse(bat, "mount propagation is not supported")
				case "create_host_path", "selinux":
				default:
					p.refuse(bat, "is not a supported bind option")
				}
			})
		case "volume":
			p.mapping(v, kat, func(vk string, vv *yaml.Node, vat string) {
				switch vk {
				case "nocopy":
				case "subpath":
					p.refuse(vat, "volume subpaths are not supported")
				default:
					p.refuse(vat, "is not a supported volume option")
				}
			})
		case "tmpfs":
			p.mapping(v, kat, func(tk string, tv *yaml.Node, tat string) {
				switch tk {
				case "size":
					m.Source = "size=" + p.str(tv, tat)
				case "mode":
					p.add(LevelIgnored, tat, "the tmpfs keeps its default mode")
				default:
					p.refuse(tat, "is not a supported tmpfs option")
				}
			})
		default:
			p.refuse(kat, "is not a supported volume mount key")
		}
	})
	switch m.Type {
	case "bind", "volume", "tmpfs":
	default:
		p.refuse(at+".type", "mount type %q is not supported (bind, volume, tmpfs)", m.Type)
		ok = false
	}
	return m, used, ok
}

// checkMount refuses what a mount may not do and resolves a bind's source
// inside the task folder.
func (p *Project) checkMount(m Mount, at string) (Mount, bool) {
	if !path.IsAbs(m.Target) || path.Clean(m.Target) == "/" {
		p.refuse(at, "target %q must be an absolute container path other than /", m.Target)
		return m, false
	}
	m.Target = path.Clean(m.Target)
	if strings.Contains(m.Source, "docker.sock") || strings.Contains(m.Target, "docker.sock") {
		p.refuse(at, "the docker socket is never mounted into a container: it controls the host")
		return m, false
	}
	switch m.Type {
	case "tmpfs":
		return m, true
	case "volume":
		// Whether a named volume is declared under the top-level volumes is
		// checked by Map, because that key may follow the services.
		return m, true
	}
	r, ok := p.rel(at, m.Source)
	if !ok {
		return m, false
	}
	abs, err := utils.ResolveInside(p.TaskDir, r)
	if err != nil {
		p.refuse(at, "%v", err)
		return m, false
	}
	if _, err := statPath(abs); err != nil {
		p.refuse(at, "bind source %s not found inside the task folder", r)
		return m, false
	}
	m.Source = r
	return m, true
}

func (p *Project) parsePorts(s *Service, n *yaml.Node, at string) {
	if n.Kind != yaml.SequenceNode {
		p.refuse(at, "want a list")
		return
	}
	for i, c := range n.Content {
		kat := fmt.Sprintf("%s[%d]", at, i)
		var m portMapping
		var err error
		if c.Kind == yaml.ScalarNode {
			m, err = parseShortPort(p.str(c, kat))
		} else {
			target, published, proto := "", "", ""
			p.mapping(c, kat, func(key string, v *yaml.Node, pat string) {
				switch key {
				case "target":
					target = p.str(v, pat)
				case "published":
					published = p.str(v, pat)
				case "protocol":
					proto = p.str(v, pat)
				case "host_ip", "mode", "name", "app_protocol":
				default:
					p.refuse(pat, "is not a supported port key")
				}
			})
			if proto != "" {
				target += "/" + proto
			}
			m.published = published
			m.container, err = portRange(target)
		}
		if err != nil {
			p.refuse(kat, "%v", err)
			continue
		}
		s.Ports = appendPorts(s.Ports, m.container)
		if m.published != "" {
			p.add(LevelChanged, kat, "host port %s is not published; the service is reachable only inside the run, as %s:%d", m.published, s.Name, m.container[0])
		}
	}
}

func (p *Project) parseServiceNetworks(s *Service, n *yaml.Node, at string) {
	if n.Kind == yaml.SequenceNode {
		p.strList(n, at)
		return
	}
	p.mapping(n, at, func(_ string, v *yaml.Node, kat string) {
		p.mapping(v, kat, func(key string, val *yaml.Node, kkat string) {
			switch key {
			case "aliases":
				s.Aliases = append(s.Aliases, p.strList(val, kkat)...)
			case "ipv4_address", "ipv6_address", "link_local_ips", "priority", "mac_address", "driver_opts", "gw_priority", "interface_name":
				p.add(LevelIgnored, kkat, "addresses on the run's network are assigned by docker")
			default:
				p.refuse(kkat, "is not a supported service network key (aliases)")
			}
		})
	})
}

func (p *Project) parseDeploy(s *Service, n *yaml.Node, at string) {
	p.mapping(n, at, func(key string, v *yaml.Node, kat string) {
		switch key {
		case "resources":
			p.mapping(v, kat, func(rk string, rv *yaml.Node, rat string) {
				switch rk {
				case "limits":
					p.mapping(rv, rat, func(lk string, lv *yaml.Node, lat string) {
						switch lk {
						case "memory":
							s.MemoryBytes = p.bytes(lv, lat)
						case "cpus":
							s.CPUs = p.cpus(lv, lat)
						case "pids":
							s.Pids = p.integer(lv, lat)
						default:
							p.refuse(lat, "is not a supported resource limit (memory, cpus, pids)")
						}
					})
				case "reservations":
					p.add(LevelIgnored, rat, "reservations are not used; only caps")
				default:
					p.refuse(rat, "is not a supported resources key")
				}
			})
		case "replicas":
			if p.integer(v, kat) > 1 {
				p.refuse(kat, "more than one replica of a service is not supported")
			}
		case "mode", "restart_policy", "update_config", "rollback_config", "placement", "labels", "endpoint_mode":
			p.add(LevelIgnored, kat, "swarm deployment settings have no effect")
		default:
			p.refuse(kat, "is not a supported deploy key")
		}
	})
}

func (p *Project) parseServiceExtension(s *Service, n *yaml.Node, at string) {
	p.mapping(n, at, func(key string, v *yaml.Node, kat string) {
		switch key {
		case "workspace":
			s.X.Workspace = p.boolean(v, kat)
		case "verifier":
			s.X.Verifier = p.boolean(v, kat)
		case "role":
			s.X.Role = p.str(v, kat)
			switch s.X.Role {
			case "", schemas.ServiceRoleData, schemas.ServiceRoleTool, schemas.ServiceRoleMCP:
			default:
				p.refuse(kat, "role %q is not data, tool or mcp", s.X.Role)
			}
		case "hidden":
			s.X.Hidden = p.boolean(v, kat)
		case "proxy":
			b := p.boolean(v, kat)
			s.X.Proxy = &b
		case "run_once":
			s.X.RunOnce = p.boolean(v, kat)
		case "ready_timeout_s":
			s.X.ReadyTimeoutS = p.integer(v, kat)
		case "export":
			if v.Kind != yaml.SequenceNode {
				p.refuse(kat, "want a list of {id, argv | path}")
				return
			}
			for i, c := range v.Content {
				eat := fmt.Sprintf("%s[%d]", kat, i)
				var x schemas.StateExport
				p.mapping(c, eat, func(ek string, ev *yaml.Node, eeat string) {
					switch ek {
					case "id":
						x.ID = p.str(ev, eeat)
					case "argv":
						x.Argv = p.strList(ev, eeat)
					case "path":
						x.Path = p.str(ev, eeat)
					case "max_bytes":
						x.MaxBytes = int64(p.integer(ev, eeat))
					default:
						p.refuse(eeat, "is not an export key (id, argv, path, max_bytes)")
					}
				})
				s.X.Export = append(s.X.Export, x)
			}
		default:
			p.refuse(kat, "is not a known x-heimdall service key (workspace, verifier, role, hidden, proxy, run_once, ready_timeout_s, export)")
		}
	})
}

// statPath stats a resolved path.
func statPath(abs string) (os.FileInfo, error) { return os.Stat(abs) }
