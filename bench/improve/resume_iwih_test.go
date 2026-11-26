package improve_test

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/improve"
	"github.com/maximhq/bifrost/bench/improve/improvetest"
)

// Security review (A1.7): a run interrupted by a restart after its final
// experiment resumes without paying again for any stage that finished: no
// second baseline, practice, gate or final scoring, no improver call.
func TestResumeAfterRestartRepeatsNoPaidStage(t *testing.T) {
	x := &improvetest.Exec{Behavior: improvetest.Honest, Base: 0, NTasks: 120, TimeoutTries: 1,
		Hold: improve.PhaseRetries, Held: make(chan struct{}), Release: make(chan struct{})}
	r := newRunner(t, x)
	rec, err := r.Create(context.Background(), w4Spec(nil), "ana")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = r.Run(context.Background(), rec.ID)
	}()
	<-x.Held // the final experiment is saved; the retries check is running
	if n, err := improve.MarkInterrupted(r.Root); err != nil || n != 1 {
		t.Fatalf("marked %d: %v", n, err)
	}
	y := &improvetest.Exec{Behavior: improvetest.Honest, Base: 0, NTasks: 120, TimeoutTries: 1}
	r2 := &improve.Runner{Root: r.Root, Exec: y}
	if _, err := r2.CanResume(rec.ID); err != nil {
		t.Fatalf("not resumable: %v", err)
	}
	got, err := r2.Run(context.Background(), rec.ID)
	close(x.Release)
	<-done
	if err != nil || got.Status != improve.StatusDone {
		t.Fatalf("resume: %v %s", err, got.Status)
	}
	for _, p := range []string{improve.PhaseBaseline, improve.PhaseScreen, improve.PhaseAudit, improve.PhaseSealed, improve.PhaseFinal, "improve"} {
		if n := y.Count(p); n != 0 {
			t.Errorf("the resumed run paid again for %s: %d calls", p, n)
		}
	}
	if y.Count(improve.PhaseRetries) == 0 {
		t.Error("the unfinished retries check did not run on resume")
	}
}
