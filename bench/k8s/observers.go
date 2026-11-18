package k8s

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/agentevents"
	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/schemas"
)

// The trusted step observers in the run pod (docs/steps-and-evidence.md,
// docs/kubernetes.md). They are the Docker driver's scripts, parsed by the
// Docker driver's code; what differs is where they run. Both are native
// sidecars, started before the agent and ready before it starts, so the
// process observer sees the agent from its first process. They write to a
// volume the agent does not mount, and the bench helper stops them and
// reads their output once the agent ended. As in Docker, an observer that
// fails is missing evidence, never a failed run.

// stopObserversScript lets the workspace observer settle, asks both
// observers to finish (SIGTERM, which makes each print its final counts)
// and waits up to five seconds for each to exit.
const stopObserversScript = `sleep 0.3
stop() {
  f=` + obsDir + `/$1.pid
  [ -s "$f" ] || return 0
  p=$(cat "$f")
  kill -TERM "$p" 2>/dev/null || return 0
  i=0
  while kill -0 "$p" 2>/dev/null && [ "$i" -lt 100 ]; do sleep 0.05; i=$((i+1)); done
}
stop fswatch
stop procwatch
exit 0`

// finishObservers stops both observers and writes processes.jsonl,
// the workspace events and observers.json, as the Docker driver does.
func (d *Driver) finishObservers(ctx context.Context, rc *schemas.RunContext, r runRef, in podInput, res *schemas.ExecResult) {
	if !in.ProcWatch && !in.FSWatch {
		return
	}
	agentEnd := time.Now().UTC()
	if res != nil && !res.Ended.IsZero() {
		agentEnd = res.Ended
	}
	secret := ""
	if rc.Key != nil {
		secret = rc.Key.Value
	}
	cctx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()
	// Restarts before the stop are deaths while the agent ran; the stop
	// itself ends each observer, and the kubelet then restarts it into
	// sleep (observerCommand).
	restarts := map[string]int{}
	if p, err := d.getPod(cctx, r); err == nil {
		for _, cs := range p.Status.InitContainerStatuses {
			restarts[cs.Name] = cs.RestartCount
		}
	}
	stopErr := ""
	if _, err := d.cfg.Client.ExecOutput(cctx, r.ns, podName, ctrBench, []string{"/bin/sh", "-c", stopObserversScript}, nil, 1<<16); err != nil {
		stopErr = err.Error()
	}
	var statuses []schemas.ObserverStatus
	if in.ProcWatch {
		st := schemas.ObserverStatus{Observer: schemas.ObserverProc, Mode: "proc_poll", IntervalMS: in.IntervalMS}
		if st.IntervalMS <= 0 {
			st.IntervalMS = environments.DefaultWatchIntervalMS
		}
		lines, why := d.readObserver(cctx, r, ctrProcWatch, stopErr, restarts[ctrProcWatch])
		var recs []schemas.ProcessRecord
		if why != "" {
			st.State, st.Reason = schemas.ObserverFailed, why
		} else {
			recs, st = environments.ParseProcLines(lines, secret, 0, agentEnd, st)
			recs = podView(recs, in.UID, in.mainPID)
			st.Records = len(recs)
			st.Coverage += "; " + podViewNote
		}
		if err := environments.WriteProcessRecords(rc.Dir, 0, recs); err != nil {
			d.cfg.Logger.Warn("write process observer records", "error", err.Error())
		}
		statuses = append(statuses, st)
	}
	if in.FSWatch {
		st := schemas.ObserverStatus{Observer: schemas.ObserverFS, Mode: "inotify"}
		lines, why := d.readObserver(cctx, r, ctrFSWatch, stopErr, restarts[ctrFSWatch])
		var evs []schemas.FileEvent
		if why != "" {
			st.State, st.Reason = schemas.ObserverFailed, why
		} else {
			evs, st = environments.ParseFSLines(lines, secret, 0, st)
		}
		if err := environments.WriteFileEvents(rc.Dir, 0, evs); err != nil {
			d.cfg.Logger.Warn("write workspace observer records", "error", err.Error())
		}
		statuses = append(statuses, st)
	}
	if err := environments.AppendObserverStatus(rc.Dir, statuses...); err != nil {
		d.cfg.Logger.Warn("write observer status", "error", err.Error())
	}
}

// readObserver returns an observer's output lines, or why it has none: it
// never got ready, it restarted (its record would be cut), or it could not
// be read.
func (d *Driver) readObserver(ctx context.Context, r runRef, name, stopErr string, restarts int) ([][]byte, string) {
	out, err := d.cfg.Client.ExecOutput(ctx, r.ns, podName, ctrBench, []string{"head", "-c", strconv.Itoa(observerMaxBytes), obsDir + "/" + name + ".jsonl"}, nil, observerMaxBytes)
	if err != nil {
		return nil, fmt.Sprintf("%s observer output could not be read: %v", name, err)
	}
	lines := bytes.Split(bytes.TrimRight(out, "\n"), []byte("\n"))
	ready := len(lines) > 0 && (bytes.Contains(lines[0], []byte(`"t":"ready"`)) || bytes.Contains(lines[0], []byte(`"t": "ready"`)))
	if !ready {
		msg, _ := d.cfg.Client.ExecOutput(ctx, r.ns, podName, ctrBench, []string{"head", "-c", "2000", obsDir + "/" + name + ".err"}, nil, 4096)
		why := strings.TrimSpace(string(msg))
		if why == "" {
			why = "no output"
		}
		return nil, fmt.Sprintf("%s observer never got ready: %s", name, why)
	}
	if restarts > 0 {
		return nil, fmt.Sprintf("%s observer died and was restarted %d times while the agent ran; its record is incomplete", name, restarts)
	}
	if stopErr != "" {
		return nil, fmt.Sprintf("%s observer could not be stopped: %s", name, stopErr)
	}
	return lines, ""
}

// podViewNote is added to the process observer's coverage.
const podViewNote = "the observer shares the pod's PID namespace: processes of other uids (the bench's containers, probes and execs, the event collector, the pause process) are left out, and the agent container's main process is shown as PID 1, as in its own namespace under Docker"

// mainPIDScript prints the PID of the agent container's main process, in
// the pod's shared PID namespace: the gate, a process of the agent's uid
// whose parent is outside the namespace (PPID 0), waiting for the bench to
// attach.
const mainPIDScript = `for d in /proc/[0-9]*; do
  u=$(sed -n 's/^Uid:[[:space:]]*\([0-9]*\).*/\1/p' "$d/status" 2>/dev/null)
  pp=$(sed -n 's/^PPid:[[:space:]]*\([0-9]*\).*/\1/p' "$d/status" 2>/dev/null)
  [ "$u" = "$1" ] && [ "$pp" = 0 ] && echo "${d#/proc/}"
done
exit 0`

// agentMainPID finds the agent's main process before it is released; 0
// when it cannot (the process view then has no PID 1).
func (d *Driver) agentMainPID(ctx context.Context, r runRef, uid int64) int {
	out, err := d.cfg.Client.ExecOutput(ctx, r.ns, podName, ctrBench, []string{"/bin/sh", "-c", mainPIDScript, "pid", strconv.FormatInt(uid, 10)}, nil, 1<<12)
	if err != nil {
		d.cfg.Logger.Warn("find the agent's main process", "run", r.runID, "error", err.Error())
		return 0
	}
	f := strings.Fields(string(out))
	if len(f) != 1 {
		return 0
	}
	n, err := strconv.Atoi(f[0])
	if err != nil {
		return 0
	}
	return n
}

// podView turns the process records of the pod's shared PID namespace into
// what the agent container's own namespace shows under Docker: only the
// agent's processes are kept (its uid; every other container of the pod
// runs as another uid: the bench's as root, the collector as 65534, pause
// as 65535; the agent cannot change its uid: no privilege escalation, every
// capability dropped), and the agent container's main process (mainPID,
// found before the agent was released; the observer can first see it while
// the runtime still sets it up as root) becomes PID 1, with its children
// pointing at it. It is pure.
func podView(recs []schemas.ProcessRecord, uid int64, mainPID int) []schemas.ProcessRecord {
	out := make([]schemas.ProcessRecord, 0, len(recs))
	for _, p := range recs {
		if p.PID == 1 || (int64(p.UID) != uid && (mainPID == 0 || p.PID != mainPID)) {
			continue
		}
		out = append(out, p)
	}
	if mainPID == 0 {
		return out
	}
	for i := range out {
		switch {
		case out[i].PID == mainPID:
			out[i].PID, out[i].PPID = 1, 0
		case out[i].PPID == mainPID:
			out[i].PPID = 1
		}
	}
	return out
}

// copyCollector copies the agent event collector's ledger and counters into
// the run folder and records whether it ran, as the Docker driver does
// after each session. A missing ledger means the agent posted nothing.
func (d *Driver) copyCollector(ctx context.Context, rc *schemas.RunContext, r runRef, in podInput) {
	if !d.cfg.EventsCollector {
		return
	}
	if !in.Collector {
		environments.WriteCollectorState(rc, "missing", "no collector image (images.toolproxy); only prefixed stdout lines are read")
		return
	}
	cctx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()
	// The collector logs to stderr as it runs, so a failure is a collector
	// container that is not running, or one that restarted.
	if p, err := d.getPod(cctx, r); err == nil {
		if cs := containerStatus(p, ctrCollector, true); cs == nil || cs.State.Running == nil || cs.RestartCount > 0 {
			why, _ := d.cfg.Client.ExecOutput(cctx, r.ns, podName, ctrBench, []string{"tail", "-c", "600", ledgerDir + "/collector.err"}, nil, 4096)
			environments.WriteCollectorState(rc, "missing", "collector not running: "+clip(strings.TrimSpace(string(why)), 300))
			return
		}
	}
	environments.WriteCollectorState(rc, "on", "")
	for src, dst := range map[string]string{agentevents.LedgerName: environments.CollectorLedgerFile, agentevents.CounterName: environments.CollectorCountsFile} {
		out, err := d.cfg.Client.ExecOutput(cctx, r.ns, podName, ctrBench, []string{"head", "-c", strconv.Itoa(environments.MaxLedgerBytes), ledgerDir + "/" + src}, nil, int64(environments.MaxLedgerBytes))
		if err != nil {
			continue // not written: the agent posted nothing
		}
		if err := os.WriteFile(filepath.Join(rc.Dir, dst), out, 0o644); err != nil {
			d.cfg.Logger.Warn("copy agent events", "run", r.runID, "file", dst, "error", err.Error())
		}
	}
}
