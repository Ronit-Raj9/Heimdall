package environments

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
)

// gVisor (runsc) has no Docker embedded DNS on a user-defined network
// (gvisor.dev FAQ, "DNS doesn't work on Docker user-defined networks"): a
// sandboxed container's 127.0.0.11 answers nothing, so an agent on its org
// network could not resolve the gateway, the tool proxy or a service by name
// and every model call failed until the run timed out. Under runsc the
// containers that dial others by name (the agent, the tool proxy) get every
// name on their networks as --add-host entries instead, and readiness probes
// dial services by address. Under runc nothing changes.

// runtimeGVisor is the gVisor runtime name (quota.RuntimeRunsc).
const runtimeGVisor = "runsc"

// gvisor reports whether this workspace's containers run under gVisor: a
// shared runner's org whose Docker policy passes --runtime runsc.
func (e *dockerWorkspace) gvisor() bool {
	return e.cfg.Docker.Policy != nil && e.cfg.Docker.Policy.Runtime == runtimeGVisor
}

// networkHosts returns "name:address" for every DNS name (container name,
// network aliases) of every container on the networks, in network order; a
// name already listed keeps its first address. "", "none" and "container:"
// networks are skipped, and a network that cannot be read adds nothing.
func (e *dockerWorkspace) networkHosts(ctx context.Context, networks ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range networks {
		if n == "" || n == "none" || strings.HasPrefix(n, "container:") {
			continue
		}
		res, err := e.cfg.Docker.Run(ctx, "network", "inspect", "--format", "{{range $id, $c := .Containers}}{{$id}} {{end}}", n)
		if err != nil || res.ExitCode != 0 {
			continue
		}
		ids := strings.Fields(string(res.Stdout))
		if len(ids) == 0 {
			continue
		}
		res, err = e.cfg.Docker.Run(ctx, append([]string{"inspect", "--format", `{{json (index .NetworkSettings.Networks "` + n + `")}}`}, ids...)...)
		if err != nil || res.ExitCode != 0 {
			continue
		}
		for _, line := range strings.Split(strings.TrimSpace(string(res.Stdout)), "\n") {
			var ep struct {
				IPAddress string   `json:"IPAddress"`
				Aliases   []string `json:"Aliases"`
				DNSNames  []string `json:"DNSNames"`
			}
			if json.Unmarshal([]byte(line), &ep) != nil || ep.IPAddress == "" {
				continue
			}
			for _, name := range append(ep.DNSNames, ep.Aliases...) {
				if name == "" || strings.ContainsAny(name, ": \t") || seen[name] {
					continue
				}
				seen[name] = true
				out = append(out, name+":"+ep.IPAddress)
			}
		}
	}
	return out
}

// withHosts puts --add-host flags for hosts right after the subcommand of a
// `docker create` or `docker run` command line. It is pure.
func withHosts(args, hosts []string) []string {
	if len(hosts) == 0 || len(args) == 0 {
		return args
	}
	out := make([]string, 0, len(args)+2*len(hosts))
	out = append(out, args[0])
	for _, h := range hosts {
		out = append(out, "--add-host", h)
	}
	return append(out, args[1:]...)
}

// sandboxHosts adds the names on networks to a create command line when the
// workspace runs under gVisor and the container has a network of its own
// (a container: network shares its holder's /etc/hosts and takes none).
func (e *dockerWorkspace) sandboxHosts(ctx context.Context, args []string, network string, more ...string) []string {
	if !e.gvisor() || strings.HasPrefix(network, "container:") {
		return args
	}
	return withHosts(args, e.networkHosts(ctx, append([]string{network}, more...)...))
}

// containerAddrs maps each network of a container to its address there.
func (e *dockerWorkspace) containerAddrs(ctx context.Context, name string) map[string]string {
	res, err := e.cfg.Docker.Run(ctx, "inspect", "--format", "{{json .NetworkSettings.Networks}}", name)
	if err != nil || res.ExitCode != 0 {
		return nil
	}
	var nets map[string]struct {
		IPAddress string `json:"IPAddress"`
	}
	if json.Unmarshal(res.Stdout, &nets) != nil {
		return nil
	}
	out := map[string]string{}
	for n, ep := range nets {
		if ep.IPAddress != "" {
			out[n] = ep.IPAddress
		}
	}
	return out
}

// sharedAddr is the address of container name on a network probe is on too,
// or "" when they share none.
func sharedAddr(name, probe map[string]string) string {
	nets := make([]string, 0, len(name))
	for n := range name {
		nets = append(nets, n)
	}
	sort.Strings(nets)
	for _, n := range nets {
		if _, ok := probe[n]; ok {
			return name[n]
		}
	}
	return ""
}

// readyHost is the host a readiness probe dials the service container name
// by: host itself, or under gVisor the container's address on a network the
// probe shares with it.
func (e *dockerWorkspace) readyHost(ctx context.Context, name, probe, host string) string {
	if !e.gvisor() {
		return host
	}
	if addr := sharedAddr(e.containerAddrs(ctx, name), e.containerAddrs(ctx, probe)); addr != "" {
		return addr
	}
	return host
}
