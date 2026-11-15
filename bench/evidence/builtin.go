// Package evidence holds the built-in evidence sources (docs/DESIGN.md 11 and
// 13.4). Each source reads one record of what happened in a run, writes the
// raw record to the run folder, and fills its part of schemas.Evidence:
// the fault proxy's ledger, the gateway's request rows normalized into
// events, the agent's own stream and final claim, the tool shim's log, and
// the cost ledger priced from the pinned snapshot.
//
// The sources run in the order Builtin returns them because later ones read
// what earlier ones filled: the gateway fetch needs the ledger to know what a
// complete set looks like, and cost needs both.
package evidence

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// version is the version every built-in evidence source reports. A source
// that changes what it records gets its own constant and a new version.
const version = "1.0.0"

// Deps is what the evidence sources need from the rest of the bench. main.go
// fills it once. A nil Gateway or Fault means that component is not
// configured, and the source that reads it records nothing.
type Deps struct {
	Gateway     schemas.Gateway
	Fault       schemas.FaultController
	Parsers     []schemas.Parser
	Docker      utils.Docker
	HelperImage string // image with cat, used to read the aux volume
	Prices      *PriceTable
	Logger      schemas.Logger
}

// Builtin returns the evidence sources that ship with the bench, in the order
// they must run: proxy ledger, gateway, agent stream, shim log, cost.
func Builtin(deps Deps) []schemas.EvidenceSource {
	return []schemas.EvidenceSource{
		newProxyLedgerSource(deps),
		newGatewaySource(deps),
		newAgentStreamSource(deps),
		newShimLogSource(deps),
		newCostSource(deps),
	}
}

// eventID builds "<run_id>:<source>:<seq>", the id format of DESIGN.md 16.4
// that keeps independent sources from colliding.
func eventID(runID string, src schemas.EventSource, seq int) string {
	return fmt.Sprintf("%s:%s:%d", runID, src, seq)
}

// eventSeq hands out per-source sequence numbers and stamps the run's ids on
// every event, so each source builds events the same way.
type eventSeq struct {
	runID, slotID, pairID string
	src                   schemas.EventSource
	level                 schemas.EvidenceLevel
	next                  int
}

// newEventSeq starts a sequence at 1 for one source of one run. rc may be
// partly empty in tests; missing ids stay empty.
func newEventSeq(rc *schemas.RunContext, src schemas.EventSource, level schemas.EvidenceLevel) *eventSeq {
	s := &eventSeq{src: src, level: level, next: 1}
	if rc == nil {
		return s
	}
	if rc.Run != nil {
		s.runID, s.pairID = rc.Run.RunID, rc.Run.PairID
		s.slotID = rc.Run.SlotID
	}
	if rc.Slot != nil {
		if s.slotID == "" {
			s.slotID = rc.Slot.SlotID
		}
		if s.pairID == "" {
			s.pairID = rc.Slot.PairID
		}
	}
	return s
}

// event returns the next event of the sequence with its ids filled.
func (s *eventSeq) event(typ schemas.EventType, ts time.Time) schemas.Event {
	seq := s.next
	s.next++
	return schemas.Event{
		EventVersion:  schemas.EventSchemaVersion,
		EventID:       eventID(s.runID, s.src, seq),
		RunID:         s.runID,
		SlotID:        s.slotID,
		PairID:        s.pairID,
		Seq:           seq,
		Sequence:      seq,
		TS:            ts,
		Timestamp:     ts,
		Type:          typ,
		Source:        s.src,
		Evidence:      s.level,
		EvidenceLevel: s.level,
		Status:        schemas.EventStatusUnknown,
	}
}

// runID returns the run id of rc, or "" when the context has no run.
func runID(rc *schemas.RunContext) string {
	if rc == nil || rc.Run == nil {
		return ""
	}
	return rc.Run.RunID
}

// pickLogger prefers the injected logger, then the run's, then a no-op one.
func pickLogger(l schemas.Logger, rc *schemas.RunContext) schemas.Logger {
	if l != nil {
		return l
	}
	if rc != nil && rc.Logger != nil {
		return rc.Logger
	}
	return schemas.NoopLogger{}
}

// writeJSONL writes one JSON value per line to name in the run folder. An
// empty run folder means there is nowhere to write, which only happens when a
// caller collects evidence in memory, so nothing is written.
func writeJSONL[T any](dir, name string, rows []T) error {
	if dir == "" {
		return nil
	}
	var buf []byte
	for i, r := range rows {
		b, err := json.Marshal(r)
		if err != nil {
			return fmt.Errorf("encode %s line %d: %w", name, i+1, err)
		}
		buf = append(buf, b...)
		buf = append(buf, '\n')
	}
	return writeFileAtomic(filepath.Join(dir, name), buf)
}

// eventsFile is the run folder file holding every normalized event.
const eventsFile = "events.jsonl"

// writeEvents rewrites events.jsonl with every event collected so far. Each
// source that adds or changes events calls it, so the file holds the whole
// set after the last source and stays whole when a source is removed or
// fails (DESIGN.md 16.1 assigns the file to evidence).
func writeEvents(rc *schemas.RunContext, ev *schemas.Evidence) error {
	events := make([]schemas.Event, len(ev.Events))
	copy(events, ev.Events)
	for i := range events {
		events[i].Summary = utils.RedactSecrets(events[i].Summary)
		events[i].Target = utils.RedactSecrets(events[i].Target)
	}
	ev.Events = events
	if err := writeJSONL(rc.Dir, eventsFile, events); err != nil {
		return fmt.Errorf("write events for run %s: %w", runID(rc), err)
	}
	return nil
}

// writeJSON writes v as indented JSON to name in the run folder.
func writeJSON(dir, name string, v any) error {
	if dir == "" {
		return nil
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", name, err)
	}
	return writeFileAtomic(filepath.Join(dir, name), append(b, '\n'))
}

// writeFileAtomic writes through a temporary file and a rename, so a crash
// never leaves a half-written artifact for the seal to hash.
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %s: %w", tmp, err)
	}
	return nil
}

// clip shortens s to at most n bytes on a rune boundary, for inert summaries.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
