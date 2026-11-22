package evidencestore

// Incremental mirror passes (INFRA_I4 item 1). A pass used to verify every
// seal of the experiment and re-read every sealed run, after every seal
// burst: about N squared seal checks over an evaluation of N runs. When the
// experiment's listing says what is mirrored already, an incremental pass
// checks that the local seals.log still starts with exactly the mirrored
// seals (same count, same last hash), verifies only the runs sealed after
// that point, and checks that each new seal chains to the one before it and
// names the experiment's manifest. Runs the listing names are not read.
//
// Full verification is unchanged and still runs: every sweep (start-up,
// the storage.sweep_interval_m timer, `evidence sweep` and the nightly
// runbook), every Repair pass, Mirror.Verify, and the notified pass at
// experiment end (Service.NotifyFull).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/maximhq/bifrost/bench/schemas"
)

// incremental is the state of one incremental snapshot.
type incremental struct {
	base  int            // seals already mirrored: entries[:base]
	index map[string]int // run id -> its line in seals.log
	prev  []string       // prev[i]: the hash on the line before line i ("" for the first)
}

// incrementalFrom returns the incremental state when the listing base is
// a prefix of the local seals.log under the same manifest, else nil (a
// full snapshot).
func incrementalFrom(base *Listing, entries []sealEntry, manifestHash string) *incremental {
	if base == nil || base.Seals <= 0 || base.LastSeal == "" || base.ManifestHash != manifestHash {
		return nil
	}
	if len(entries) < base.Seals || entries[base.Seals-1].Hash != base.LastSeal {
		return nil
	}
	inc := &incremental{base: base.Seals, index: make(map[string]int, len(entries)), prev: make([]string, len(entries))}
	for i := range entries {
		if i > 0 {
			inc.prev[i] = entries[i-1].Hash
		}
	}
	return inc
}

// checkLog checks what VerifyChain checks of the log alone: every run is
// named once.
func (inc *incremental) checkLog(entries []sealEntry) error {
	for i, e := range entries {
		if j, dup := inc.index[e.RunID]; dup {
			return fmt.Errorf("seals.log names run %s twice (lines %d and %d)", e.RunID, j+1, i+1)
		}
		inc.index[e.RunID] = i
	}
	return nil
}

// listed reports whether a logged run was mirrored by an earlier pass.
func (inc *incremental) listed(runID string) bool {
	i, ok := inc.index[runID]
	return ok && i < inc.base
}

// verifyRun checks one run sealed after the mirrored point: the store's
// seal and file checks against the logged hash, then that its seal chains
// to the line before it and names the experiment's manifest.
func (inc *incremental) verifyRun(ctx context.Context, org Org, expID, dir, runID, hash, manifestHash string) error {
	if err := org.Store.VerifyLoggedRun(ctx, dir, runID, hash); err != nil {
		return err
	}
	b, err := os.ReadFile(filepath.Join(dir, sealFile))
	if err != nil {
		return err
	}
	var rec schemas.SealRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return fmt.Errorf("decode seal.json: %w", err)
	}
	if want := inc.prev[inc.index[runID]]; rec.PrevSeal != want {
		return fmt.Errorf("seal chains to %s, the line before it in seals.log is %s", short(rec.PrevSeal), short(want))
	}
	if rec.ManifestHash != manifestHash {
		return fmt.Errorf("sealed under manifest %s, manifest.json is %s", short(rec.ManifestHash), short(manifestHash))
	}
	return nil
}
