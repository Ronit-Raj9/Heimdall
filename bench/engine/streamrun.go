package engine

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Stream mode at run time (streamplan.go plans the chains). A stream run
// records its place in stream.json. With memory carry, prepare seeds the
// fresh memory volumes from the archive the chain's previous run left, and
// export archives this run's memory volumes into memory.tar in the run
// folder, which is sealed with the run.

// streamRun reports whether the run is a slot of a stream chain.
func streamRun(rc *schemas.RunContext) bool {
	return rc != nil && rc.Slot != nil && rc.Slot.StreamIndex > 0 && rc.Experiment != nil && rc.Experiment.Stream != nil
}

// streamCarries reports whether the run hands its memory on and takes it
// from the previous run.
func streamCarries(rc *schemas.RunContext) bool {
	return streamRun(rc) && rc.Experiment.Stream.Carries(rc.Slot.Arm)
}

// seedStream fills a carrying run's fresh memory volumes from the archive of
// the chain's previous run: the latest sealed attempt of the previous slot
// that wrote one. A missing archive (the previous run aborted before
// export, say) starts the run empty and says why in stream.json; it never
// fails the run. stream.json is written for every stream run.
func (p *Pipeline) seedStream(ctx context.Context, st *runState) error {
	rc := st.rc
	if !streamRun(rc) {
		return nil
	}
	rec := schemas.StreamRecord{Index: rc.Slot.StreamIndex, Replay: rc.Slot.StreamReplay, Carry: streamCarries(rc), PrevSlot: rc.Slot.StreamPrev}
	st.stream = &rec
	if rec.Carry && rec.PrevSlot != "" {
		src, runID, note := p.previousArchive(ctx, rc)
		switch {
		case src == "":
			rec.SeedNote = note
		default:
			mc, ok := st.env.(schemas.MemoryCarrier)
			if !ok {
				return wrapPart("environment", st.env, fmt.Errorf("stream memory carry: environment %s cannot carry memory", st.env.GetName()))
			}
			sum, err := utils.SHA256File(src)
			if err != nil {
				return fmt.Errorf("stream memory carry: %w", err)
			}
			if err := mc.SeedMemory(ctx, rc, src); err != nil {
				return wrapPart("environment", st.env, fmt.Errorf("stream memory carry: seed from run %s: %w", runID, err))
			}
			rec.SeededFrom, rec.SeedSHA256 = runID, sum
		}
	}
	return p.writeJSON(ctx, st, schemas.StreamFile, rec)
}

// previousArchive finds the memory archive of the chain's previous slot:
// its latest sealed attempt whose folder holds one. It returns the path and
// run id, or "" and why not.
func (p *Pipeline) previousArchive(ctx context.Context, rc *schemas.RunContext) (string, string, string) {
	recs, err := slotRuns(ctx, p.reg.Store, rc.Slot.ExpID, rc.Slot.StreamPrev)
	if err != nil {
		return "", "", "previous runs unreadable: " + err.Error()
	}
	slices.SortStableFunc(recs, func(a, b schemas.RunRecord) int { return cmp.Compare(b.Run.Attempt, a.Run.Attempt) })
	for _, r := range recs {
		if !r.Sealed || r.Dir == "" {
			continue
		}
		path := filepath.Join(r.Dir, schemas.StreamMemoryArchive)
		if st, err := os.Stat(path); err == nil && st.Mode().IsRegular() {
			return path, r.Run.RunID, ""
		}
	}
	if len(recs) == 0 {
		return "", "", "the previous run of the chain never ran; this run starts with empty memory"
	}
	return "", "", "the previous run of the chain left no memory archive; this run starts with empty memory"
}

// archiveStream writes a carrying run's memory volumes to memory.tar in the
// run folder after the agent exited, and records it in stream.json. A
// failed archive is logged and recorded, never fatal: the next run of the
// chain then starts empty and says so.
func (p *Pipeline) archiveStream(ctx context.Context, st *runState) error {
	rc := st.rc
	if st.stream == nil || !streamCarries(rc) {
		return nil
	}
	mc, ok := st.env.(schemas.MemoryCarrier)
	if !ok || rc.Dir == "" {
		return nil
	}
	dst := filepath.Join(rc.Dir, schemas.StreamMemoryArchive)
	if err := mc.ArchiveMemory(ctx, rc, dst); err != nil {
		p.logger.Warn("stream memory not archived; the next run of the chain starts empty", "run", rc.Run.RunID, "error", err.Error())
		os.Remove(dst)
		return p.writeJSON(ctx, st, schemas.StreamFile, st.stream)
	}
	sum, err := utils.SHA256File(dst)
	if err == nil {
		st.stream.Archived, st.stream.ArchiveSHA = true, sum
	}
	return p.writeJSON(ctx, st, schemas.StreamFile, st.stream)
}

// budgetEnv is what budget disclosure adds to the agent's environment: the
// per-run cap, the deadline (now plus the plan's wall budget) and the tool
// call cap, each only when known. Empty without budget.disclose.
func budgetEnv(rc *schemas.RunContext, plan *schemas.RunPlan, now time.Time) map[string]string {
	if rc == nil || rc.Experiment == nil || !rc.Experiment.Budget.Disclose {
		return nil
	}
	env := map[string]string{}
	usd := 0.0
	if rc.Agent != nil {
		usd = rc.Agent.Budgets.USD
	}
	if usd <= 0 && rc.Manifest != nil && len(rc.Manifest.Slots) > 0 && rc.Experiment.Budget.TotalUSD > 0 {
		usd = rc.Experiment.Budget.TotalUSD / float64(len(rc.Manifest.Slots))
	}
	if usd > 0 {
		env[schemas.EnvBudgetUSD] = strconv.FormatFloat(usd, 'f', 4, 64)
	}
	wall := 0
	if plan != nil {
		wall = plan.WallS
	}
	if wall <= 0 && rc.Agent != nil {
		wall = rc.Agent.Budgets.WallS
	}
	if wall > 0 {
		env[schemas.EnvDeadline] = now.UTC().Add(time.Duration(wall) * time.Second).Format(time.RFC3339)
	}
	if rc.Agent != nil && rc.Agent.Budgets.MaxToolCalls > 0 {
		env[schemas.EnvMaxToolCalls] = strconv.Itoa(rc.Agent.Budgets.MaxToolCalls)
	}
	return env
}
