package schedule

import (
	"context"
	"fmt"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Settle records the outcome of a scheduled evaluation once it finished
// (lane Y05, audit G4-08). A trigger of an evaluation schedule only starts
// the evaluation, so its own record says "started" and no verdict alert
// can fire then; when the evaluation ends the server calls Settle with
// its decision (regressed against the previous run of the schedule) and
// release policy (block when a release would need an override). Settle
// appends a second record for the trigger and sends the schedule's
// alerts for that outcome: verdict_regressed, policy_block.
func (d *Daemon) Settle(ctx context.Context, name string, started time.Time, res *Result) (Record, error) {
	s, ok := d.file.Find(name)
	if !ok {
		return Record{}, fmt.Errorf("schedule %q: %w", name, schemas.ErrNotFound)
	}
	if res == nil {
		return Record{}, fmt.Errorf("schedule %s: no outcome to record", name)
	}
	rec := Record{At: started, Started: started, Schedule: s.Name, Status: StatusOK, Result: res}
	if loc, err := s.Location(); err == nil {
		rec.SpentTodayUSD = d.spent(s.Name, d.o.Clock.Now().In(loc).Format(time.DateOnly))
	}
	if failedCheck(s, res) {
		rec.Status = StatusFailed
	}
	return d.finish(ctx, s, rec, EventsFor(res, nil)), nil
}
