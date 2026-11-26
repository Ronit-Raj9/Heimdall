package improve

import (
	"fmt"
	"strings"
	"testing"
)

// TestStudyPooledVerdict: three loops, the second without a commit. The
// pooled difference per task is the mean over loops, the loop without a
// commit counting as version 0; two of three loops ended better.
func TestStudyPooledVerdict(t *testing.T) {
	var tasks []string
	var runs []RunScore
	for i := range 30 {
		task := fmt.Sprintf("held-%02d", i)
		tasks = append(tasks, task)
		runs = append(runs, RunScore{Arm: 0, Task: task, Known: true, Success: 0})
		// Loop 1 fixes every task, loop 2's arm would fix them too but it
		// committed nothing, loop 3 fixes two tasks in three.
		runs = append(runs, RunScore{Arm: 1, Task: task, Known: true, Success: 1})
		runs = append(runs, RunScore{Arm: 2, Task: task, Known: true, Success: 1})
		s := 0.0
		if i%3 != 0 {
			s = 1
		}
		runs = append(runs, RunScore{Arm: 3, Task: task, Known: true, Success: s})
	}
	v := StudyPooled(runs, []bool{true, false, true}, tasks)
	if v.N != 30 || v.Loops != 3 || v.Better != 2 {
		t.Fatalf("verdict %+v", v)
	}
	want := (1.0 + 0 + 2.0/3) / 3
	if d := v.Gain - want; d > 1e-9 || d < -1e-9 {
		t.Errorf("gain %v, want %v", v.Gain, want)
	}
	if v.LoopGains[1] != 0 || v.State != VerdictSupported || v.PUpper > StudyAlpha || v.LoopCI80 == nil {
		t.Errorf("verdict %+v", v)
	}
	if !strings.HasPrefix(v.Headline, "Across 3 runs of the loop from version 0: +56 points on 30 held-out tasks") ||
		!strings.HasSuffix(v.Headline, "2 of 3 runs ended better than version 0.") {
		t.Errorf("headline %q", v.Headline)
	}
	none := StudyPooled(runs, []bool{false, false}, tasks)
	if none.State != VerdictNoChange || none.Gain != 0 || none.LoopCI80 != nil {
		t.Errorf("no commits: %+v", none)
	}
}

// TestStudyViewHidesSealed: a served study names no held-out task.
func TestStudyViewHidesSealed(t *testing.T) {
	r := &Runner{Root: t.TempDir()}
	v := r.ViewStudy(&Study{ID: "ims_x-1", Sealed: []string{"held-secret-01"}})
	if fmt.Sprintf("%+v", v) == "" || v.HeldOut != 1 || strings.Contains(fmt.Sprintf("%+v", v), "held-secret-01") {
		t.Errorf("view %+v", v)
	}
}
