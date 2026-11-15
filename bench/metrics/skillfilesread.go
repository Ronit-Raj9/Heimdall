package metrics

import (
	"context"
	"path"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// SkillFilesRead is the name of the skill-file read count.
const SkillFilesRead = "skill_files_read"

// Parser names and skill folders the metric reads from an agent config. They
// mirror adapters/skills.go (SkillsDir), which the metrics package does not
// import: the folder a run's skills were installed in.
const (
	parserClaudeCode  = "claudecode"
	parserCodex       = "codex"
	parserOpenCode    = "opencode"
	fallbackSkillsDir = "/opt/bench/skills"
	agentHome         = "/home/agent"
)

// skillTools are the tools that load a skill by name instead of by path:
// Claude Code's Skill and OpenCode's skill.
var skillTools = map[string]bool{"Skill": true, "skill": true}

var _ schemas.Metric = (*skillFilesReadMetric)(nil)

// skillFilesReadMetric counts the distinct files of the run's installed
// skills that the agent's own stream shows it reading. It is a report-only
// diagnostic for skill comparisons: it says whether an arm with skills used
// them, never whether it did better. Its direction is higher-is-better only
// because using the skills an arm installs is the point of installing them.
type skillFilesReadMetric struct{}

// GetName implements schemas.Part.
func (m *skillFilesReadMetric) GetName() string { return SkillFilesRead }

// GetVersion implements schemas.Part.
func (m *skillFilesReadMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *skillFilesReadMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name:           SkillFilesRead,
		Version:        version,
		Label:          "Skill files read",
		Kind:           schemas.KindCount,
		HigherIsBetter: true,
		MinEvidence:    schemas.EvidenceNative,
		Description:    "Distinct files of the run's installed skills the agent read, from its own stream, over runs with skills and a parsed stream; report only.",
	}
}

// Compute implements schemas.Metric. It is not_applicable for a run that
// installed no skills (skills disabled, or neither the task nor the config
// has any). It is unknown when the agent's stream gave no events, since a
// run without a parser shows no file reads at all. Otherwise it counts, over
// agent_stream tool calls, every distinct path inside an installed skill
// folder that a call's target names (a Read of a file, a shell command that
// prints one), plus each distinct call of a tool that loads a skill by name.
// A hidden folder in the skills folder (Codex keeps its bundled skills in
// .system) is not an installed skill. It is a lower bound: a file reached
// through a relative path or a variable the metric cannot expand is missed.
func (m *skillFilesReadMetric) Compute(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	if !runHasSkills(rc) {
		return schemas.NotApplicable(), nil
	}
	ev = orEmpty(ev)
	roots := skillRoots(rc.Agent)
	streamed := false
	read := map[string]struct{}{}
	for _, e := range ev.Events {
		if e.Source != schemas.SourceAgentStream {
			continue
		}
		streamed = true
		if e.Type != schemas.EventToolCall {
			continue
		}
		if skillTools[e.ToolName] {
			key := strings.TrimSpace(e.Target)
			if key == "" {
				key = e.ArgsHash
			}
			read["tool:"+e.ToolName+":"+key] = struct{}{}
			continue
		}
		for _, p := range skillPaths(e.Target, roots) {
			read[p] = struct{}{}
		}
	}
	if !streamed {
		return schemas.Unknown("no agent_stream events: the agent's stream gives no file reads", schemas.EvidenceNative), nil
	}
	return schemas.Observed(float64(len(read)), schemas.EvidenceNative), nil
}

// newSkillFilesReadMetric returns the skill_files_read metric.
func newSkillFilesReadMetric() *skillFilesReadMetric {
	return &skillFilesReadMetric{}
}

// runHasSkills reports whether the run installed skills: skills not disabled,
// and a task skills folder or a config skills.extra entry.
func runHasSkills(rc *schemas.RunContext) bool {
	if rc == nil || rc.Agent == nil {
		return false
	}
	s := rc.Agent.Skills
	if s != nil && s.Enabled != nil && !*s.Enabled {
		return false
	}
	return (rc.Task != nil && rc.Task.Skills != "") || (s != nil && len(s.Extra) > 0)
}

// skillRoots returns the skills folder as it may appear in a tool call's
// target: absolute, and under the home as ~/ and $HOME/.
func skillRoots(cfg *schemas.AgentConfig) []string {
	dir := ""
	if cfg.Skills != nil {
		dir = strings.TrimSpace(cfg.Skills.Dir)
	}
	if dir == "" {
		switch cfg.Parser {
		case parserClaudeCode:
			dir = path.Join(envFolder(cfg, "CLAUDE_CONFIG_DIR", agentHome+"/.claude"), "skills")
		case parserCodex:
			dir = path.Join(envFolder(cfg, "CODEX_HOME", agentHome+"/.codex"), "skills")
		case parserOpenCode:
			dir = path.Join(envFolder(cfg, "XDG_CONFIG_HOME", agentHome+"/.config"), "opencode", "skills")
		default:
			dir = fallbackSkillsDir
		}
	}
	dir = path.Clean(dir)
	roots := []string{dir}
	if rest, ok := strings.CutPrefix(dir, agentHome+"/"); ok {
		roots = append(roots, "~/"+rest, "$HOME/"+rest, "${HOME}/"+rest)
	}
	return roots
}

// envFolder is the literal absolute folder an agent config's env sets for
// name, else def.
func envFolder(cfg *schemas.AgentConfig, name, def string) string {
	v := strings.TrimSpace(cfg.Env[name])
	if v == "" || strings.Contains(v, "{{") || !path.IsAbs(v) {
		return def
	}
	return path.Clean(v)
}

// skillPaths returns every file path inside an installed skill folder that
// target names, as <skill>/<file>. A path must name a file in a skill folder
// (a listing of the skills folder or of one skill names no file), and a
// skill folder name may not be hidden or a glob.
func skillPaths(target string, roots []string) []string {
	var out []string
	for _, root := range roots {
		rest := target
		for {
			i := strings.Index(rest, root+"/")
			if i < 0 {
				break
			}
			rest = rest[i+len(root)+1:]
			end := strings.IndexAny(rest, " \t\n\"'`;|&<>()")
			tok := rest
			if end >= 0 {
				tok = rest[:end]
			}
			name, file, ok := strings.Cut(tok, "/")
			if !ok || name == "" || strings.HasPrefix(name, ".") || strings.ContainsAny(name+file, "*?[") {
				continue
			}
			file = path.Clean(file)
			if file == "." || file == ".." || strings.HasPrefix(file, "../") {
				continue
			}
			out = append(out, name+"/"+file)
		}
	}
	return out
}
