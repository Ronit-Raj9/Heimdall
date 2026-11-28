package adapters

import (
	"errors"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// controlPlan is the argv of an empty, mutant or wrong_answer control, the
// hosted twin of local admission's file and answer controls
// (suite/admitscenario.go): it follows the task's control kind as the
// oracle does. A patch is applied with git apply (an empty patch changes
// nothing), a script runs in the agent's place, an answer is written to the
// answer path. A mutant or wrong answer the task does not have prints
// ScriptedNoControl and changes nothing.
func controlPlan(rc *schemas.RunContext, kind string, index int, files map[string]string) ([]string, error) {
	text, ok, err := controlText(rc.Task, kind, index)
	if err != nil {
		return nil, err
	}
	if !ok {
		return []string{"echo", ScriptedNoControl}, nil
	}
	ck := utils.ControlKind(rc.Suite, rc.Task)
	if kind == ScriptedWrongAnswer {
		ck = utils.ControlAnswer // a wrong answer is always an answer
	}
	switch ck {
	case utils.ControlAnswer:
		files[controlFile] = text
		return []string{"sh", "-c", writeAnswer, "write-answer", utils.AnswerContainerPath(rc.Suite, rc.Task), controlFile}, nil
	case utils.ControlScript:
		files[controlFile] = text
		return []string{"sh", controlFile}, nil
	default:
		if strings.TrimSpace(text) == "" {
			return []string{"true"}, nil // the empty patch: nothing to apply
		}
		files[controlFile] = text
		return []string{"git", "-c", "safe.directory=*", "apply", "--whitespace=nowarn", controlFile}, nil
	}
}

// controlText is the content of the control kind/index of task t; ok is
// false when the task has no such control.
func controlText(t *schemas.Task, kind string, index int) (string, bool, error) {
	switch kind {
	case ScriptedEmpty:
		if t.Controls.Empty == "" {
			return "", false, nil
		}
		s, err := readTaskFile(t, t.Controls.Empty)
		return s, err == nil, err
	case ScriptedMutant:
		if index < 1 {
			return "", false, fmt.Errorf("scripted.index must be 1 or more")
		}
		if index > len(t.Controls.Mutants) {
			return "", false, nil
		}
		s, err := readTaskFile(t, t.Controls.Mutants[index-1])
		return s, err == nil, err
	case ScriptedVariant:
		if index < 1 {
			return "", false, fmt.Errorf("scripted.index must be 1 or more")
		}
		vs := utils.TaskVariantChecks(t)
		if index > len(vs) {
			return "", false, nil
		}
		if v := vs[index-1]; v.Err != "" {
			return "", false, errors.New(v.Err)
		}
		s, err := readTaskFile(t, vs[index-1].File)
		return s, err == nil, err
	case ScriptedWrongAnswer:
		if index < 1 {
			return "", false, fmt.Errorf("scripted.index must be 1 or more")
		}
		wrong, err := utils.TaskWrongAnswers(t)
		if err != nil {
			return "", false, err
		}
		if index > len(wrong) {
			return "", false, nil
		}
		return wrong[index-1], true, nil
	}
	return "", false, fmt.Errorf("not a control kind: %q", kind)
}
