package adapters

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

const (
	// SkillFile is the file that makes a folder a skill.
	SkillFile = "SKILL.md"
	// FallbackSkillsDir is where skills are installed for an agent whose CLI
	// has no skills folder the bench knows. The agent is told nothing: the
	// instruction is never changed, and BENCH_SKILLS_DIR names the folder.
	FallbackSkillsDir = "/opt/bench/skills"
	// EnvSkillsDir names the container folder that holds the run's skills.
	EnvSkillsDir = "BENCH_SKILLS_DIR"

	agentHome = "/home/agent"
)

// skillName is what a skill folder may be called: it becomes a container
// folder name and, for most CLIs, the skill's name.
var skillName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Skill is one skill folder an arm installs for a task.
type Skill struct {
	Name string // folder name inside the skills folder
	Host string // absolute host folder holding SKILL.md
}

// SkillSetsDir is the task folder that holds alternative skill sets, one
// subfolder per set, chosen by an agent config's skills.set.
const SkillSetsDir = "skill-sets"

func skillSet(cfg *schemas.AgentConfig) string {
	if cfg == nil || cfg.Skills == nil {
		return ""
	}
	return strings.TrimSpace(cfg.Skills.Set)
}

// SkillsEnabled reports whether an agent config installs skills at all:
// skills.enabled, true when unset.
func SkillsEnabled(cfg *schemas.AgentConfig) bool {
	return cfg == nil || cfg.Skills == nil || cfg.Skills.Enabled == nil || *cfg.Skills.Enabled
}

// SkillsDir returns the container folder skills are installed in:
// skills.dir when set, else the CLI's own user-level skills folder (Claude
// Code $CLAUDE_CONFIG_DIR/skills, Codex $CODEX_HOME/skills, OpenCode
// $XDG_CONFIG_HOME/opencode/skills), else FallbackSkillsDir. The CLI is the
// one the config's parser reads (nativeCLI). The folder must be absolute and
// outside the workspace, where installed skills would land in the diff.
func SkillsDir(cfg *schemas.AgentConfig) (string, error) {
	dir := ""
	if cfg.Skills != nil {
		dir = strings.TrimSpace(cfg.Skills.Dir)
	}
	if dir == "" {
		switch nativeCLI(cfg) {
		case ParserClaudeCode:
			dir = path.Join(envDir(cfg, "CLAUDE_CONFIG_DIR", agentHome+"/.claude"), "skills")
		case ParserCodex:
			dir = path.Join(envDir(cfg, "CODEX_HOME", agentHome+"/.codex"), "skills")
		case ParserOpenCode:
			dir = path.Join(envDir(cfg, "XDG_CONFIG_HOME", agentHome+"/.config"), "opencode", "skills")
		default:
			dir = FallbackSkillsDir
		}
	}
	if !path.IsAbs(dir) {
		return "", fmt.Errorf("skills.dir %q must be an absolute container path", dir)
	}
	dir = path.Clean(dir)
	if dir == "/" || dir == Workspace || strings.HasPrefix(dir, Workspace+"/") {
		return "", fmt.Errorf("skills.dir %q must be outside the workspace and not the root", dir)
	}
	return dir, nil
}

// ResolveSkills returns the skills an agent config installs for a task,
// sorted by name: the task's skills folder (Task.Skills, relative to the task
// folder) and every skills.extra entry (relative to the bench root). A folder
// that holds SKILL.md is one skill; any other folder is a collection whose
// subfolders are skills, each of which must hold SKILL.md (hidden entries and
// loose files in a collection are skipped). Every path must stay inside its
// root after symlinks are resolved, and two skills may not share a name.
// A skill an agent uploaded (agents/<id>/skills/<name>) must have a passing
// scan of its current files (skillgate.go); an upload collection that is
// missing or empty installs nothing. While RestrictSkillsExtra is on (hosted
// server, shared worker), skills.extra may name only upload collections
// (skillextra.go). skills.enabled false returns none.
func ResolveSkills(benchRoot string, cfg *schemas.AgentConfig, task *schemas.Task) ([]Skill, error) {
	if !SkillsEnabled(cfg) {
		return nil, nil
	}
	type source struct {
		root, rel, field string
		extra            bool // a skills.extra entry: an agent's upload collection is gated by its scans
	}
	var sources []source
	if set := skillSet(cfg); set != "" {
		if !skillName.MatchString(set) {
			return nil, fmt.Errorf("skills.set %q must match %s", set, skillName)
		}
		if task != nil {
			rel := path.Join(SkillSetsDir, set)
			if fi, err := os.Stat(filepath.Join(task.Dir, filepath.FromSlash(rel))); err == nil && fi.IsDir() {
				sources = append(sources, source{task.Dir, rel, "task " + task.ID + " skill set " + set, false})
			}
		}
	} else if task != nil && task.Skills != "" {
		sources = append(sources, source{task.Dir, task.Skills, "task " + task.ID + " skills", false})
	}
	if cfg.Skills != nil {
		for i, e := range cfg.Skills.Extra {
			sources = append(sources, source{benchRoot, e, fmt.Sprintf("skills.extra[%d]", i), true})
		}
	}
	from := map[string]string{} // skill name -> source field
	var out []Skill
	for _, src := range sources {
		restricted := src.extra && SkillsExtraRestricted()
		if restricted && !extraEntryAllowed(src.rel) {
			return nil, fmt.Errorf("%s %q: %w", src.field, src.rel, errSkillsExtra)
		}
		if src.extra && emptyUploadCollection(src.root, src.rel) {
			continue
		}
		dir, err := resolveInside(src.root, src.rel)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", src.field, err)
		}
		if restricted && !extraDirAllowed(src.root, dir) {
			return nil, fmt.Errorf("%s %q: %w", src.field, src.rel, errSkillsExtra)
		}
		found, err := skillsIn(dir)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", src.field, err)
		}
		if src.extra {
			if err := gateExtraSkills(src.root, dir, found); err != nil {
				return nil, err
			}
		}
		for _, s := range found {
			if prev, dup := from[s.Name]; dup {
				return nil, fmt.Errorf("skill %q comes from both %s and %s", s.Name, prev, src.field)
			}
			from[s.Name] = src.field
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// gateExtraSkills applies the skill scan gate (skillgate.go) to the skills
// a skills.extra entry resolved to: every skill of an agent's upload
// collection needs a passing scan of its current files.
func gateExtraSkills(root, dir string, found []Skill) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolve root: %w", err)
	}
	for _, s := range found {
		agent, _, ok := uploadCollection(realRoot, s.Host)
		if !ok {
			continue
		}
		if err := gateUploadedSkill(realRoot, agent, s); err != nil {
			return err
		}
	}
	return nil
}

// SkillHashes returns container path -> sha256 for every file of every skill
// an agent config installs for a task (ResolveSkills, under SkillsDir). JSON
// encodes the map with sorted keys, so equal skills always hash equal. It is
// empty when the arm installs no skills.
func SkillHashes(benchRoot string, cfg *schemas.AgentConfig, task *schemas.Task) (map[string]string, error) {
	skills, err := ResolveSkills(benchRoot, cfg, task)
	if err != nil || len(skills) == 0 {
		return map[string]string{}, err
	}
	dir, err := SkillsDir(cfg)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, s := range skills {
		err := filepath.WalkDir(s.Host, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, err := filepath.Rel(s.Host, p)
			if err != nil {
				return err
			}
			sum, err := utils.SHA256File(p)
			if err != nil {
				return err
			}
			out[path.Join(dir, s.Name, filepath.ToSlash(rel))] = sum
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("hash skill %s: %w", s.Name, err)
		}
	}
	return out, nil
}

// AddSkillHashes adds SkillHashes for one task to a manifest's per-condition
// files map, keyed "<task id>:<container path>" like the planner's other
// per-task files. It adds nothing when the arm installs no skills, or runs
// through an adapter that installs none (only the command adapter does), so a
// manifest without skills is unchanged.
func AddSkillHashes(files map[string]string, benchRoot string, cfg *schemas.AgentConfig, task *schemas.Task) error {
	if cfg.Adapter != AdapterCommand {
		return nil
	}
	sums, err := SkillHashes(benchRoot, cfg, task)
	if err != nil {
		return fmt.Errorf("skills: %w", err)
	}
	for p, sum := range sums {
		files[task.ID+":"+p] = sum
	}
	return nil
}

// addSkills installs the run's skills: one RunPlan.Dirs entry per skill
// folder under SkillsDir, and BENCH_SKILLS_DIR. A run without skills leaves
// the plan untouched.
func addSkills(plan *schemas.RunPlan, benchRoot string, rc *schemas.RunContext) error {
	skills, err := ResolveSkills(benchRoot, rc.Agent, rc.Task)
	if err != nil || len(skills) == 0 {
		return err
	}
	dir, err := SkillsDir(rc.Agent)
	if err != nil {
		return err
	}
	if plan.Dirs == nil {
		plan.Dirs = map[string]string{}
	}
	for _, s := range skills {
		plan.Dirs[path.Join(dir, s.Name)] = s.Host
	}
	if plan.Env == nil {
		plan.Env = map[string]string{}
	}
	if _, ok := plan.Env[EnvSkillsDir]; ok {
		return fmt.Errorf("agent env already defines %s", EnvSkillsDir)
	}
	plan.Env[EnvSkillsDir] = dir
	return nil
}

// nativeCLI names the CLI an agent config runs, read from its parser: every
// shipped CLI runs through the command adapter, and the parser is the one
// field that says which CLI it is. "" for any other agent.
func nativeCLI(cfg *schemas.AgentConfig) string {
	switch cfg.Parser {
	case ParserClaudeCode, ParserCodex, ParserOpenCode:
		return cfg.Parser
	}
	return ""
}

// envDir returns the folder an agent config's env sets for name, or def when
// the config does not set it to a literal absolute path (a template value
// cannot be known at plan time). The shipped images set the same defaults.
func envDir(cfg *schemas.AgentConfig, name, def string) string {
	v := strings.TrimSpace(cfg.Env[name])
	if v == "" || strings.Contains(v, "{{") || !path.IsAbs(v) {
		return def
	}
	return path.Clean(v)
}

// skillsIn lists the skills in dir: dir itself when it holds SKILL.md, else
// each visible subfolder, which must hold SKILL.md.
func skillsIn(dir string) ([]Skill, error) {
	if ok, err := isSkill(dir); err != nil || ok {
		if err != nil {
			return nil, err
		}
		s := Skill{Name: filepath.Base(dir), Host: dir}
		return []Skill{s}, checkSkill(s)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read skills folder: %w", err)
	}
	var out []Skill
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") || !e.IsDir() {
			if e.Type()&fs.ModeSymlink != 0 {
				return nil, fmt.Errorf("skills folder entry %s is a symlink", e.Name())
			}
			continue
		}
		s := Skill{Name: e.Name(), Host: filepath.Join(dir, e.Name())}
		if ok, err := isSkill(s.Host); err != nil || !ok {
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("skill folder %s holds no %s", e.Name(), SkillFile)
		}
		if err := checkSkill(s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s holds no %s and no skill folders", dir, SkillFile)
	}
	return out, nil
}

// isSkill reports whether dir holds a regular SKILL.md.
func isSkill(dir string) (bool, error) {
	fi, err := os.Lstat(filepath.Join(dir, SkillFile))
	switch {
	case os.IsNotExist(err):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("stat %s: %w", SkillFile, err)
	case !fi.Mode().IsRegular():
		return false, fmt.Errorf("%s in %s is not a regular file", SkillFile, filepath.Base(dir))
	}
	return true, nil
}

// checkSkill refuses a skill whose name cannot be a folder name in the
// container, or whose folder holds anything but folders and regular files.
// A symlink is refused outright: the folder is copied from the host, and a
// link could carry a host file into the agent's container.
func checkSkill(s Skill) error {
	if !skillName.MatchString(s.Name) {
		return fmt.Errorf("skill folder name %q must match %s", s.Name, skillName)
	}
	return filepath.WalkDir(s.Host, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Type().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(s.Host, p)
		return fmt.Errorf("skill %s: %s is not a regular file or folder", s.Name, filepath.ToSlash(rel))
	})
}

// resolveInside resolves rel under root and refuses a result outside root,
// symlinks included. An absolute rel must already lie inside root.
func resolveInside(root, rel string) (string, error) {
	if strings.TrimSpace(rel) == "" {
		return "", fmt.Errorf("empty path")
	}
	if root == "" {
		return "", fmt.Errorf("path %q has no root to resolve against", rel)
	}
	p := rel
	if !filepath.IsAbs(p) {
		if c := filepath.Clean(filepath.FromSlash(rel)); c != "." && !filepath.IsLocal(c) {
			return "", fmt.Errorf("path %q leaves %s", rel, root)
		}
		p = filepath.Join(root, filepath.FromSlash(rel))
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", rel, err)
	}
	inside, err := filepath.Rel(realRoot, real)
	if err != nil || !filepath.IsLocal(inside) && inside != "." {
		return "", fmt.Errorf("path %q leaves %s", rel, root)
	}
	fi, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("path %q is not a folder", rel)
	}
	return real, nil
}
