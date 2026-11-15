package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// goodObservers are observer records where both observer rows pass: both
// observers ran, the process observer saw the agent and the workspace
// observer saw the edit the good check run's diff holds.
func goodObservers() *observerRecords {
	start := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	return &observerRecords{
		statuses: []schemas.ObserverStatus{
			{Observer: schemas.ObserverProc, State: schemas.ObserverOn, Mode: "proc_poll", IntervalMS: 50, Records: 2, PIDsAllocated: 3, PIDsSeen: 3},
			{Observer: schemas.ObserverFS, State: schemas.ObserverOn, Mode: "inotify", Records: 1},
		},
		procs: []schemas.ProcessRecord{
			{PID: 1, UID: 1000, Comm: "bench-agent", Argv: []string{"bench-agent"}, Start: start},
			{PID: 7, PPID: 1, UID: 1000, Comm: "git", Argv: []string{"git", "status"}, Start: start.Add(time.Second)},
		},
		files: []schemas.FileEvent{{TS: start.Add(2 * time.Second), Op: schemas.FileModify, Path: "a.py"}},
	}
}

func TestObserverRows(t *testing.T) {
	tests := []struct {
		name         string
		mutate       func(in *checkInput)
		proc, fs     string // statuses; empty means PASS
		detail, fsIn string // substrings of the process and workspace row details
	}{
		{name: "both observed", mutate: func(*checkInput) {},
			detail: "2 processes recorded (bench-agent, git); proc_poll every 50ms, saw 3 of 3 PIDs", fsIn: "1 file event (1 modify) on a.py; inotify"},
		{name: "no observers.json", mutate: func(in *checkInput) {
			in.observers = &observerRecords{statusErr: "observers.json missing from the run folder (no such file)"}
		}, proc: checkWarn, fs: checkWarn, detail: "observers.json missing from the run folder (no such file): commands unknown, not zero", fsIn: "file changes unknown, not zero"},
		{name: "nothing read", mutate: func(in *checkInput) { in.observers = nil },
			proc: checkWarn, fs: checkWarn, detail: "no observer status", fsIn: "no observer status"},
		{name: "no status for an observer", mutate: func(in *checkInput) { in.observers.statuses = in.observers.statuses[1:] },
			proc: checkWarn, detail: "observers.json has no process observer status"},
		{name: "missing with reason", mutate: func(in *checkInput) {
			in.observers.statuses[0].State, in.observers.statuses[0].Reason = schemas.ObserverFailed, "cannot join the PID namespace"
			in.observers.statuses[1].State, in.observers.statuses[1].Reason = schemas.ObserverFailed, "inotify watch limit reached"
		}, proc: checkWarn, fs: checkWarn, detail: "the observer did not run (cannot join the PID namespace)", fsIn: "inotify watch limit reached"},
		{name: "missing in one session", mutate: func(in *checkInput) {
			in.observers.statuses[0].Session = 1
			in.observers.statuses = append(in.observers.statuses, schemas.ObserverStatus{Observer: schemas.ObserverProc, Session: 2,
				State: schemas.ObserverFailed, Reason: "observer exited early"})
		}, proc: checkWarn, detail: "session 2: observer exited early"},
		{name: "switched off", mutate: func(in *checkInput) {
			in.observers.statuses[0].State, in.observers.statuses[0].Reason = schemas.ObserverOff, "observers.process false"
			in.observers.statuses[1].State = schemas.ObserverOff
		}, proc: checkSkip, fs: checkSkip, detail: "switched off (observers.process false)", fsIn: "switched off (no reason given)"},
		{name: "zero processes", mutate: func(in *checkInput) { in.observers.procs = []schemas.ProcessRecord{} },
			proc: checkFail, detail: "recorded no process"},
		{name: "processes file missing", mutate: func(in *checkInput) {
			in.observers.procs, in.observers.procsErr = nil, "processes.jsonl missing from the run folder"
		}, proc: checkFail, detail: "reports observed, but processes.jsonl missing"},
		{name: "no file event, no change", mutate: func(in *checkInput) {
			in.observers.files = []schemas.FileEvent{}
			in.ev.Diff.Files = nil
		}, fs: checkWarn, fsIn: "an agent that changes no file legitimately shows none"},
		{name: "no file event, diff changed", mutate: func(in *checkInput) { in.observers.files = []schemas.FileEvent{} },
			fs: checkFail, fsIn: "the diff changed 1 file: the observer missed writes"},
		{name: "fsevents file missing", mutate: func(in *checkInput) {
			in.observers.files, in.observers.filesErr = nil, "fsevents.jsonl missing from the run folder"
		}, fs: checkFail, fsIn: "reports observed, but fsevents.jsonl missing"},
		{name: "drops noted", mutate: func(in *checkInput) { in.observers.statuses[0].Dropped = 4 }, detail: "4 dropped"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := goodCheckInput()
			tt.mutate(in)
			for _, c := range []struct {
				row          checkRow
				want, detail string
			}{{processObserverRow(in), tt.proc, tt.detail}, {workspaceObserverRow(in), tt.fs, tt.fsIn}} {
				want := c.want
				if want == "" {
					want = checkPass
				}
				if c.row.Status != want || c.row.Title == "" {
					t.Errorf("%s = %s %q (%s), want %s", c.row.ID, c.row.Status, c.row.Title, c.row.Detail, want)
				}
				if (want == checkWarn || want == checkFail) && c.row.Fix == "" {
					t.Errorf("%s is %s with no fix hint", c.row.ID, want)
				}
				if c.detail != "" && !strings.Contains(c.row.Detail, c.detail) {
					t.Errorf("%s detail %q, want %q", c.row.ID, c.row.Detail, c.detail)
				}
			}
		})
	}
}

func TestObserverHelperFix(t *testing.T) {
	in := goodCheckInput()
	in.observers.statuses[0].State = schemas.ObserverFailed
	in.observers.statuses[0].Reason = "helper image acme/node:22 has no python3; set images.helper to the bench helper image"
	if r := processObserverRow(in); r.Status != checkWarn || !strings.Contains(r.Fix, "set images.helper in deploy/bench.yaml") {
		t.Errorf("helper fix not used: %+v", r)
	}
}

func TestCheckRowsObserverOrder(t *testing.T) {
	ids := []string{}
	for _, r := range checkRows(goodCheckInput()) {
		ids = append(ids, r.ID)
	}
	got := strings.Join(ids, ",")
	if !strings.Contains(got, rowEgress+","+rowProcObserver+","+rowFSObserver+","+rowEvidence) {
		t.Errorf("observer rows not after the egress row: %s", got)
	}
	skipped := skippedRows(withScenarioRows(rowOrder[1:], &schemas.Suite{}, &schemas.Task{}), "no image")
	n := 0
	for _, r := range skipped {
		if r.ID == rowProcObserver || r.ID == rowFSObserver {
			n++
		}
	}
	if n != 2 {
		t.Errorf("the observer rows are not skipped when the image fails: %+v", skipped)
	}
}

func TestReadObservers(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	o := readObservers(dir, &schemas.Evidence{})
	if o.statuses != nil || !strings.Contains(o.statusErr, "observers.json missing") ||
		o.procs != nil || !strings.Contains(o.procsErr, "processes.jsonl missing") ||
		o.files != nil || !strings.Contains(o.filesErr, "fsevents.jsonl missing") {
		t.Fatalf("empty run folder: %+v", o)
	}
	ev := &schemas.Evidence{
		Observers:  []schemas.ObserverStatus{{Observer: schemas.ObserverProc, State: schemas.ObserverOn}},
		Processes:  []schemas.ProcessRecord{{PID: 1}},
		FileEvents: []schemas.FileEvent{{Op: schemas.FileCreate, Path: "x"}},
	}
	if o := readObservers(dir, ev); len(o.statuses) != 1 || len(o.procs) != 1 || len(o.files) != 1 || o.statusErr+o.procsErr+o.filesErr != "" {
		t.Errorf("evidence fallback: %+v", o)
	}
	write(schemas.ObserversFile, `[{"observer":"process","state":"observed","mode":"proc_poll","records":2},{"observer":"workspace","state":"missing","reason":"no inotify","records":0}]`)
	write(schemas.ProcessesFile, `{"pid":1,"ppid":0,"uid":1000,"comm":"agent","start":"2026-11-15T10:00:00Z"}`+"\n\n"+
		`{"pid":5,"ppid":1,"uid":1000,"comm":"sh","start":"2026-11-15T10:00:01Z","exit_code":0}`+"\n")
	write(schemas.FSEventsFile, "")
	o = readObservers(dir, ev)
	if len(o.statuses) != 2 || o.statusErr != "" || len(o.procs) != 2 || o.procs[1].Comm != "sh" || o.procsErr != "" {
		t.Fatalf("run folder: %+v", o)
	}
	if o.files == nil || len(o.files) != 0 || o.filesErr != "" {
		t.Fatalf("an empty fsevents.jsonl is zero events, read: %+v %q", o.files, o.filesErr)
	}
	in := goodCheckInput()
	in.observers = o
	if r := workspaceObserverRow(in); r.Status != checkWarn || !strings.Contains(r.Detail, "no inotify") {
		t.Errorf("workspace row from the run folder: %+v", r)
	}
	write(schemas.ObserversFile, `{not json`)
	write(schemas.ProcessesFile, "{not json\n")
	o = readObservers(dir, ev)
	if !strings.Contains(o.statusErr, "observers.json does not parse") || !strings.Contains(o.procsErr, "processes.jsonl line 1 does not parse") {
		t.Errorf("bad files: %q %q", o.statusErr, o.procsErr)
	}
	in = &checkInput{}
	if readJSONFile(filepath.Join(dir, "absent.json"), &in.ev) {
		t.Error("readJSONFile read an absent file")
	}
}
