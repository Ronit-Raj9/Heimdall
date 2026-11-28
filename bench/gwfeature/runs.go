package gwfeature

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/metrics"
	"github.com/maximhq/bifrost/bench/schemas"
)

// version is part of every fingerprint: a change to how effects are computed
// bumps it so saved effects are recomputed.
const version = "1.1.0"

// LoadRuns returns the counted runs of an experiment: the newest attempt per
// slot that is sealed, not superseded and not aborted by infrastructure, of
// the clean condition only unless allConditions. Sorted by run id. An
// unreadable run file leaves its field nil and never fails the load.
func LoadRuns(src leaderboard.Source, expID string, allConditions bool) ([]Run, error) {
	bySlot := map[string][]leaderboard.Run{}
	for _, r := range src.Runs(expID) {
		if !r.Sealed || r.Superseded || r.Run.Outcome == schemas.OutcomeAbortedInfra {
			continue
		}
		if !allConditions && r.Slot.Condition != schemas.ConditionClean {
			continue
		}
		bySlot[r.Slot.SlotID] = append(bySlot[r.Slot.SlotID], r)
	}
	out := make([]Run, 0, len(bySlot))
	for _, attempts := range bySlot {
		lr := slices.MaxFunc(attempts, func(x, y leaderboard.Run) int { return cmp.Compare(x.Run.Attempt, y.Run.Attempt) })
		r := Run{RunID: lr.Run.RunID, Task: lr.Slot.Task, Arm: lr.Slot.Arm, Condition: lr.Slot.Condition, Trial: lr.Slot.Trial}
		if data, err := src.RunFile(r.RunID, "metrics.json"); err == nil {
			var m map[string]schemas.Value
			if json.Unmarshal(data, &m) == nil {
				if v, ok := m[metrics.VerifiedSuccess]; ok && v.V != nil {
					b := *v.V >= 1
					r.Success = &b
				}
				if v, ok := m[metrics.CostUSD]; ok && v.V != nil {
					c := *v.V
					r.CostUSD = &c
				}
			}
		}
		if data, err := src.RunFile(r.RunID, "gates.json"); err == nil {
			var g []schemas.GateResult
			if json.Unmarshal(data, &g) == nil {
				r.Gates = g
			}
		}
		if data, err := src.RunFile(r.RunID, fileEvidence); err == nil {
			var ev schemas.Evidence
			if json.Unmarshal(data, &ev) == nil {
				r.Evidence = &ev
			}
		}
		if rows, complete, _, err := ReadRows(src, r.RunID); err == nil {
			r.Rows, r.RowsComplete = rows, complete
		}
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b Run) int { return cmp.Compare(a.RunID, b.RunID) })
	return out, nil
}

// Fingerprint identifies the run set an Effect was computed from.
func Fingerprint(runs []Run, feature string) string {
	ids := make([]string, 0, len(runs))
	for _, r := range runs {
		ids = append(ids, r.RunID)
	}
	slices.Sort(ids)
	sum := sha256.Sum256([]byte(feature + "\n" + strings.Join(ids, "\n") + version))
	return hex.EncodeToString(sum[:])
}
