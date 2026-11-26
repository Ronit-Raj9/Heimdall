package projectfs

import (
	"fmt"
	"testing"
)

func TestInvalidReasonPlain(t *testing.T) {
	yamlErr := fmt.Errorf("%w: %v", ErrInvalid, "benchmarks/faq/tasks/t1/task.yaml: yaml: line 3: did not find expected ',' or ']'")
	if got, want := InvalidReason(yamlErr), "line 3: did not find expected ',' or ']'"; got != want {
		t.Fatalf("yaml: got %q, want %q", got, want)
	}
	if got := InvalidReason(fmt.Errorf("write: %w", ErrOutside)); got != "the file name is not allowed" {
		t.Fatalf("outside: got %q", got)
	}
	other := fmt.Errorf("%w: %v", ErrInvalid, "the task id must match its folder")
	if got := InvalidReason(other); got != "the task id must match its folder" {
		t.Fatalf("validator: got %q", got)
	}
}
