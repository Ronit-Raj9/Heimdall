package k8s

import (
	"fmt"

	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Plan-time refusals (docs/kubernetes.md, "What is refused"). The driver
// runs one agent process per run on a repository task graded on its diff,
// clean or under a gateway fault. Everything else the Docker driver can do is
// refused here, before anything is frozen, with the reason; nothing runs half
// way. Each reason is a stable sentence, so the planner can list them once.
const (
	RefuseServices    = "tasks with services, external APIs or a mailbox are not supported (services run as Docker containers beside the agent)"
	RefuseTeam        = "multi-agent teams are not supported (one agent container per run)"
	RefuseSessions    = "multi-session tasks are not supported (no session reset between agent containers)"
	RefuseMemory      = "agent memory sections are not supported (no memory volume between sessions)"
	RefuseSkills      = "agent skills folders are not supported (plan folders are copied into Docker volumes)"
	RefuseSnapshot    = "snapshot grading is not supported (the agent's root file system is never committed)"
	RefuseTaskImage   = "tasks with their own environment image, workspace path or user are not supported (the workspace is seeded from the task repo only)"
	RefuseRoot        = "tasks that run the agent as root are not supported (agent containers run as non-root)"
	RefuseRemote      = "remote_http agents are not supported (they need the Docker egress forwarder)"
	RefuseNeutral     = "realism neutral is not supported (the neutral view renames Docker objects)"
	RefuseProbeLayer  = "probe layer %s is not supported (gateway, fixture and tool shim probes are)"
	RefuseMemberProbe = "member-targeted probes are not supported (no teams)"
	RefuseGPU         = "GPU tasks are not supported (no GPU request is passed to the pod)"
	RefuseCompose     = "compose tasks are not supported (their services run as Docker containers)"
	RefuseA2A         = "a2a agents are not supported (proven on the Docker driver only, docs/a2a-agents.md)" // B1
)

// Refusals implements environments.Driver.
func (d *Driver) Refusals(s environments.DriverSlot) []string {
	return refusals(s)
}

// refusals lists every reason the driver cannot run a slot. It is pure.
func refusals(s environments.DriverSlot) []string {
	var out []string
	add := func(r string) { out = append(out, r) }
	if s.Experiment != nil && schemas.EffectiveRealism(s.Experiment, s.Suite) == schemas.RealismNeutral {
		add(RefuseNeutral)
	} else if s.Experiment == nil && s.Suite != nil && s.Suite.Realism == schemas.RealismNeutral {
		add(RefuseNeutral)
	}
	if s.Suite != nil && utils.VerifierMode(s.Suite) == schemas.VerifierSnapshot {
		add(RefuseSnapshot)
	}
	if t := s.Task; t != nil {
		if len(utils.TaskServices(s.Suite, t)) > 0 || len(utils.ExternalAPIs(s.Suite, t)) > 0 {
			add(RefuseServices)
		}
		if len(t.Team) > 0 && (s.Agent == nil || (s.Agent.Topology != schemas.TopologySingle && s.Agent.Topology != schemas.TopologyBestOfN)) {
			add(RefuseTeam)
		}
		if len(t.Sessions) > 0 {
			add(RefuseSessions)
		}
		if t.GPUCount() > 0 {
			add(RefuseGPU)
		}
		if t.Compose != "" {
			add(RefuseCompose)
		}
		if t.Environment != nil {
			if utils.TaskRunsAsRoot(t) {
				add(RefuseRoot)
			} else {
				add(RefuseTaskImage)
			}
		}
	}
	if a := s.Agent; a != nil {
		if len(a.Team) > 0 {
			add(RefuseTeam)
		}
		if a.Memory != nil {
			add(RefuseMemory)
		}
		if a.Skills != nil {
			add(RefuseSkills)
		}
		if a.Adapter == "remote_http" || a.Remote != nil {
			add(RefuseRemote)
		}
		if a.Adapter == "a2a" || a.A2A != nil { // B1: a2a agents are proven on the Docker driver only (docs/a2a-agents.md)
			add(RefuseA2A)
		}
	}
	if p := s.Probe; p != nil {
		switch {
		case p.Layer != schemas.LayerGateway && p.Layer != schemas.LayerFixture && p.Layer != schemas.LayerToolShim:
			add(fmt.Sprintf(RefuseProbeLayer, p.Layer))
		case p.Apply.Agent != "":
			add(RefuseMemberProbe)
		}
	}
	return out
}
