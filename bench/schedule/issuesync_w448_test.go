package schedule

import (
	"slices"
	"testing"
)

func TestIssueSyncJobKnown(t *testing.T) {
	if !slices.Contains(Jobs, JobIssueSync) {
		t.Fatalf("Jobs %v lacks %s", Jobs, JobIssueSync)
	}
}
