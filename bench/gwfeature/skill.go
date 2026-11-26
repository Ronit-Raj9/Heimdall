package gwfeature

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// W4 feature 6 (audit/W4_DESIGN_3_BIFROST.md section 9): two versions of a
// skill from Bifrost's Skills Repository, each imported into the agent's
// upload folder (POST /agents/{id}/skills/from-bifrost), as two setups whose
// skills.extra differ. Bifrost is not in the path at run time, so there is no
// applied check from gateway rows; the skill_files_read metric shows whether
// the agent opened the skill.

// skillExtraPathRe is an imported skill folder: agents/<agent>/skills/<folder>.
var skillExtraPathRe = regexp.MustCompile(`^agents/[A-Za-z0-9][A-Za-z0-9._-]{0,63}/skills/[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// skillFolderVersionRe splits a folder <name>-v<version>.
var skillFolderVersionRe = regexp.MustCompile(`^(.+)-v([^-]+)$`)

const skillFilesReadMetric = "skill_files_read" // metrics.SkillFilesRead

func init() {
	Register(Feature{
		ID:      schemas.GatewayFeatureSkillVersion,
		Name:    "Skills repository version",
		Draft:   skillDraft,
		Compute: skillCompute,
	})
}

// skillNameVersion reads the skill name and version from an extra path.
func skillNameVersion(path string) (name, version string) {
	folder := path[strings.LastIndex(path, "/")+1:]
	if m := skillFolderVersionRe.FindStringSubmatch(folder); m != nil {
		return m[1], m[2]
	}
	return folder, ""
}

// skillLabel is "skill review v4".
func skillLabel(path string) string {
	name, v := skillNameVersion(path)
	if v == "" {
		return "skill " + name
	}
	return fmt.Sprintf("skill %s v%s", name, v)
}

func skillCheckPath(p string) error {
	if !skillExtraPathRe.MatchString(p) || strings.Contains(p, "..") {
		return fmt.Errorf("The skill path %q is not a folder imported from Bifrost (agents/<agent>/skills/<folder>).", p)
	}
	return nil
}

func skillDraft(_ context.Context, env DraftEnv, req DraftRequest) (*schemas.Experiment, []string, error) {
	control := strings.TrimSpace(req.Options["control_path"])
	variant := strings.TrimSpace(req.Options["variant_path"])
	if control == "" {
		control = "none"
	}
	if variant == "" {
		return nil, nil, errors.New("Pick the skill version to test.")
	}
	if err := skillCheckPath(variant); err != nil {
		return nil, nil, err
	}
	if control != "none" {
		if err := skillCheckPath(control); err != nil {
			return nil, nil, err
		}
	}
	if control == variant {
		return nil, nil, errors.New("Pick two different skill versions.")
	}
	exp := promptBaseExperiment(env, req)
	if control == "none" {
		exp.Arms["A"] = schemas.ArmSpec{Label: "no extra skill", Set: promptArmSet(req, nil)}
	} else {
		exp.Arms["A"] = schemas.ArmSpec{Label: skillLabel(control), Set: promptArmSet(req, map[string]any{"skills.extra": []any{control}})}
	}
	exp.Arms["B"] = schemas.ArmSpec{Label: skillLabel(variant), Set: promptArmSet(req, map[string]any{"skills.extra": []any{variant}})}
	name, _ := skillNameVersion(variant)
	exp.Name = "Bifrost test: skill " + name
	exp.GatewayTest = &schemas.GatewayTest{Feature: schemas.GatewayFeatureSkillVersion, Control: "A", Variants: []string{"B"}, Options: req.Options}
	notes := []string{
		"The setup's skills.extra replaces the agent's own extra skills list, so each setup has only the chosen skill version plus the task's own skills. The installed folder names differ (" + skillFolders(control, variant) + "); the skill's name inside SKILL.md is the same.",
	}
	return exp, notes, nil
}

// skillFolders is "review-v3, review-v4" for the note.
func skillFolders(paths ...string) string {
	var out []string
	for _, p := range paths {
		if p == "none" {
			continue
		}
		out = append(out, p[strings.LastIndex(p, "/")+1:])
	}
	return strings.Join(out, ", ")
}

// skillReads is the run's skill_files_read value, and whether it was observed.
func skillReads(in Input, runID string) (float64, bool) {
	if in.Src == nil {
		return 0, false
	}
	data, err := in.Src.RunFile(runID, "metrics.json")
	if err != nil {
		return 0, false
	}
	var m map[string]schemas.Value
	if json.Unmarshal(data, &m) != nil {
		return 0, false
	}
	v, ok := m[skillFilesReadMetric]
	if !ok || v.V == nil {
		return 0, false
	}
	return *v.V, true
}

func skillCompute(_ context.Context, in Input) Effect {
	control := in.Test.Control
	variant := ""
	if len(in.Test.Variants) > 0 {
		variant = in.Test.Variants[0]
	}
	e := Effect{Variant: variant, Arms: ArmResults(in.Runs, in.Manifest, append([]string{control}, in.Test.Variants...))}
	cp, vp := in.Test.Options["control_path"], in.Test.Options["variant_path"]
	_, cv := skillNameVersion(cp)
	name, vv := skillNameVersion(vp)
	if cp == "" || cp == "none" {
		cv = "none"
	}
	e.Source.Text = fmt.Sprintf("Source: Bifrost Skills Repository, versions %s and %s; scanned and admitted", cv, vv)

	// The agent must have opened the variant's skill, when every run says.
	for _, arm := range in.Test.Variants {
		runs, observed, read := 0, 0, 0.0
		for _, r := range in.Runs {
			if r.Arm != arm {
				continue
			}
			runs++
			if v, ok := skillReads(in, r.RunID); ok {
				observed++
				read += v
			}
		}
		if runs > 0 && observed == runs && read == 0 {
			e.State, e.Result = StateNotApplied, "failed"
			e.Reason = "the agent did not open the skill in any run; the comparison measures nothing"
			e.Headline = "Not applied: " + e.Reason
			return e
		}
	}

	solve := PairedTasks(in.Runs, control, variant, SolveValue)
	cost := PairedTasks(in.Runs, control, variant, CostValue)
	lat := PairedTasks(in.Runs, control, variant, LatencyValue)
	e.Tasks = solve.Tasks
	if solve.Tasks < MinTasks {
		e.State = StateNotEnoughData
		e.Reason = fmt.Sprintf("%d tasks have results on both setups; at least %d are needed", solve.Tasks, MinTasks)
		e.Headline = "Not enough data: " + e.Reason
		return e
	}
	e.State = StateMeasured
	e.SolveDelta = &solve.Delta
	if cost.Tasks > 0 {
		e.CostDelta = &cost.Delta
	}
	if lat.Tasks > 0 {
		e.LatencyDelta = &lat.Delta
	}
	switch {
	case solve.Delta.High != nil && *solve.Delta.High < 0:
		e.Result = "lost"
	case e.CostDelta != nil && e.CostDelta.Point < 0 && solve.Delta.Low != nil && *solve.Delta.Low >= -0.03:
		e.Result = "saved"
	default:
		e.Result = "worked"
	}
	pts := solve.Delta.Point * 100
	more := "more"
	if pts < 0 {
		more, pts = "fewer", -pts
	}
	e.Headline = fmt.Sprintf("Skill %s v%s solved %.0f points %s tasks than %s.", name, vv, pts, more, skillVersionText(cv))
	e.Setting = &BifrostSetting{
		Kind:  "none",
		Title: fmt.Sprintf("Make version %s the current version in Bifrost's Skills Repository", vv),
		Where: fmt.Sprintf("Bifrost dashboard, Skills, %s, Versions", name),
	}
	return e
}

// skillVersionText is "v3", or "no extra skill".
func skillVersionText(v string) string {
	if v == "none" || v == "" {
		return "no extra skill"
	}
	return "v" + v
}
