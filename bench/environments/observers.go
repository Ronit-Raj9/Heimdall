package environments

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// The trusted step observers (docs/steps-and-evidence.md). Both run from the
// helper image with python3, no network, a read-only root and the run's
// labels, so Destroy and the janitor remove a leftover. Neither can fail a
// run: an observer that cannot start or stops early is recorded in
// observers.json as missing, with the reason, and the agent runs as usual.

// procwatchScript polls /proc in the agent's PID namespace.
//
//go:embed procwatch.py
var procwatchScript string

// fswatchScript watches the workspace volume with inotify.
//
//go:embed fswatch.py
var fswatchScript string

// Observer limits.
const (
	// DefaultWatchIntervalMS is the process observer's poll interval. At
	// 20 ms it catches every process that lives 20 ms or more and about a
	// third of 5 ms ones, for about 3% of one CPU core.
	DefaultWatchIntervalMS = 20
	observerReady          = 30 * time.Second
	observerStop           = 5 * time.Second
	observerMaxLines       = 200000
	observerMaxBytes       = 64 << 20
	fsWatchMaxEvents       = 20000
	fsWatchSettle          = 300 * time.Millisecond
	// procWatchEarly is how long before the agent's end the process observer
	// may stop before the bench calls it stopped early: the agent's PID 1
	// exiting ends every process in the namespace, the observer included,
	// a moment before `docker start -a` returns.
	procWatchEarly = 1500 * time.Millisecond
)

// observer is one running observer container: `docker run -i` with the
// script on stdin (so the agent, which can see the process observer, sees
// only "python3 -u -"), and its stdout lines kept in memory, capped.
type observer struct {
	d      utils.Docker
	logger schemas.Logger
	name   string
	cmd    *exec.Cmd
	mu     sync.Mutex
	lines  [][]byte
	size   int
	cut    int
	ready  chan struct{}
	done   chan struct{}
	ended  time.Time
	stderr bytes.Buffer
	once   sync.Once
	keep   bool // stop leaves the container for a retry (not yet ready)
}

// runObserver starts an observer container and returns once it printed its
// ready line.
func (e *dockerWorkspace) runObserver(ctx context.Context, name string, args []string, script string, removeOnFail bool) (*observer, error) {
	o := &observer{d: e.cfg.Docker, logger: e.cfg.Logger, name: name, ready: make(chan struct{}), done: make(chan struct{}), keep: !removeOnFail}
	o.cmd = exec.Command(dockerBinary(e.cfg.Docker), args...)
	o.cmd.Stdin = strings.NewReader(script)
	o.cmd.Stderr = &o.stderr
	stdout, err := o.cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("observer stdout: %w", err)
	}
	if err := o.cmd.Start(); err != nil {
		return nil, fmt.Errorf("start observer: %w", err)
	}
	go o.copy(stdout)
	select {
	case <-o.ready:
		return o.started(), nil
	case <-o.done:
		o.stop(ctx, 0) // waits for the process, so stderr is complete
		msg := strings.TrimSpace(o.stderr.String())
		if tool := missingExecutable(msg); tool != "" {
			msg = utils.HelperLacks(args[len(args)-4], tool)
		}
		if lines, _, _ := o.output(); msg == "" {
			msg = strings.TrimSpace(string(bytes.Join(lines, []byte(" "))))
		}
		err = fmt.Errorf("observer exited before it was ready: %s", msg)
	case <-time.After(observerReady):
		err = fmt.Errorf("observer not ready after %s", observerReady)
	case <-ctx.Done():
		err = ctx.Err()
	}
	o.stop(ctx, 0)
	return nil, err
}

// started marks a ready observer: from now on stop removes its container.
func (o *observer) started() *observer {
	o.keep = false
	return o
}

// copy keeps the observer's lines, capped, closes ready on the first ready
// line and done when stdout ends.
func (o *observer) copy(r io.Reader) {
	defer func() {
		o.mu.Lock()
		o.ended = time.Now().UTC()
		o.mu.Unlock()
		close(o.done)
	}()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	isReady := false
	for sc.Scan() {
		line := append([]byte(nil), sc.Bytes()...)
		if !isReady && (bytes.Contains(line, []byte(`"t":"ready"`)) || bytes.Contains(line, []byte(`"t": "ready"`))) {
			isReady = true
			close(o.ready)
		}
		o.mu.Lock()
		if len(o.lines) >= observerMaxLines || o.size+len(line) > observerMaxBytes {
			o.cut++
		} else {
			o.lines = append(o.lines, line)
			o.size += len(line)
		}
		o.mu.Unlock()
	}
}

// stop lets the observer settle, asks it to finish (SIGTERM), waits for its
// output to drain and removes the container. Safe to call more than once.
func (o *observer) stop(ctx context.Context, settle time.Duration) {
	o.once.Do(func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), observerStop+killGrace)
		defer cancel()
		if o.cmd != nil && o.cmd.Process != nil {
			select {
			case <-o.done:
			default:
				time.Sleep(settle)
				if _, err := o.d.Run(cctx, "kill", "--signal", "TERM", o.name); err != nil {
					o.logger.Debug("stop observer", "container", o.name, "error", err.Error())
				}
				select {
				case <-o.done:
				case <-time.After(observerStop):
					o.logger.Warn("observer did not stop; removing it", "container", o.name)
				}
			}
		}
		rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), killGrace)
		defer rcancel()
		if o.keep {
			select {
			case <-o.done: // exited on its own: keep the container for a retry
				if o.cmd != nil && o.cmd.Process != nil {
					_ = o.cmd.Wait()
				}
				return
			default: // hung: never retried, so remove it
			}
		}
		if _, err := o.d.Run(rctx, "rm", "-f", o.name); err != nil {
			o.logger.Warn("remove observer", "container", o.name, "error", err.Error())
		}
		if o.cmd != nil && o.cmd.Process != nil {
			if err := o.cmd.Wait(); err != nil {
				o.logger.Debug("observer ended", "container", o.name, "error", err.Error())
			}
		}
	})
}

// output returns the kept lines, how many were cut, and when stdout ended
// (zero while it runs).
func (o *observer) output() ([][]byte, int, time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.lines, o.cut, o.ended
}

// observerArgs is the common prefix of both observers' docker run.
func observerArgs(name string, labels []string) []string {
	args := append([]string{"run", "-i", "--name", name}, labels...)
	return append(args, "--network", "none", "--user", "0", "--cap-drop", "ALL")
}

// stepObservers is one agent session's pair of observers.
type stepObservers struct {
	e        *dockerWorkspace
	session  int
	secret   string
	interval int
	procOn   bool
	fsOn     bool
	fs       *observer
	fsErr    error
	procMu   sync.Mutex
	proc     *observer
	procErr  error
	procDone chan struct{}
	stopProc chan struct{}
	watching bool // watchAgent was called
	once     sync.Once
}

// observersOff returns the reason both observers do not apply to this plan,
// or "": a remote agent's actions happen outside the bench. Observers
// switched off in config write nothing, like runs recorded before them.
func (e *dockerWorkspace) observersOff(plan *schemas.RunPlan) string {
	if len(plan.Egress) > 0 {
		return "remote agent: its actions happen outside the bench"
	}
	return ""
}

// startObservers starts the workspace observer before the agent container
// exists. The process observer needs a running container to join, so
// watchAgent starts it once the agent is up.
func (e *dockerWorkspace) startObservers(ctx context.Context, rc *schemas.RunContext, plan *schemas.RunPlan, expID, runID string) *stepObservers {
	_, _, session := streamPaths(rc)
	s := &stepObservers{e: e, session: session, procOn: e.cfg.ProcessWatch, fsOn: e.cfg.WorkspaceWatch,
		interval: e.cfg.WatchIntervalMS, procDone: make(chan struct{}), stopProc: make(chan struct{})}
	if s.interval <= 0 {
		s.interval = DefaultWatchIntervalMS
	}
	if rc.Key != nil {
		s.secret = rc.Key.Value
	}
	if !s.procOn && !s.fsOn {
		close(s.procDone)
		return s
	}
	if reason := e.observersOff(plan); reason != "" {
		s.procOn, s.fsOn = false, false
		close(s.procDone)
		s.writeOff(rc, reason)
		return s
	}
	if s.fsOn {
		name := helperName(runID, "fswatch")
		args := observerArgs(name, utils.LabelArgs(expID, runID))
		args = append(args, "--cap-add", "DAC_READ_SEARCH", "--security-opt", "no-new-privileges", "--read-only",
			"--pids-limit", "8", "--memory", "128m", "--log-driver", "none",
			"-v", rc.Handle.Volume+":/w:ro", "--entrypoint", "python3", e.helperImage(rc), "-u", "-", strconv.Itoa(fsWatchMaxEvents))
		s.fs, s.fsErr = e.runObserver(ctx, name, args, fswatchScript, true)
		if s.fsErr != nil {
			e.cfg.Logger.Warn("workspace observer did not start; the agent runs without it", "run", runID, "error", s.fsErr.Error())
		}
	}
	if !s.procOn {
		close(s.procDone)
	}
	return s
}

// watchAgent starts the process observer in the agent's PID namespace as
// soon as the agent container runs. It returns at once.
func (s *stepObservers) watchAgent(ctx context.Context, rc *schemas.RunContext, expID, runID, agent string) {
	if !s.procOn {
		return
	}
	s.watching = true
	e := s.e
	name := helperName(runID, "procwatch")
	args := append([]string{"create", "-i", "--name", name}, utils.LabelArgs(expID, runID)...)
	args = append(args, "--network", "none", "--user", "0", "--cap-drop", "ALL",
		"--pid", "container:"+agent, "--cap-add", "SYS_PTRACE", "--security-opt", "no-new-privileges",
		"--read-only", "--pids-limit", "8", "--memory", "128m", "--log-driver", "none")
	args = append(args, procWatchCommand(viewFor(rc), e.helperImage(rc), s.interval)...) // Eval integrity area (S2): the argv the agent sees in ps
	if _, err := e.cfg.Docker.MustRun(ctx, args...); err != nil {
		s.setProc(nil, fmt.Errorf("create process observer: %w", err))
		close(s.procDone)
		return
	}
	// The observer is created before the agent starts and started the moment
	// the agent's namespace exists: a start that comes too early fails with
	// "is not running" and is retried, so the gap is one docker start. A
	// start that fails another way while the agent runs gets a fresh
	// container; an agent that already ended is recorded as such.
	go func() {
		defer close(s.procDone)
		deadline := time.Now().Add(observerReady)
		recreated := 0
		fail := func(err error) {
			s.setProc(nil, err)
			removeObserver(ctx, e, name)
		}
		for {
			select {
			case <-s.stopProc:
				fail(errAgentEnded)
				return
			case <-ctx.Done():
				fail(ctx.Err())
				return
			default:
			}
			o, err := e.runObserver(ctx, name, []string{"start", "-a", "-i", name}, procwatchScript, false)
			if err == nil {
				s.setProc(o, nil)
				return
			}
			early := strings.Contains(err.Error(), "is not running")
			if !early && !agentRunning(ctx, e, agent) {
				fail(errAgentEnded)
				return
			}
			if time.Now().After(deadline) || (!early && recreated >= 3) {
				e.cfg.Logger.Warn("process observer did not start; the agent runs without it", "run", runID, "error", err.Error())
				fail(err)
				return
			}
			if !early {
				recreated++
				removeObserver(ctx, e, name)
				if _, cerr := e.cfg.Docker.MustRun(ctx, args...); cerr != nil {
					fail(fmt.Errorf("create process observer: %w", cerr))
					return
				}
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
}

// errAgentEnded is the process observer's reason when the agent exited
// before the observer could join its PID namespace.
var errAgentEnded = errors.New("the agent ended before the process observer could join it")

// agentRunning reports whether the agent container still runs. An inspect
// error counts as running, so the caller retries rather than guesses.
func agentRunning(ctx context.Context, e *dockerWorkspace, agent string) bool {
	res, err := e.cfg.Docker.Run(ctx, "inspect", "--format", "{{.State.Running}}", agent)
	if err != nil || res == nil {
		return true
	}
	return strings.HasPrefix(strings.TrimSpace(string(res.Stdout)), "true")
}

// removeObserver force-removes an observer container that never ran.
func removeObserver(ctx context.Context, e *dockerWorkspace, name string) {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), killGrace)
	defer cancel()
	if _, err := e.cfg.Docker.Run(rctx, "rm", "-f", name); err != nil {
		e.cfg.Logger.Warn("remove observer", "container", name, "error", err.Error())
	}
}

func (s *stepObservers) setProc(o *observer, err error) {
	s.procMu.Lock()
	defer s.procMu.Unlock()
	s.proc, s.procErr = o, err
}

// finish stops both observers after the agent ended and writes their
// records and statuses to the run folder. Problems are logged, never
// returned: observer trouble is missing evidence, not a failed run.
func (s *stepObservers) finish(ctx context.Context, rc *schemas.RunContext, res *schemas.ExecResult) {
	if s == nil || (!s.procOn && !s.fsOn) {
		return
	}
	s.once.Do(func() { s.finishOnce(ctx, rc, res) })
}

func (s *stepObservers) finishOnce(ctx context.Context, rc *schemas.RunContext, res *schemas.ExecResult) {
	agentEnd := time.Now().UTC()
	if res != nil && !res.Ended.IsZero() {
		agentEnd = res.Ended
	}
	close(s.stopProc)
	if s.procOn && !s.watching {
		s.setProc(nil, errors.New("the agent container never started"))
		close(s.procDone)
	}
	<-s.procDone
	var statuses []schemas.ObserverStatus
	if s.procOn {
		s.procMu.Lock()
		o, err := s.proc, s.procErr
		s.procMu.Unlock()
		st := schemas.ObserverStatus{Observer: schemas.ObserverProc, Session: s.session, Mode: "proc_poll", IntervalMS: s.interval}
		var recs []schemas.ProcessRecord
		if err != nil {
			st.State, st.Reason = schemas.ObserverFailed, err.Error()
		} else {
			o.stop(ctx, 0)
			lines, cut, ended := o.output()
			recs, st = parseProcLines(lines, s.secret, s.session, agentEnd, st)
			st.Dropped += cut
			if !ended.IsZero() && agentEnd.Sub(ended) > procWatchEarly {
				st.State = schemas.ObserverFailed
				st.Reason = fmt.Sprintf("process observer stopped %s before the agent ended; a root agent holds CAP_KILL and can stop it", agentEnd.Sub(ended).Round(time.Millisecond))
			}
		}
		if err := writeJSONL(rc.Dir, schemas.ProcessesFile, s.session, recs); err != nil {
			s.e.cfg.Logger.Warn("write process observer records", "error", err.Error())
		}
		statuses = append(statuses, st)
	}
	if s.fsOn {
		st := schemas.ObserverStatus{Observer: schemas.ObserverFS, Session: s.session, Mode: "inotify"}
		var evs []schemas.FileEvent
		if s.fsErr != nil {
			st.State, st.Reason = schemas.ObserverFailed, s.fsErr.Error()
		} else {
			s.fs.stop(ctx, fsWatchSettle)
			lines, cut, _ := s.fs.output()
			evs, st = parseFSLines(lines, s.session, st)
			for i := range evs {
				evs[i].Path, evs[i].From = redact(evs[i].Path, s.secret), redact(evs[i].From, s.secret)
			}
			st.Dropped += cut
		}
		if err := writeJSONL(rc.Dir, schemas.FSEventsFile, s.session, evs); err != nil {
			s.e.cfg.Logger.Warn("write workspace observer records", "error", err.Error())
		}
		statuses = append(statuses, st)
	}
	if err := appendObserverStatus(rc.Dir, statuses...); err != nil {
		s.e.cfg.Logger.Warn("write observer status", "error", err.Error())
	}
}

// writeOff records both observers as off for this session.
func (s *stepObservers) writeOff(rc *schemas.RunContext, reason string) {
	err := appendObserverStatus(rc.Dir,
		schemas.ObserverStatus{Observer: schemas.ObserverProc, Session: s.session, State: schemas.ObserverOff, Reason: reason},
		schemas.ObserverStatus{Observer: schemas.ObserverFS, Session: s.session, State: schemas.ObserverOff, Reason: reason})
	if err != nil {
		s.e.cfg.Logger.Warn("write observer status", "error", err.Error())
	}
}

// procLine is one line of the process observer's output.
type procLine struct {
	T          string   `json:"t"`
	PID        int      `json:"pid"`
	PPID       int      `json:"ppid"`
	ST         int64    `json:"st"`
	Start      float64  `json:"start"`
	At         float64  `json:"at"`
	UID        int      `json:"uid"`
	Comm       string   `json:"comm"`
	Argv       []string `json:"argv"`
	Cwd        string   `json:"cwd"`
	Cut        bool     `json:"cut"`
	Exit       *int     `json:"exit"`
	Signal     int      `json:"signal"`
	Alloc      int      `json:"alloc"`
	Seen       float64  `json:"seen"` // start lines: when first seen; stats lines: PIDs seen
	IntervalMS int      `json:"interval_ms"`
	Error      string   `json:"error"`
}

// parseProcLines turns the observer's start and end lines into process
// records, in start order, with secrets redacted, and fills the status.
// A process with no end line was still running when the session ended.
func parseProcLines(lines [][]byte, secret string, session int, agentEnd time.Time, st schemas.ObserverStatus) ([]schemas.ProcessRecord, schemas.ObserverStatus) {
	type key struct {
		pid int
		st  int64
	}
	var recs []schemas.ProcessRecord
	at := map[key]int{}
	stats := false
	for _, raw := range lines {
		var l procLine
		if json.Unmarshal(raw, &l) != nil {
			continue
		}
		switch l.T {
		case "start":
			r := schemas.ProcessRecord{PID: l.PID, PPID: l.PPID, Session: session, UID: l.UID, Comm: redact(l.Comm, secret),
				Cwd: redact(l.Cwd, secret), Start: unixTime(l.Start), ArgvTruncated: l.Cut}
			for _, a := range l.Argv {
				r.Argv = append(r.Argv, redact(a, secret))
			}
			at[key{l.PID, l.ST}] = len(recs)
			recs = append(recs, r)
		case "end":
			i, ok := at[key{l.PID, l.ST}]
			if !ok {
				continue
			}
			recs[i].End = unixTime(l.At)
			if l.Exit != nil {
				code := *l.Exit
				recs[i].ExitCode = &code
			} else if l.Signal > 0 {
				code := 128 + l.Signal
				recs[i].ExitCode = &code
			}
		case "stats":
			stats = true
			st.PIDsAllocated, st.PIDsSeen = l.Alloc, int(l.Seen)
			if l.IntervalMS > 0 {
				st.IntervalMS = l.IntervalMS
			}
		}
	}
	for i := range recs {
		if recs[i].End.IsZero() {
			recs[i].End, recs[i].RunningAtEnd = agentEnd, true
		}
	}
	st.State, st.Records = schemas.ObserverOn, len(recs)
	st.Coverage = fmt.Sprintf("polled /proc every %d ms; processes shorter than that can be missed", st.IntervalMS)
	if stats && st.PIDsAllocated > 0 {
		missed := max(st.PIDsAllocated-st.PIDsSeen, 0)
		st.Coverage += fmt.Sprintf("; %d of %d PIDs allocated in the agent's namespace were seen as a process or thread, so at most %d processes were missed", st.PIDsSeen, st.PIDsAllocated, missed)
	} else {
		st.Coverage += "; no PID counter report, so the miss count is not bounded"
	}
	return recs, st
}

// fsLine is one line of the workspace observer's output.
type fsLine struct {
	T       string   `json:"t"`
	TS      float64  `json:"ts"`
	Op      string   `json:"op"`
	Path    string   `json:"path"`
	From    string   `json:"from"`
	Size    *int64   `json:"size"`
	Dir     bool     `json:"dir"`
	Events  int      `json:"events"`
	Dropped int      `json:"dropped"`
	Watches int      `json:"watches"`
	Notes   []string `json:"notes"`
	Error   string   `json:"error"`
}

// parseFSLines turns the workspace observer's lines into file events and
// fills the status.
func parseFSLines(lines [][]byte, session int, st schemas.ObserverStatus) ([]schemas.FileEvent, schemas.ObserverStatus) {
	var evs []schemas.FileEvent
	watches, final := 0, false
	var notes []string
	for _, raw := range lines {
		var l fsLine
		if json.Unmarshal(raw, &l) != nil {
			continue
		}
		switch l.T {
		case "ready":
			watches = l.Watches
		case "fs":
			evs = append(evs, schemas.FileEvent{TS: unixTime(l.TS), Op: l.Op, Path: l.Path, From: l.From, Size: l.Size, Dir: l.Dir, Session: session})
		case "stats":
			final = true
			st.Dropped += l.Dropped
			watches = max(watches, l.Watches)
			notes = l.Notes
		}
	}
	st.State, st.Records = schemas.ObserverOn, len(evs)
	st.Coverage = fmt.Sprintf("inotify on the workspace volume, %d folders watched; .git internals and caches skipped; reads and writes outside the workspace are not seen", watches)
	if !final {
		st.Coverage += "; the observer did not report final counts, so changes in its last moments may be missing"
	}
	if len(notes) > 0 {
		st.Coverage += "; " + strings.Join(notes, "; ")
	}
	return evs, st
}

// redact removes the run's key and anything that looks like a credential.
func redact(s, secret string) string {
	if secret != "" {
		s = strings.ReplaceAll(s, secret, "[redacted]")
	}
	return utils.RedactSecrets(s)
}

// unixTime converts fractional Unix seconds to UTC time.
func unixTime(f float64) time.Time {
	if f <= 0 {
		return time.Time{}
	}
	sec := int64(f)
	return time.Unix(sec, int64((f-float64(sec))*1e9)).UTC()
}

// writeJSONL writes one record per line to name in dir. A session run
// writes name with the session index (processes.2.jsonl) and appends to the
// unindexed file, which the first session truncates.
func writeJSONL[T any](dir, name string, session int, recs []T) error {
	var buf bytes.Buffer
	for _, r := range recs {
		b, err := json.Marshal(r)
		if err != nil {
			return err
		}
		buf.Write(append(b, '\n'))
	}
	if session == 0 {
		return os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0o644)
	}
	indexed := strings.TrimSuffix(name, ".jsonl") + "." + strconv.Itoa(session) + ".jsonl"
	if err := os.WriteFile(filepath.Join(dir, indexed), buf.Bytes(), 0o644); err != nil {
		return err
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if session == 1 {
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	f, err := os.OpenFile(filepath.Join(dir, name), flags, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.Write(buf.Bytes())
	return errors.Join(werr, f.Close())
}

// appendObserverStatus adds statuses to observers.json. The first session
// of a run (or a run without sessions) starts the file afresh.
func appendObserverStatus(dir string, add ...schemas.ObserverStatus) error {
	path := filepath.Join(dir, schemas.ObserversFile)
	var all []schemas.ObserverStatus
	if len(add) > 0 && add[0].Session > 1 {
		if b, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(b, &all)
		}
	}
	all = append(all, add...)
	b, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// ReadObservers reads the observer files of a run folder: processes, file
// events and statuses. Missing files read as nil; a bad line is skipped.
func ReadObservers(dir string) ([]schemas.ProcessRecord, []schemas.FileEvent, []schemas.ObserverStatus, error) {
	var statuses []schemas.ObserverStatus
	b, err := os.ReadFile(filepath.Join(dir, schemas.ObserversFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil, nil
	}
	if err != nil {
		return nil, nil, nil, err
	}
	if err := json.Unmarshal(b, &statuses); err != nil {
		return nil, nil, nil, fmt.Errorf("read %s: %w", schemas.ObserversFile, err)
	}
	procs, err := readJSONL[schemas.ProcessRecord](filepath.Join(dir, schemas.ProcessesFile))
	if err != nil {
		return nil, nil, statuses, err
	}
	evs, err := readJSONL[schemas.FileEvent](filepath.Join(dir, schemas.FSEventsFile))
	return procs, evs, statuses, err
}

func readJSONL[T any](path string) ([]T, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []T
	for _, line := range bytes.Split(b, []byte("\n")) {
		var r T
		if len(bytes.TrimSpace(line)) == 0 || json.Unmarshal(line, &r) != nil {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}
