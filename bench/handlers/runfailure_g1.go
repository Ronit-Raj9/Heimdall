package handlers

import (
	"github.com/valyala/fasthttp"
	"slices"

	"github.com/maximhq/bifrost/bench/clusters"
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
	for _, c := range rep.Clusters {
		d, err := clusters.Find(rep, c.Signature)
		if err != nil {
			continue
		}
		i := slices.IndexFunc(d.Runs, func(r clusters.RunRef) bool { return r.RunID == rv.Run.RunID })
		if i < 0 {
			continue
		}
		return &schemas.RunFailureGroup{Signature: c.Signature, Title: c.Title, Text: d.Runs[i].ErrorText, Count: c.Count, ExpID: expID}
	}
	return nil
}
