package utils

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/maximhq/bifrost/bench/schemas"
	"gopkg.in/yaml.v3"
)

// answerSpecFile is the hidden answer file looked for in the verifier tests
// folder when verifier.answer is not set.
const answerSpecFile = "answer.yaml"

// TaskAnswerSpec reads the hidden AnswerSpec of an answer task
// (verifier.answer, else answer.yaml in the verifier tests folder), never
// outside the task folder; unknown keys are an error. A task without an
// answer file gives nil and no error.
func TaskAnswerSpec(t *schemas.Task) (*schemas.AnswerSpec, error) {
	if t == nil || t.Dir == "" {
		return nil, errors.New("task is not loaded")
	}
	rp := t.Verifier.Answer
	if rp == "" {
		rp = path.Join(filepath.ToSlash(t.Verifier.Tests), answerSpecFile)
	}
	b, err := ReadFileInside(t.Dir, rp)
	if errors.Is(err, os.ErrNotExist) && t.Verifier.Answer == "" {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("task %s: answer spec: %w", t.ID, err)
	}
	var spec schemas.AnswerSpec
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&spec); err != nil {
		return nil, fmt.Errorf("task %s: answer spec %s: %w", t.ID, rp, err)
	}
	return &spec, nil
}

// TaskWrongAnswers is every wrong answer admission grades for an answer
// task: the author's wrong_answers, then the generated ones. A task without
// an answer file has none. Local admission (suite) and the hosted
// wrong_answer control (adapters) read the same list, so both grade the
// same answers.
func TaskWrongAnswers(t *schemas.Task) ([]string, error) {
	spec, err := TaskAnswerSpec(t)
	if err != nil || spec == nil {
		return nil, err
	}
	return spec.AllWrong(), nil
}
