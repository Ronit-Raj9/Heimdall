package scaffold

import (
	"errors"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ChecksGrader is the grader that reads each task's checks.yaml; a task
// without one is still graded by the suite's verifier command.
const ChecksGrader = "checks"

// SetChecksGrader moves a suite to grader checks, keeping its verifier
// command for the tasks that have no checks.yaml. With judge set it also
// declares grading: judged and pins the judge a reply_rubric needs.
func SetChecksGrader(src []byte, judge *JudgeConfig) ([]byte, error) {
	if judge != nil {
		if strings.TrimSpace(judge.Provider) == "" || strings.TrimSpace(judge.Model) == "" {
			return nil, errors.New("the reply judge needs a provider and a model to pin")
		}
		if judge.Votes < 0 || (judge.Votes > 0 && judge.Votes%2 == 0) || judge.Votes > 9 {
			return nil, errors.New("judge votes: an odd number from 1 to 9")
		}
	}
	return editMapping(src, func(m *yaml.Node) error {
		setScalar(m, "grader", ChecksGrader)
		if judge == nil {
			return nil
		}
		setScalar(m, "grading", "judged")
		j := mapValue(m, "judge")
		if j == nil || j.Kind != yaml.MappingNode {
			j = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		}
		setScalar(j, "provider", strings.TrimSpace(judge.Provider))
		setScalar(j, "model", strings.TrimSpace(judge.Model))
		if mapValue(j, "prompt_version") == nil {
			setScalar(j, "prompt_version", DefaultJudgePrompt)
		}
		if judge.Votes > 0 {
			setNode(j, "votes", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(judge.Votes)})
		}
		setNode(m, "judge", j)
		return nil
	})
}

// ReadTaskFolder reads every regular file of a task folder (a symlink is
// refused), for copying a task's shape.
func ReadTaskFolder(dir string) ([]File, error) { return readTaskFolder(dir) }
