package scaffold

// Small edits of suite.yaml and task.yaml for the website (lane X13): mark a
// task held out or public, choose a question-and-answer suite's grader and
// pin its judge, and start a suite with no sample task. Each edit keeps the
// other keys and their comments.

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// JudgeConfig is the judge a judged suite pins (schemas.JudgeSpec's fields
// the website sets).
type JudgeConfig struct {
	Provider      string `json:"provider" yaml:"provider"`
	Model         string `json:"model" yaml:"model"`
	PromptVersion string `json:"prompt_version,omitempty" yaml:"prompt_version"`
	Votes         int    `json:"votes,omitempty" yaml:"votes"`
}

// DefaultJudgePrompt is the shipped prompt of a question-and-answer judge.
const DefaultJudgePrompt = "qa-v1"

func editMapping(src []byte, edit func(m *yaml.Node) error) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("not a YAML mapping")
	}
	if err := edit(doc.Content[0]); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// deleteKey removes key from mapping m.
func deleteKey(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

// SetTaskSplit marks a task.yaml held out (split: held_out) or public (no
// split key, the default).
func SetTaskSplit(src []byte, heldOut bool) ([]byte, error) {
	return editMapping(src, func(m *yaml.Node) error {
		if heldOut {
			setScalar(m, "split", "held_out")
		} else {
			deleteKey(m, "split")
		}
		return nil
	})
}

// QAExactDescription is the qa template's description, true while the suite
// is graded on the exact answer; QAJudgeDescription replaces it when the
// suite switches to the rubric judge (and back again).
const (
	QAExactDescription = "Question-answering tasks matched against a hidden expected answer, no model in the grade."
	QAJudgeDescription = "Question-answering tasks graded by a pinned judge model against a rubric."
)

// SetSuiteGrader sets a question-and-answer suite's grader: answer_match
// (grading verified, no judge) or judge (grading judged, the judge pinned).
// A judge's other keys (max_tokens, auditor) are kept when it stays judged.
func SetSuiteGrader(src []byte, grader string, judge *JudgeConfig) ([]byte, error) {
	switch grader {
	case RowGraderExact:
	case RowGraderJudge:
		if judge == nil || strings.TrimSpace(judge.Provider) == "" || strings.TrimSpace(judge.Model) == "" {
			return nil, errors.New("the rubric judge needs a provider and a model to pin")
		}
		if judge.Votes < 0 || (judge.Votes > 0 && judge.Votes%2 == 0) || judge.Votes > 9 {
			return nil, errors.New("judge votes: an odd number from 1 to 9")
		}
	default:
		return nil, fmt.Errorf("grader %q: want answer_match or judge", grader)
	}
	return editMapping(src, func(m *yaml.Node) error {
		setScalar(m, "grader", grader)
		if d := mapValue(m, "description"); d != nil && d.Kind == yaml.ScalarNode {
			if grader == RowGraderJudge && d.Value == QAExactDescription {
				d.Value = QAJudgeDescription
			} else if grader == RowGraderExact && d.Value == QAJudgeDescription {
				d.Value = QAExactDescription
			}
		}
		if grader == RowGraderExact {
			deleteKey(m, "grading")
			deleteKey(m, "judge")
			return nil
		}
		setScalar(m, "grading", "judged")
		j := mapValue(m, "judge")
		if j == nil || j.Kind != yaml.MappingNode {
			j = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		}
		setScalar(j, "provider", strings.TrimSpace(judge.Provider))
		setScalar(j, "model", strings.TrimSpace(judge.Model))
		pv := strings.TrimSpace(judge.PromptVersion)
		if pv == "" {
			pv = DefaultJudgePrompt
		}
		setScalar(j, "prompt_version", pv)
		if judge.Votes > 0 {
			setNode(j, "votes", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(judge.Votes)})
		} else {
			deleteKey(j, "votes")
		}
		setNode(m, "judge", j)
		return nil
	})
}

// BlankSuite drops a scenario template's sample task from a new suite's
// files: no tasks/ folder, and suite.yaml lists no tasks.
func BlankSuite(files []File) ([]File, error) {
	var out []File
	for _, f := range files {
		if strings.HasPrefix(f.Path, "tasks/") {
			continue
		}
		if f.Path == "suite.yaml" {
			data, err := editMapping(f.Data, func(m *yaml.Node) error {
				setNode(m, "tasks", &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle})
				return nil
			})
			if err != nil {
				return nil, fmt.Errorf("suite.yaml: %w", err)
			}
			f.Data = data
		}
		out = append(out, f)
	}
	return out, nil
}
