package evidence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/agentevents"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// AgentEvents is the name of the agent-reported events source.
const AgentEvents = "agent_events"

// Run folder files the environment kernel writes for the collector
// (environments/eventcollector.go), and where the events file lives in the
// aux volume.
const (
	collectorLedgerFile = "agent_events.collector.jsonl"
	collectorCountsFile = "agent_events.counts.json"
	collectorStateFile  = "agent_events.collector.json"
	eventsFilePath      = auxMount + "/events/events.jsonl"
	agentEventsMax      = 16 << 20
	// GenericJSONLParser names the parser whose whole stdout is native event
	// lines; the stdout channel then needs no prefix and is not read twice.
	GenericJSONLParser = "generic_jsonl"
)

var _ schemas.EvidenceSource = (*agentEventsSource)(nil)

// agentEventsSource reads what the agent reported about itself: the
// collector's ledger (OTLP spans and native events), the events file in the
// aux volume and prefixed stdout lines. It turns them into events with the
// agent_reported origin (agentevents.Normalize), adds them to ev.Events and
// writes agent_events.jsonl and agent_events.json. It runs before the step
// timeline, which joins the events to the trusted witnesses. Nothing here
// can fail a run: a source that cannot be read leaves its channel empty.
type agentEventsSource struct {
	docker utils.Docker
	image  string
	logger schemas.Logger
}

// GetName implements schemas.Part.
func (s *agentEventsSource) GetName() string { return AgentEvents }

// GetVersion implements schemas.Part.
func (s *agentEventsSource) GetVersion() string { return version }

// Collect implements schemas.EvidenceSource.
func (s *agentEventsSource) Collect(ctx context.Context, rc *schemas.RunContext, ev *schemas.Evidence) error {
	if rc == nil || rc.Dir == "" {
		return nil
	}
	now := time.Now()
	if rc.Exec != nil && !rc.Exec.Ended.IsZero() {
		now = rc.Exec.Ended
	}
	lim := agentevents.Limits{}.WithDefaults()
	var recs []agentevents.Record
	received, dropped := 0, 0
	state, reason := readCollectorState(rc.Dir)
	if raw := readCapped(filepath.Join(rc.Dir, collectorLedgerFile)); raw != nil {
		r, bad := agentevents.ReadRecords(raw)
		recs = append(recs, r...)
		dropped += bad
		received += len(r) + bad
	}
	if raw := readCapped(filepath.Join(rc.Dir, collectorCountsFile)); raw != nil {
		if c, err := agentevents.ReadCounts(raw); err == nil {
			// The collector already counted what it refused at its caps.
			dropped += c.Dropped
			received += c.Dropped
		}
	}
	var file []byte
	if state != "" {
		// The events folder exists only when the collector feature is on.
		file = s.readEventsFile(ctx, rc)
	}
	if len(file) > 0 {
		r, bad := agentevents.ParseNative(file, schemas.ReportChannelFile, lim, now)
		recs = append(recs, r...)
		dropped += bad
		received += len(r) + bad
	}
	if rc.Exec != nil && rc.Exec.StdoutPath != "" && (rc.Agent == nil || rc.Agent.Parser != GenericJSONLParser) {
		if raw := readCapped(rc.Exec.StdoutPath); bytes.Contains(raw, []byte(schemas.AgentEventsStdoutPrefix)) {
			r, bad := agentevents.ParseNative(raw, schemas.ReportChannelStdout, lim, now)
			recs = append(recs, r...)
			dropped += bad
			received += len(r) + bad
		}
	}
	// Events a generic_jsonl parser already put in ev.Events count too.
	var parsed []schemas.Event
	for _, e := range ev.Events {
		if e.Source == schemas.SourceAgentReported {
			parsed = append(parsed, e)
		}
	}
	if state == "" && len(recs) == 0 && len(parsed) == 0 && dropped == 0 {
		// Collector switched off (or a run recorded before it) and nothing
		// reported: no status, so the metrics read unknown.
		return nil
	}
	var secrets []string
	if rc.Key != nil {
		secrets = append(secrets, rc.Key.Value)
	}
	ids := newEventSeq(rc, schemas.SourceAgentReported, schemas.EvidenceNative)
	evs, over := agentevents.Normalize(recs, agentevents.Options{
		RunID: ids.runID, SlotID: ids.slotID, PairID: ids.pairID, Secrets: secrets, Limits: lim,
	})
	if len(parsed) > 0 {
		// Renumber after the parser's events so ids stay unique.
		evs = renumber(evs, ids.runID, len(parsed))
		evs = append(parsed, evs...)
	}
	dropped += over
	if state == "" {
		state = "off"
		reason = "no collector ran; events came from the file or stdout channel"
	}
	ev.AgentEvents = agentevents.Status(evs, received+len(parsed), dropped, state, reason)
	kept := map[string]bool{}
	for _, e := range evs {
		kept[e.EventID] = true
	}
	out := make([]schemas.Event, 0, len(ev.Events)+len(evs))
	for _, e := range ev.Events {
		if e.Source != schemas.SourceAgentReported || !kept[e.EventID] {
			out = append(out, e)
		}
	}
	ev.Events = append(out, evs...)
	var errs []error
	if err := writeJSONL(rc.Dir, schemas.AgentEventsFile, evs); err != nil {
		errs = append(errs, fmt.Errorf("write agent events for run %s: %w", runID(rc), err))
	}
	if err := writeJSON(rc.Dir, schemas.AgentEventsStatusFile, ev.AgentEvents); err != nil {
		errs = append(errs, err)
	}
	if err := writeEvents(rc, ev); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// renumber shifts event ids and parent ids by n, for events appended after n
// others of the same source.
func renumber(evs []schemas.Event, run string, n int) []schemas.Event {
	re := func(id string) string {
		i := strings.LastIndexByte(id, ':')
		if i < 0 {
			return id
		}
		k, err := strconv.Atoi(id[i+1:])
		if err != nil {
			return id
		}
		return eventID(run, schemas.SourceAgentReported, k+n)
	}
	for i := range evs {
		evs[i].EventID = re(evs[i].EventID)
		if evs[i].ParentEventID != "" {
			evs[i].ParentEventID = re(evs[i].ParentEventID)
		}
		evs[i].Seq += n
		evs[i].Sequence += n
	}
	return evs
}

func readCollectorState(dir string) (string, string) {
	raw := readCapped(filepath.Join(dir, collectorStateFile))
	if raw == nil {
		return "", ""
	}
	var st struct{ Collector, Reason string }
	if json.Unmarshal(raw, &st) != nil {
		return "", ""
	}
	return st.Collector, st.Reason
}

// readCapped reads at most agentEventsMax bytes of a file; nil when absent.
func readCapped(p string) []byte {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, agentEventsMax))
	if err != nil {
		return nil
	}
	return b
}

// readEventsFile copies the agent's events file out of the aux volume,
// read-only and with no network, like the shim log. It is agent-written, so
// it is capped; a missing file is no events.
func (s *agentEventsSource) readEventsFile(ctx context.Context, rc *schemas.RunContext) []byte {
	if s.image == "" || rc.Handle == nil || rc.Handle.AuxVolume == "" {
		return nil
	}
	expID := ""
	if rc.Run != nil {
		expID = rc.Run.ExpID
	}
	args := []string{"run", "--rm", "--network", "none", "--security-opt", "no-new-privileges"}
	args = append(args, utils.LabelArgs(expID, runID(rc))...)
	args = append(args, "-v", rc.Handle.AuxVolume+":"+auxMount+":ro", s.image, "head", "-c", strconv.Itoa(agentEventsMax), eventsFilePath)
	res, err := s.docker.Run(ctx, args...)
	if err != nil || res.ExitCode != 0 {
		return nil
	}
	return res.Stdout
}

// newAgentEventsSource returns the agent_events source.
func newAgentEventsSource(deps Deps) *agentEventsSource {
	return &agentEventsSource{docker: deps.Docker, image: deps.HelperImage, logger: deps.Logger}
}
