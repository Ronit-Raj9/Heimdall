package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Observer rows: the trusted process and workspace observers ran beside the
// agent and recorded what it did (docs/steps-and-evidence.md).
const (
	rowProcObserver = "process_observer"
	rowFSObserver   = "workspace_observer"
)

func init() {
	rowTitles[rowProcObserver] = "process observer saw the agent's commands"
	rowTitles[rowFSObserver] = "workspace observer saw file changes"
}

// observerFix is the fix hint when an observer did not run or saw nothing
// it should have: the observers belong to the bench, not to the agent.
const observerFix = "run bifrost-bench doctor and rebuild the bench image; the observers are on by default and run beside the agent"

// observerRecords is what the observer rows read from one check run: the
// observers' status and their records. A file that could not be read leaves
// its part nil with the reason in its *Err, so a row can say it is unknown
// instead of reading it as zero activity.
type observerRecords struct {
	statuses  []schemas.ObserverStatus
	statusErr string
	procs     []schemas.ProcessRecord
	procsErr  string
	files     []schemas.FileEvent
	filesErr  string
}

// readObservers reads observers.json, processes.jsonl and fsevents.jsonl of a
// check run folder; a file that is missing falls back to the evidence fields
// when the evidence has them.
func readObservers(dir string, ev *schemas.Evidence) *observerRecords {
	o := &observerRecords{}
	b, err := os.ReadFile(filepath.Join(dir, schemas.ObserversFile))
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &o.statuses); err != nil {
			o.statuses, o.statusErr = nil, schemas.ObserversFile+" does not parse: "+err.Error()
		}
	case len(ev.Observers) > 0:
		o.statuses = ev.Observers
	default:
		o.statusErr = schemas.ObserversFile + " missing from the run folder (" + err.Error() + ")"
	}
	o.procs, o.procsErr = readJSONLines[schemas.ProcessRecord](filepath.Join(dir, schemas.ProcessesFile), ev.Processes)
	o.files, o.filesErr = readJSONLines[schemas.FileEvent](filepath.Join(dir, schemas.FSEventsFile), ev.FileEvents)
	return o
}

// readJSONLines reads a file of one JSON record per line. A missing file
// falls back to the evidence records when there are any, else is reported,
// never read as zero records.
func readJSONLines[T any](path string, fallback []T) ([]T, string) {
	b, err := os.ReadFile(path)
	if err != nil {
		if len(fallback) > 0 {
			return fallback, ""
		}
		return nil, filepath.Base(path) + " missing from the run folder (" + err.Error() + ")"
	}
	out := []T{}
	for i, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var v T
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			return nil, fmt.Sprintf("%s line %d does not parse: %v", filepath.Base(path), i+1, err)
		}
		out = append(out, v)
	}
	return out, ""
}

// observerState sums up one observer over every session: observed when it
// ran (for every session it was on in), off when it was switched off in all
// of them, missing when it was on but could not run in one. It returns an
// empty state, with the reason, when the run has no status for it.
func observerState(o *observerRecords, name string) (state, detail string, st []schemas.ObserverStatus) {
	if o == nil {
		return "", "the run recorded no observer status", nil
	}
	if o.statusErr != "" {
		return "", o.statusErr, nil
	}
	for _, s := range o.statuses {
		if s.Observer == name {
			st = append(st, s)
		}
	}
	if len(st) == 0 {
		return "", schemas.ObserversFile + " has no " + name + " observer status", nil
	}
	var missing, off []string
	for _, s := range st {
		label := firstNonEmpty(s.Reason, "no reason given")
		if s.Session > 0 {
			label = fmt.Sprintf("session %d: %s", s.Session, label)
		}
		switch s.State {
		case schemas.ObserverOn:
		case schemas.ObserverOff:
			off = append(off, label)
		default:
			missing = append(missing, label)
		}
	}
	switch {
	case len(missing) > 0:
		return schemas.ObserverFailed, listed(missing), st
	case len(off) == len(st):
		return schemas.ObserverOff, listed(off), st
	}
	return schemas.ObserverOn, "", st
}

// observerStatusRow fills a row whose observer did not run as observed: no
// status (WARN, activity unknown; also an observer switched off in config,
// which records nothing), off for a remote agent (SKIP) or missing (WARN
// with the observer's reason). It reports false when the observer ran.
func observerStatusRow(row checkRow, state, detail, what string) (checkRow, bool) {
	switch state {
	case schemas.ObserverOn:
		return row, false
	case schemas.ObserverOff:
		return row.set(checkSkip, "switched off ("+detail+"): "+what+" not observed", ""), true
	case schemas.ObserverFailed:
		fix := observerFix
		if hf, ok := helperFix(detail); ok {
			fix = hf
		} else if strings.Contains(detail, "is not running") || strings.Contains(detail, "before the process observer could join") {
			fix = "nothing to fix for an agent that exits in well under a second: the observer joins a moment after the agent starts; a real agent run lasts long enough"
		}
		return row.set(checkWarn, "the observer did not run ("+detail+"): "+what+" unknown, not zero", fix), true
	default:
		return row.set(checkWarn, detail+": "+what+" unknown, not zero", observerFix), true
	}
}

// processObserverRow: the process observer ran and saw at least one
// process. It always sees the agent's own command, so a run with no process
// recorded means the observer watched the wrong place.
func processObserverRow(in *checkInput) checkRow {
	row := newRow(rowProcObserver)
	state, detail, st := observerState(in.observers, schemas.ObserverProc)
	if r, done := observerStatusRow(row, state, detail, "commands"); done {
		return r
	}
	o := in.observers
	switch {
	case o.procs == nil:
		return row.set(checkFail, "the process observer reports observed, but "+o.procsErr, observerFix)
	case len(o.procs) == 0:
		return row.set(checkFail, "the process observer ran but recorded no process, not even the agent's own command: commands would read as zero",
			"run bifrost-bench doctor; the process observer must share the agent container's PID namespace")
	}
	seen := map[string]bool{}
	var names []string
	for _, p := range o.procs {
		n := firstNonEmpty(p.Comm, firstArg(p.Argv))
		if n != "" && !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	sort.Strings(names)
	d := fmt.Sprintf("%d processes recorded", len(o.procs))
	if len(o.procs) == 1 {
		d = "1 process recorded"
	}
	if len(names) > 0 {
		d += " (" + listed(names) + ")"
	}
	return row.set(checkPass, d+observerNotes(st), "")
}

// workspaceObserverRow: the workspace observer ran and saw the agent's file
// changes. An agent that changes no file legitimately shows none, so zero
// events is a WARN, unless the diff changed files: then the observer missed
// writes, a FAIL.
func workspaceObserverRow(in *checkInput) checkRow {
	row := newRow(rowFSObserver)
	state, detail, st := observerState(in.observers, schemas.ObserverFS)
	if r, done := observerStatusRow(row, state, detail, "file changes"); done {
		return r
	}
	o := in.observers
	changed := 0
	if in.ev.Diff != nil {
		changed = len(in.ev.Diff.Files)
	}
	switch {
	case o.files == nil:
		return row.set(checkFail, "the workspace observer reports observed, but "+o.filesErr, observerFix)
	case len(o.files) == 0 && changed > 0:
		return row.set(checkFail, fmt.Sprintf("the workspace observer saw no file event, but the diff changed %s: the observer missed writes", plural(changed, "file")),
			"run bifrost-bench doctor; the workspace observer must watch the workspace volume the agent writes")
	case len(o.files) == 0:
		return row.set(checkWarn, "no file event recorded; an agent that changes no file legitimately shows none (the scripted model asks for no edit)",
			"nothing to fix if the agent wrote no file; if it did, run bifrost-bench doctor")
	}
	ops := map[string]int{}
	seen := map[string]bool{}
	var paths []string
	for _, e := range o.files {
		ops[e.Op]++
		if !seen[e.Path] {
			seen[e.Path] = true
			paths = append(paths, e.Path)
		}
	}
	var counts []string
	for _, op := range []string{schemas.FileCreate, schemas.FileModify, schemas.FileDelete, schemas.FileRename} {
		if ops[op] > 0 {
			counts = append(counts, fmt.Sprintf("%d %s", ops[op], op))
		}
	}
	sort.Strings(paths)
	d := fmt.Sprintf("%s (%s) on %s", plural(len(o.files), "file event"), strings.Join(counts, ", "), listed(paths))
	return row.set(checkPass, d+observerNotes(st), "")
}

// observerNotes are the observer's mode, coverage and losses, for a passing
// row's detail: "; proc_poll every 50ms, saw 14 of 15 PIDs, 2 dropped".
func observerNotes(st []schemas.ObserverStatus) string {
	var mode string
	var interval, allocated, seen, dropped int
	for _, s := range st {
		if s.State != schemas.ObserverOn {
			continue
		}
		mode = firstNonEmpty(mode, s.Mode)
		interval = max(interval, s.IntervalMS)
		allocated += s.PIDsAllocated
		seen += s.PIDsSeen
		dropped += s.Dropped
	}
	var notes []string
	if mode != "" {
		if interval > 0 {
			mode += fmt.Sprintf(" every %dms", interval)
		}
		notes = append(notes, mode)
	}
	if allocated > 0 {
		notes = append(notes, fmt.Sprintf("saw %d of %d PIDs", seen, allocated))
	}
	if dropped > 0 {
		notes = append(notes, fmt.Sprintf("%d dropped", dropped))
	}
	if len(notes) == 0 {
		return ""
	}
	return "; " + strings.Join(notes, ", ")
}
