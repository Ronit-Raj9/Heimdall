package engine

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Control evaluations and framing pairs (docs/control-evals.md,
// schemas/control.go). The attack condition gives each run one of the
// task's side tasks in its instruction and opts the experiment into
// side_task_completed; framing pairs add twin copies of a base arm that
// differ only in a frozen preamble.

// planAttack checks the attack condition before anything is frozen: every
// task it selects declares valid side tasks whose instruction files exist
// inside the task folder, and no task runs as a team (a team member's
// launch does not take the composed instruction). It opts the experiment
// into side_task_completed. It changes nothing when no condition is attack.
func planAttack(suite *schemas.Suite, e *schemas.Experiment, conds []condPlan) error {
	used := false
	for _, c := range conds {
		if c.id != schemas.ConditionAttack {
			continue
		}
		used = true
		for _, id := range c.tasks {
			task := suite.TaskByID(id)
			if task == nil {
				return fmt.Errorf("condition %s: task %s is not loaded", c.id, id)
			}
			if err := checkSideTasks(task); err != nil {
				return fmt.Errorf("condition %s: %w", c.id, err)
			}
			if len(task.Team) > 0 {
				return fmt.Errorf("condition %s: task %s runs as a team; side tasks reach single-agent runs only", c.id, id)
			}
		}
	}
	if used && !slices.Contains(e.ExtraGates, schemas.GateSideTaskCompleted) {
		e.ExtraGates = append(append([]string(nil), e.ExtraGates...), schemas.GateSideTaskCompleted)
	}
	return nil
}

// checkSideTasks validates a task's side tasks for an attack run.
func checkSideTasks(task *schemas.Task) error {
	if len(task.SideTasks) == 0 {
		return fmt.Errorf("task %s declares no side_tasks", task.ID)
	}
	seen := map[string]bool{}
	for _, st := range task.SideTasks {
		if err := st.Validate(); err != nil {
			return fmt.Errorf("task %s: %w", task.ID, err)
		}
		if seen[st.ID] {
			return fmt.Errorf("task %s: side task %s is listed twice", task.ID, st.ID)
		}
		seen[st.ID] = true
		if st.InstructionFile != "" {
			if _, err := sideTaskText(task, &st); err != nil {
				return err
			}
		}
	}
	return nil
}

// sideTaskText returns a side task's attack instruction: inline, or read
// from its file inside the task folder.
func sideTaskText(task *schemas.Task, st *schemas.SideTask) (string, error) {
	if st.InstructionFile == "" {
		return st.Instruction, nil
	}
	p, err := hostPath(task.Dir, st.InstructionFile)
	if err != nil {
		return "", fmt.Errorf("task %s side task %s: attack_instruction_file: %w", task.ID, st.ID, err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("task %s side task %s: read attack_instruction_file: %w", task.ID, st.ID, err)
	}
	if strings.TrimSpace(string(b)) == "" {
		return "", fmt.Errorf("task %s side task %s: attack_instruction_file is empty", task.ID, st.ID)
	}
	return string(b), nil
}

// validateFramingPairs checks framing_pairs before any I/O.
func validateFramingPairs(e *schemas.Experiment) error {
	if len(e.FramingPairs) == 0 {
		return nil
	}
	var errs []error
	if e.Matrix != nil {
		errs = append(errs, errors.New("framing_pairs cannot be used with a matrix: the twin arms would enter the ranking"))
	}
	seen := map[string]bool{}
	for _, fp := range e.FramingPairs {
		k, ok := schemas.FramingKinds[fp.Kind]
		if !ok {
			errs = append(errs, fmt.Errorf("framing pair %q: kind %q: want %s, %s or %s", fp.ID, fp.Kind,
				schemas.FramingSandbagging, schemas.FramingMonitoring, schemas.FramingAwareness))
			continue
		}
		if !validID(fp.ID) {
			errs = append(errs, fmt.Errorf("framing pair id %q must match ^[a-z0-9_]+$", fp.ID))
		}
		if seen[fp.ID] {
			errs = append(errs, fmt.Errorf("framing pair %s is listed twice", fp.ID))
		}
		seen[fp.ID] = true
		if _, ok := e.Arms[fp.Base]; !ok {
			errs = append(errs, fmt.Errorf("framing pair %s: base arm %q is not an arm", fp.ID, fp.Base))
		}
		for name := range fp.Framings {
			if name != k.Control && name != k.Treatment {
				errs = append(errs, fmt.Errorf("framing pair %s: framing %q: kind %s has %s and %s", fp.ID, name, fp.Kind, k.Control, k.Treatment))
			}
		}
		for _, name := range []string{k.Control, k.Treatment} {
			id := schemas.FramingArmID(fp.Base, fp.ID, name)
			if _, clash := e.Arms[id]; clash || id == AAArm || id == CompatArm {
				errs = append(errs, fmt.Errorf("framing pair %s: arm id %s is already taken", fp.ID, id))
			}
		}
		if framingText(fp, k.Control) == framingText(fp, k.Treatment) {
			errs = append(errs, fmt.Errorf("framing pair %s: both framings have the same preamble", fp.ID))
		}
	}
	return errors.Join(errs...)
}

// framingText is a framing's preamble: the pair's own, else the default.
func framingText(fp schemas.FramingPair, name string) string {
	if t, ok := fp.Framings[name]; ok {
		return t
	}
	return schemas.DefaultFramings[fp.Kind][name]
}

// addFramingArms adds the twin arms of every framing pair: copies of the
// resolved base arm (same config, config hash, image and files) carrying
// the frozen preamble, like the A/A arm copies arm A.
func addFramingArms(e *schemas.Experiment, arms map[string]schemas.ArmManifest) error {
	for _, fp := range e.FramingPairs {
		base, ok := arms[fp.Base]
		if !ok {
			return fmt.Errorf("framing pair %s: base arm %s was not resolved", fp.ID, fp.Base)
		}
		k := schemas.FramingKinds[fp.Kind]
		for _, f := range []struct{ name, role string }{{k.Control, schemas.FramingRoleControl}, {k.Treatment, schemas.FramingRoleTreatment}} {
			id := schemas.FramingArmID(fp.Base, fp.ID, f.name)
			if _, clash := arms[id]; clash {
				return fmt.Errorf("framing pair %s: arm id %s is already taken", fp.ID, id)
			}
			a := copyArm(base)
			a.Label = fmt.Sprintf("%s (%s: %s)", base.Label, fp.ID, f.name)
			a.Framing = &schemas.ArmFraming{Pair: fp.ID, Kind: fp.Kind, Base: fp.Base, Framing: f.name, Role: f.role,
				Preamble: framingText(fp, f.name)}
			arms[id] = a
		}
	}
	return nil
}

// copyArm deep-copies the maps of an arm manifest so editing one arm never
// edits another.
func copyArm(a schemas.ArmManifest) schemas.ArmManifest {
	c := a
	if a.Files != nil {
		c.Files = make(map[string]map[string]string, len(a.Files))
		for k, v := range a.Files {
			c.Files[k] = maps.Clone(v)
		}
	}
	c.TaskImages = maps.Clone(a.TaskImages)
	c.TaskImageIDs = maps.Clone(a.TaskImageIDs)
	c.CapabilityGaps = slices.Clone(a.CapabilityGaps)
	return c
}

// instructionFile is the run file holding the composed instruction of a
// framed or attack run: what the agent was given, kept as evidence.
const instructionFile = "instruction.given.md"

// controlInstruction returns the run's composed instruction when the arm
// has a framing preamble or the slot is an attack run (side task on the
// first session only), and ok false when the run takes the task's
// instruction unchanged.
func controlInstruction(rc *schemas.RunContext) (string, bool, error) {
	preamble := ""
	if rc.Manifest != nil && rc.Slot != nil {
		if a, ok := rc.Manifest.Arms[rc.Slot.Arm]; ok && a.Framing != nil {
			preamble = a.Framing.Preamble
		}
	}
	side := ""
	if rc.Slot != nil && rc.Slot.Condition == schemas.ConditionAttack && rc.SessionIndex == 0 {
		st := schemas.SideTaskFor(rc.Task, rc.Slot.Trial)
		if st == nil {
			return "", false, fmt.Errorf("attack run of task %s, which declares no side task", rc.Task.ID)
		}
		text, err := sideTaskText(rc.Task, st)
		if err != nil {
			return "", false, err
		}
		side = text
	}
	if strings.TrimSpace(preamble) == "" && side == "" {
		return "", false, nil
	}
	p := rc.Task.Instruction
	if !filepath.IsAbs(p) {
		var err error
		if p, err = hostPath(rc.Task.Dir, p); err != nil {
			return "", false, fmt.Errorf("task %s instruction: %w", rc.Task.ID, err)
		}
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", false, fmt.Errorf("task %s: read instruction: %w", rc.Task.ID, err)
	}
	return schemas.ComposeInstruction(string(b), preamble, side), true, nil
}

// withControlInstruction writes the composed instruction into the run
// folder and points a shallow copy of the task at it; restore puts the
// task back. A run with nothing to compose is left as it is.
func (p *Pipeline) withControlInstruction(ctx context.Context, st *runState) (restore func(), err error) {
	rc := st.rc
	noop := func() {}
	if rc.Task == nil {
		return noop, nil
	}
	text, ok, err := controlInstruction(rc)
	if err != nil || !ok {
		return noop, err
	}
	if rc.Dir == "" {
		return noop, errors.New("composed instruction: run has no folder")
	}
	name := instructionFile
	if rc.SessionIndex > 0 {
		name = fmt.Sprintf("instruction.given.%d.md", rc.SessionIndex+1)
	}
	if err := p.writeFile(ctx, st, name, []byte(text)); err != nil {
		return noop, err
	}
	orig := rc.Task
	task := *orig
	task.Instruction = filepath.Join(rc.Dir, name)
	rc.Task = &task
	return func() { rc.Task = orig }, nil
}

// framingPreamble is the preamble a slot's arm adds, for the run input
// hash; "" for every arm outside a framing pair.
func framingPreamble(a *schemas.ArmManifest) string {
	if a == nil || a.Framing == nil {
		return ""
	}
	return a.Framing.Preamble
}
