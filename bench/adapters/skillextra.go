package adapters

import (
	"errors"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/maximhq/bifrost/bench/schemas"
)

// SkillsExtraRefusal is the error text when skills.extra names anything but
// an agent's upload collection on a server that restricts it.
const SkillsExtraRefusal = "skills.extra may only name an agent's uploaded skills (agents/<id>/skills) on this server. Upload the skill on the agent's Tools tab."

// restrictSkillsExtra is set once at start by the hosted server and the
// shared worker (fix wave 3, SEC-B, D34). When on, every skills.extra entry
// must be an agent's upload collection agents/<id>/skills or one skill
// folder in it, where every skill is gated by its scan record
// (skillgate.go). Local servers leave it off.
var restrictSkillsExtra atomic.Bool

// RestrictSkillsExtra switches the skills.extra rule on or off.
func RestrictSkillsExtra(on bool) { restrictSkillsExtra.Store(on) }

// SkillsExtraRestricted reports whether the skills.extra rule is on.
func SkillsExtraRestricted() bool { return restrictSkillsExtra.Load() }

var errSkillsExtra = errors.New(SkillsExtraRefusal) //nolint:staticcheck // ST1005: a full sentence shown to the user as is

// uploadCollectionRel reports whether the slash path rel (relative to the
// project) is agents/<id>/skills or agents/<id>/skills/<name>.
func uploadCollectionRel(rel string) bool {
	parts := strings.Split(rel, "/")
	if len(parts) < 3 || len(parts) > 4 || parts[0] != "agents" || parts[2] != SkillUploadDir {
		return false
	}
	for _, p := range parts[1:] {
		if p == "" || p == "." || p == ".." {
			return false
		}
	}
	return true
}

// CheckSkillsExtra refuses an agent config whose skills.extra names anything
// but an agent's upload collection while the rule is on. It reads the
// config only; ResolveSkills checks the resolved folders at plan and run
// time.
func CheckSkillsExtra(cfg *schemas.AgentConfig) error {
	if !SkillsExtraRestricted() || cfg == nil || cfg.Skills == nil {
		return nil
	}
	for _, e := range cfg.Skills.Extra {
		if !extraEntryAllowed(e) {
			return errSkillsExtra
		}
	}
	return nil
}

// extraEntryAllowed checks one skills.extra entry by its text.
func extraEntryAllowed(e string) bool {
	e = strings.TrimSpace(strings.ReplaceAll(e, "\\", "/"))
	if e == "" || path.IsAbs(e) || filepath.IsAbs(e) {
		return false
	}
	c := path.Clean(e)
	if c == ".." || strings.HasPrefix(c, "../") {
		return false
	}
	return uploadCollectionRel(c)
}

// extraDirAllowed checks the folder a skills.extra entry resolved to
// (symlinks followed) against the project root.
func extraDirAllowed(root, dir string) bool {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(realRoot, dir)
	if err != nil || !filepath.IsLocal(rel) {
		return false
	}
	return uploadCollectionRel(filepath.ToSlash(rel))
}
