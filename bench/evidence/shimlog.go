package evidence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// ShimLog is the name of the tool shim log source.
const ShimLog = "shim_log"

// Where the shim log lives: the aux volume is mounted at /opt/bench in the
// agent container and the shims append to log/shim.jsonl there.
const (
	auxMount    = "/opt/bench"
	shimLogPath = auxMount + "/log/shim.jsonl"
	shimCrash   = "crash"
	shimEmpty   = "empty"
	noSuchFile  = "No such file"
	// shimLogMax caps what is read: the log directory is writable by the
	// agent user, so its size is not under the bench's control.
	shimLogMax = 8 << 20
)

var _ schemas.EvidenceSource = (*shimLogSource)(nil)

// shimLogSource reads what the tool shim recorded. The log sits on the run's
// aux volume, never in the workspace, so it is read with a short-lived helper
// container instead of a host bind mount.
type shimLogSource struct {
	docker utils.Docker
	image  string
	logger schemas.Logger
}

// GetName implements schemas.Part.
func (s *shimLogSource) GetName() string { return ShimLog }

// GetVersion implements schemas.Part.
func (s *shimLogSource) GetVersion() string { return version }

// Collect implements schemas.EvidenceSource. It only acts for a tool_shim
// probe. A missing log file means the shim never ran, which is an observed
// empty log (the fired receipt reads it as "no"), not a failure. Malformed
// lines are skipped and reported as one error event.
func (s *shimLogSource) Collect(ctx context.Context, rc *schemas.RunContext, ev *schemas.Evidence) error {
	if rc.Probe == nil || rc.Probe.Layer != schemas.LayerToolShim {
		return nil
	}
	id := runID(rc)
	if rc.Handle == nil || rc.Handle.AuxVolume == "" {
		return fmt.Errorf("read shim log for run %s: %w", id, errNoAuxVolume)
	}
	if s.image == "" {
		return fmt.Errorf("read shim log for run %s: %w", id, errNoHelperImage)
	}
	raw, err := s.read(ctx, rc)
	if err != nil {
		return fmt.Errorf("read shim log for run %s: %w", id, err)
	}
	calls, bad := ParseShimLog(raw)
	for i := range calls {
		calls[i].Argv = utils.RedactSecrets(calls[i].Argv)
	}
	ev.ShimLog = calls
	events := shimEvents(rc, calls)
	if bad > 0 {
		pickLogger(s.logger, rc).Warn("shim log has malformed lines", "run_id", id, "lines", bad)
		seq := newEventSeq(rc, schemas.SourceShimLog, schemas.EvidenceBoundary)
		seq.next = len(events) + 1
		e := seq.event(schemas.EventError, time.Time{})
		e.IsError = true
		e.Summary = fmt.Sprintf("shim log: %d malformed lines skipped", bad)
		events = append(events, e)
	}
	ev.Events = append(ev.Events, events...)
	return writeEvents(rc, ev)
}

var (
	errNoAuxVolume   = errors.New("run has no aux volume")
	errNoHelperImage = errors.New("no helper image configured")
)

// ParseShimLog parses the shim's JSONL log. It returns the calls in file order
// and the number of non-blank lines that did not parse; those are skipped,
// because a shim killed mid-write leaves a torn last line and the rest of the
// log is still good evidence. It is pure.
func ParseShimLog(raw []byte) ([]schemas.ShimCall, int) {
	calls := []schemas.ShimCall{}
	bad := 0
	for line := range bytes.SplitSeq(raw, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var c schemas.ShimCall
		if err := json.Unmarshal(line, &c); err != nil || c.Tool == "" || c.Action == "" {
			bad++
			continue
		}
		calls = append(calls, c)
	}
	return calls, bad
}

// read copies the shim log out of the aux volume, read-only, with no
// network. It uses head -c rather than cat so an agent that floods the log
// cannot exhaust the bench's memory; a cut last line is a malformed line.
func (s *shimLogSource) read(ctx context.Context, rc *schemas.RunContext) ([]byte, error) {
	args := []string{"run", "--rm", "--network", "none", "--security-opt", "no-new-privileges"}
	expID := ""
	if rc.Run != nil {
		expID = rc.Run.ExpID
	}
	args = append(args, utils.LabelArgs(expID, runID(rc))...)
	args = append(args, "-v", rc.Handle.AuxVolume+":"+auxMount+":ro", s.image, "head", "-c", strconv.Itoa(shimLogMax), shimLogPath)
	res, err := s.docker.Run(ctx, args...)
	if err != nil {
		return nil, err
	}
	if res.ExitCode == 0 {
		return res.Stdout, nil
	}
	if strings.Contains(string(res.Stderr), noSuchFile) {
		return nil, nil
	}
	return nil, fmt.Errorf("helper exit %d: %s", res.ExitCode, strings.TrimSpace(string(res.Stderr)))
}

// shimEvents returns one fault event per broken invocation. The nonce stays
// out of the event; only the receipt compares it.
func shimEvents(rc *schemas.RunContext, calls []schemas.ShimCall) []schemas.Event {
	seq := newEventSeq(rc, schemas.SourceShimLog, schemas.EvidenceBoundary)
	var out []schemas.Event
	for _, c := range calls {
		if c.Action != shimCrash && c.Action != shimEmpty {
			continue
		}
		ts, err := time.Parse(time.RFC3339Nano, c.At)
		if err != nil {
			ts = time.Time{}
		}
		e := seq.event(schemas.EventFault, ts)
		e.ToolName = c.Tool
		e.Target = strings.TrimSpace(c.Tool + " " + c.Argv)
		e.IsError = c.Action == shimCrash
		e.Summary = clip(fmt.Sprintf("shim %s on %s call %d", c.Action, c.Tool, c.Call), 200)
		out = append(out, e)
	}
	return out
}

// newShimLogSource returns the shim_log source.
func newShimLogSource(deps Deps) *shimLogSource {
	return &shimLogSource{docker: deps.Docker, image: deps.HelperImage, logger: deps.Logger}
}
