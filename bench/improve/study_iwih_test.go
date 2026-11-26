package improve_test

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/improve"
	"github.com/maximhq/bifrost/bench/improve/improvetest"
)

// Item 6 of the group H merge (I42 and I40): the loops of a replication
// study skip their own final experiment and controls; the study scores the
// held-out tasks once for all loops. A lone run of the same spec does run
// them, so the counts below are not zero by accident.
func TestStudyLoopsSkipFinalAndControls(t *testing.T) {
	spec := func() *improve.Spec {
		return w4Spec(func(s *improve.Spec) { s.Rounds = 2; s.Replicate = improve.ReplicationPlan{Loops: 2} })
	}
	lone := &improvetest.Exec{Behavior: improvetest.Honest, Base: 0, NTasks: 120}
	s := spec()
	s.Replicate = improve.ReplicationPlan{Loops: 1}
	rec := createRun(t, newRunner(t, lone), s)
	if rec.Final == nil || lone.Count(improve.PhaseFinal) != 1 || rec.Controls == nil || rec.Controls.Null == nil {
		t.Fatalf("lone run: final %v, final calls %d, controls %+v", rec.Final, lone.Count(improve.PhaseFinal), rec.Controls)
	}
	loneImprove := lone.Count("improve")

	x := &improvetest.Exec{Behavior: improvetest.Honest, Base: 0, NTasks: 120}
	r := newRunner(t, x)
	st, err := r.CreateStudy(context.Background(), spec(), "ana")
	if err != nil {
		t.Fatal(err)
	}
	if st, err = r.RunStudy(context.Background(), st.ID); err != nil {
		t.Fatal(err)
	}
	if st.Status != improve.StatusDone || len(st.Loops) != 2 {
		t.Fatalf("study %s (%s), loops %d", st.Status, st.Reason, len(st.Loops))
	}
	for _, p := range []string{improve.PhaseFinal, improve.PhaseRetries, improve.PhaseExtension} {
		if n := x.Count(p); n != 0 {
			t.Errorf("study loops ran %d %s scorings", n, p)
		}
	}
	if n := x.Count(improve.PhaseStudyFinal); n != 1 {
		t.Errorf("study held-out experiments %d, want 1", n)
	}
	// The null improver adds improver calls; two loops without it make at
	// most the lone run's own improver calls each, minus its null rounds.
	if n := x.Count("improve"); n >= 2*loneImprove {
		t.Errorf("improver calls %d, lone run %d: the loops ran the null improver", n, loneImprove)
	}
}
