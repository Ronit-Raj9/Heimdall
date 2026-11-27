package handlers

import (
	"github.com/valyala/fasthttp"

	"github.com/maximhq/bifrost/bench/clusters"
	"github.com/maximhq/bifrost/bench/plaintext"
	"github.com/maximhq/bifrost/bench/schemas"
)

// runFailureGroup finds the failure group of one run inside its own
// evaluation, by the same clustering the failure groups page uses, so the
// signature links to that group. It is asked for only with ?failure=1: it
// groups every failed run of the evaluation. nil when the index cannot group
// runs, the run has no evaluation, or the run did not enter a group (a run
// with an evidence problem, or a synthetic one).
func runFailureGroup(ctx *fasthttp.RequestCtx, index ReadIndex, rv *RunView) *schemas.RunFailureGroup {
	src, ok := index.(clusters.Source)
	expID := rv.Slot.ExpID
	if expID == "" {
		expID = rv.Run.ExpID
	}
	if !ok || expID == "" {
		return nil
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	rep, err := clusters.Build(work, src, clusters.Query{Experiments: []string{expID}})
	if err != nil {
		return nil
	}
	f, found := clusters.FindRun(rep, rv.Run.RunID)
	if !found {
		return nil
	}
	out := &schemas.RunFailureGroup{Signature: f.Signature, Text: f.Text, Count: f.Count, ExpID: expID}
	for _, c := range rep.Clusters {
		if c.Signature == f.Signature {
			out.Title = c.Title
		}
	}
	// text and title are not plainServed field names, and both come from a
	// worker's error output: clean them here, like the run's part failures.
	out.Text, out.Title = plaintext.Hosted(out.Text), plaintext.Hosted(out.Title)
	return out
}
