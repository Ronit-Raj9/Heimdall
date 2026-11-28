package schedule

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// PauseAfterFailures is how many failed firings in a row pause a schedule
// by itself, so a schedule that can never start stops trying every day.
const PauseAfterFailures = 3

// FailedFiring reports whether a trigger record is a firing that did not
// start or ended in an error (a refusal, an error); a skipped trigger (an
// overlap) and a finished check, even a failed one, are not.
func FailedFiring(r Record) bool {
	return r.Status == StatusError || r.Status == StatusRefused
}

// FailureStreaks returns, per schedule, how many of its newest records in a
// row are failed firings, and the reason of the newest one.
func FailureStreaks(stateDir string) (map[string]int, map[string]string, error) {
	f, err := os.Open(filepath.Join(stateDir, RecordsFile))
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]int{}, map[string]string{}, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read schedule records: %w", err)
	}
	defer f.Close()
	n, why := map[string]int{}, map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var r Record
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		switch {
		case FailedFiring(r):
			n[r.Schedule]++
			why[r.Schedule] = r.Reason
		case r.Status == StatusSkipped:
		default:
			n[r.Schedule], why[r.Schedule] = 0, ""
		}
	}
	if err := sc.Err(); err != nil {
		return nil, nil, fmt.Errorf("read schedule records: %w", err)
	}
	return n, why, nil
}

// PauseReason is the notice a schedule paused by itself carries.
func PauseReason(failures int, reason string) string {
	msg := fmt.Sprintf("Paused by itself after %d firings in a row that did not start.", failures)
	if reason != "" {
		msg += " Last reason: " + reason
	}
	return msg + " Fix the cause, then resume it."
}
