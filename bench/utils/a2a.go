package utils

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// B1: A2A agents through Bifrost's agent gateway (docs/a2a-agents.md).

// a2aMemberID is a team member id: it becomes part of a registration name,
// which Bifrost allows as lowercase letters, digits and single hyphens.
var a2aMemberID = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// A2ANoCredentialReason marks the gateway log of an a2a run whose agent got
// no Bifrost credential: its model calls, if any, are not on the run key.
const A2ANoCredentialReason = "a2a agent with credential none: its model calls are not made on the run key, so tokens, cost and steps are unknown"

// A2ALead is the member id of the entry agent of an A2A team.
const A2ALead = "lead"

// IsA2A reports whether a config is an a2a agent.
func IsA2A(a *schemas.AgentConfig) bool {
	return a != nil && (a.Adapter == "a2a" || a.A2A != nil)
}

// CheckA2AAgent refuses an a2a block the adapter cannot run. It is pure.
func CheckA2AAgent(s *schemas.A2AAgent) error {
	if s == nil {
		return fmt.Errorf("adapter a2a needs an a2a block")
	}
	switch {
	case s.CardURL == "" && s.Registration == "":
		return fmt.Errorf("a2a: set card_url (the bench registers the agent per run) or registration (an existing registration that allows any virtual key)")
	case s.CardURL != "" && s.Registration != "":
		return fmt.Errorf("a2a: set card_url or registration, not both")
	case s.CardURL != "" && !strings.HasPrefix(s.CardURL, "http://") && !strings.HasPrefix(s.CardURL, "https://"):
		return fmt.Errorf("a2a.card_url %q is not an http(s) URL", s.CardURL)
	case s.Registration != "" && !a2aMemberID.MatchString(s.Registration):
		return fmt.Errorf("a2a.registration %q is not a Bifrost agent name (lowercase letters, digits, single hyphens)", s.Registration)
	}
	switch s.Credential {
	case "", schemas.A2ACredentialForward, schemas.A2ACredentialNone:
	default:
		return fmt.Errorf("a2a.credential %q: want forward or none", s.Credential)
	}
	switch s.Tools {
	case "", schemas.RemoteToolsOff, schemas.RemoteToolsExpose:
	default:
		return fmt.Errorf("a2a.tools %q: want expose or off", s.Tools)
	}
	switch s.Artifacts {
	case "", "none", schemas.RemoteArtifactsInline:
	default:
		return fmt.Errorf("a2a.artifacts %q: want inline or none", s.Artifacts)
	}
	if len(s.Team) > 0 && s.Registration != "" {
		return fmt.Errorf("a2a.team needs card_url: team members are registered per run beside the entry agent")
	}
	seen := map[string]bool{A2ALead: true}
	for _, m := range s.Team {
		switch {
		case !a2aMemberID.MatchString(m.ID):
			return fmt.Errorf("a2a.team member id %q: lowercase letters, digits and single hyphens", m.ID)
		case seen[m.ID]:
			return fmt.Errorf("a2a.team member id %q is used twice (lead is the entry agent)", m.ID)
		case !strings.HasPrefix(m.CardURL, "http://") && !strings.HasPrefix(m.CardURL, "https://"):
			return fmt.Errorf("a2a.team member %s: card_url %q is not an http(s) URL", m.ID, m.CardURL)
		case m.BudgetUSD < 0:
			return fmt.Errorf("a2a.team member %s: budget_usd is negative", m.ID)
		}
		seen[m.ID] = true
	}
	return CheckConversation(s.Conversation, "a2a") // Y11
}

// A2AOutputAllowed says whether an a2a agent can produce a task's output
// kind, and why not. The agent is not beside the workspace, so the rules
// are the remote agent's (RemoteOutputAllowed) with the a2a block's tools
// and artifacts settings.
func A2AOutputAllowed(s *schemas.A2AAgent, kind string) (bool, string) {
	view := A2ARemoteView(s)
	ok, why := RemoteOutputAllowed(view, kind)
	if !ok {
		why = strings.NewReplacer("remote.tools", "a2a.tools", "remote.artifacts", "a2a.artifacts", "a remote agent", "an A2A agent", "A remote agent", "An A2A agent").Replace(why)
	}
	return ok, why
}

// A2ARemoteView is the remote block an a2a block behaves like for the
// planner's output-kind rules and the published tool gate.
func A2ARemoteView(s *schemas.A2AAgent) *schemas.RemoteAgent {
	if s == nil {
		return nil
	}
	return &schemas.RemoteAgent{Tools: s.Tools, Artifacts: s.Artifacts}
}

// A2AFaultOf is the a2a probe's client-side stream fault of a run, or nil.
func A2AFaultOf(rc *schemas.RunContext) *schemas.A2AFault {
	if rc == nil || rc.Probe == nil || rc.Probe.Layer != schemas.LayerA2A {
		return nil
	}
	return rc.Probe.Apply.A2A
}

// A2ARegistrationName is the run-scoped registration name of a member:
// "hb-" plus the run id with every other character turned into a hyphen,
// plus the member id. At most 63 characters (a DNS label, Bifrost's gRPC
// limit); a longer one is cut and keeps its uniqueness from the run id's
// tail.
func A2ARegistrationName(runID, member string) string {
	var b strings.Builder
	last := byte('-')
	for i := 0; i < len(runID); i++ {
		c := runID[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c >= 'A' && c <= 'Z':
			c += 'a' - 'A'
		default:
			c = '-'
		}
		if c == '-' && last == '-' {
			continue
		}
		b.WriteByte(c)
		last = c
	}
	id := strings.Trim(b.String(), "-")
	name := "hb-" + id + "-" + member
	if len(name) > 63 {
		keep := 63 - len("hb--") - len(member)
		if keep < 8 {
			keep = 8
		}
		if len(id) > keep {
			id = strings.Trim(id[len(id)-keep:], "-")
		}
		name = "hb-" + id + "-" + member
		if len(name) > 63 {
			// 3-A-15: a cut member id keeps a digest of the whole id, so two
			// long member ids with one prefix stay distinct.
			sum := sha256.Sum256([]byte(member))
			tag := "-" + hex.EncodeToString(sum[:])[:10]
			name = strings.Trim(name[:63-len(tag)], "-") + tag
		}
	}
	return name
}

// A2ATeam is the effective member topology of an a2a run: the entry agent
// as lead (id lead) and each a2a.team member, whose empty or "lead" role is
// a worker. The engine mints member keys from it and the gateway probe
// layer resolves apply.agent roles against it, so planning, minting and
// arming agree on who is a worker. Nil for an agent without an a2a team.
func A2ATeam(cfg *schemas.AgentConfig) []schemas.TeamMember {
	if cfg == nil || cfg.A2A == nil || len(cfg.A2A.Team) == 0 {
		return nil
	}
	members := []schemas.TeamMember{{ID: "lead", Role: "lead"}}
	for _, m := range cfg.A2A.Team {
		role := m.Role
		if role == "" || role == "lead" {
			role = "worker"
		}
		members = append(members, schemas.TeamMember{ID: m.ID, Role: role, BudgetUSD: m.BudgetUSD})
	}
	return members
}
