package handlers

import (
	"errors"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestFailedPlanListsPlainSentences(t *testing.T) {
	err := errors.Join(ownImageErr("C", "bfcl-1"), ownImageErr("C", "bfcl-2"), ownImageErr("C", "bfcl-3"))
	p := FailedPlan(&schemas.Experiment{}, err)
	if len(p.Errors) != 1 {
		t.Fatalf("errors %q", p.Errors)
	}
	if strings.Contains(p.Errors[0], "build.base_arg") || !strings.Contains(p.Errors[0], "3 tasks of hd-harbor-bfcl") {
		t.Fatalf("error %q", p.Errors[0])
	}
	two := FailedPlan(&schemas.Experiment{}, errors.Join(ownImageErr("C", "bfcl-1"),
		errors.New("arm B: agent config agents/bare-model.yaml: effective agent config: schema: notes.project_file: required field missing")))
	if len(two.Errors) != 2 {
		t.Fatalf("two problems give %q", two.Errors)
	}
}
