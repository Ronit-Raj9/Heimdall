package sandbox

import (
	"fmt"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Plan-time refusals: one agent process per run on a repository task graded
// on its diff, clean or under a gateway fault. Each reason is a stable
// sentence so the planner lists it once; plainRefusals below holds the words
// a person reads for each of them (fix wave 3, N216).
const (
	RefuseNoPublicGateway = "the gateway has no public URL (gateway.public_url), so an agent in a cloud sandbox cannot reach it"
	RefuseServices        = "tasks with services, external APIs or a mailbox are not supported (services run as Docker containers beside the agent)"
	RefuseTeam            = "multi-agent teams are not supported (one sandbox per run)"
	RefuseSessions        = "multi-session tasks are not supported (no session reset between sandboxes)"
	RefuseMemory          = "agent memory sections are not supported (no memory volume between sessions)"
	RefuseSkills          = "agent skills folders are not supported (plan folders are copied into Docker volumes)"
	RefuseSnapshot        = "snapshot grading is not supported (the sandbox's root file system is never committed)"
	RefuseTaskImage       = "tasks with their own environment image, workspace path or user are not supported (the workspace is seeded from the task repo only)"
	RefuseRemote          = "remote_http agents are not supported (they need the Docker egress forwarder)"
	RefuseA2A             = "a2a agents are not supported (proven on the Docker driver only)"
	RefuseNeutral         = "realism neutral is not supported (the neutral view renames Docker objects)"
	RefuseProbeLayer      = "fault layer %s is not supported in a cloud sandbox (gateway faults are)"
	RefuseGPU             = "GPU tasks are not supported (no GPU is requested from the platform)"
	RefuseGPUFit          = "GPU task cannot be placed: %s"
	RefuseCompose         = "compose tasks are not supported (their services run as Docker containers)"
	RefuseAnswer          = "answer-graded tasks are not supported (the sandbox driver cannot export the agent's answer)"
	RefuseArtifact        = "artifact-graded tasks are not supported (the sandbox driver cannot export the agent's files)"
)

// plainRefusals maps each fixed refusal to the words a person reads.
var plainRefusals = map[string]string{
	RefuseNoPublicGateway: "the gateway has no public address a sandbox can reach",
	RefuseServices:        "its tasks need extra services beside the agent",
	RefuseCompose:         "its tasks need extra services beside the agent",
	RefuseTeam:            "it needs a team of agents",
	RefuseSessions:        "its tasks run over several sessions",
	RefuseMemory:          "the agent keeps memory between sessions",
	RefuseSkills:          "the agent has skills folders",
	RefuseSnapshot:        "it is graded from a copy of the whole machine, which a Modal sandbox cannot make",
	RefuseTaskImage:       "its tasks bring their own machine image",
	RefuseRemote:          "the agent is reached over the network, not run in the sandbox",
	RefuseA2A:             "the agent is reached over the network, not run in the sandbox",
	RefuseNeutral:         "it uses the neutral realism view, which works on Docker workers only",
	RefuseGPU:             "it needs a GPU",
	RefuseAnswer:          "it is graded from an answer or file the sandbox cannot hand back",
	RefuseArtifact:        "it is graded from an answer or file the sandbox cannot hand back",
}

// Refuse lists why a cloud sandbox cannot run the slot, without the two
// checks that depend on the driver itself (a public gateway URL and GPU
// placement, see Driver.Refusals). It is pure, sorted and without
// duplicates, so the server can refuse an evaluation before launch with the
// same rules the shared runner applies at plan time.
func Refuse(s environments.DriverSlot) []string {
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
		if t.Compose != "" {
			add(RefuseCompose)
		}
		if t.Environment != nil {
			rest := *t.Environment
			rest.Env = nil
			if !isZeroTaskEnv(rest) {
				add(RefuseTaskImage)
			}
		}
		switch utils.OutputKind(s.Suite, t) {
		case schemas.OutputAnswer:
			add(RefuseAnswer)
		case schemas.OutputArtifact:
			add(RefuseArtifact)
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
		if a.Adapter == "a2a" || a.A2A != nil {
			add(RefuseA2A)
		}
	}
	if p := s.Probe; p != nil && p.Layer != schemas.LayerGateway {
		add(fmt.Sprintf(RefuseProbeLayer, p.Layer))
	}
	sort.Strings(out)
	return dedupe(out)
}

// PlainRefusal is the words a person reads for one refusal of Refuse or
// Driver.Refusals: "it is graded from a copy of the whole machine, ...". A
// reason it does not know is returned unchanged.
func PlainRefusal(reason string) string {
	if p, ok := plainRefusals[reason]; ok {
		return p
	}
	var layer string
	if n, _ := fmt.Sscanf(reason, RefuseProbeLayer, &layer); n == 1 && layer != "" {
		return "the " + layer + " failure test works on Docker workers only"
	}
	if strings.HasPrefix(reason, strings.TrimSuffix(RefuseGPUFit, "%s")) {
		return "it needs a GPU this platform cannot give"
	}
	return reason
}

// PlainRefusals maps reasons through PlainRefusal, without duplicates, in
// the order given.
func PlainRefusals(reasons []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range reasons {
		p := PlainRefusal(r)
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// PlatformTitle is the display name of a sandbox platform ("Modal" for
// "modal"); an unknown name is returned unchanged.
func PlatformTitle(name string) string {
	if info, ok := Lookup(name); ok && info.Title != "" {
		return info.Title
	}
	return name
}

// NeedsRegistry is the sentence for a platform that has no image registry
// it can pull Heimdall's images from (fix wave 3, N216): "Modal needs an
// image registry it can pull from. Add one on the Modal card."
func NeedsRegistry(platform string) string {
	t := PlatformTitle(platform)
	return t + " needs an image registry it can pull from. Add one on the " + t + " card."
}
