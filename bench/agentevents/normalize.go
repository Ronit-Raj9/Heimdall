package agentevents

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Options identify the run the events belong to and the secrets to redact.
type Options struct {
	RunID, SlotID, PairID string
	// Secrets are literal values to redact from every field (the run's
	// gateway key); the bench's redaction rules apply too.
	Secrets []string
	// ParserVersion is stamped on each event.
	ParserVersion string
	Limits        Limits
	// Now stamps records that carry no time (default time.Now).
	Now func() time.Time
}

// ParserVersion is the version of this package's mapping.
const ParserVersion = "agentevents/1.0.0"

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// Normalize turns records into events, ordered by start time, with the
// agent_reported origin and partial strength. Event ids are
// "<run>:agent_reported:<seq>"; a parent the agent named (a native parent_id
// or a span's parent) becomes ParentEventID when that parent is among the
// records (same channel and, for spans, same trace). At most
// Limits.MaxEvents are returned; the count of the rest is the second result.
func Normalize(recs []Record, o Options) ([]schemas.Event, int) {
	lim := o.Limits.WithDefaults()
	sorted := make([]Record, len(recs))
	copy(sorted, recs)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Start.Before(sorted[j].Start) })
	dropped := 0
	if len(sorted) > lim.MaxEvents {
		dropped = len(sorted) - lim.MaxEvents
		sorted = sorted[:lim.MaxEvents]
	}
	key := func(r Record, id string) string { return r.Channel + "\x00" + r.TraceID + "\x00" + id }
	ids := map[string]string{}
	for i, r := range sorted {
		if r.ID != "" {
			if _, dup := ids[key(r, r.ID)]; !dup {
				ids[key(r, r.ID)] = eventID(o.RunID, i+1)
			}
		}
	}
	out := make([]schemas.Event, 0, len(sorted))
	for i, r := range sorted {
		sanitize(&r, lim)
		red := func(s string) string { return Redact(s, o.Secrets...) }
		seq := i + 1
		e := schemas.Event{
			EventVersion:     schemas.EventSchemaVersion,
			EventID:          eventID(o.RunID, seq),
			RunID:            o.RunID,
			SlotID:           o.SlotID,
			PairID:           o.PairID,
			Seq:              seq,
			Sequence:         seq,
			TS:               r.Start.UTC(),
			Timestamp:        r.Start.UTC(),
			Type:             eventType(r.Type),
			Source:           schemas.SourceAgentReported,
			Evidence:         schemas.EvidenceNative,
			EvidenceLevel:    schemas.EvidenceNative,
			EvidenceSource:   schemas.EvidenceSourceAgentReported,
			EvidenceStrength: schemas.EvidencePartial,
			Status:           eventStatus(r.Status),
			ParserVersion:    o.ParserVersion,
			ToolCallID:       red(r.ToolCallID),
			ModelServed:      red(r.Model),
			IsError:          r.Status == StatusError || r.Type == TypeError,
			ReportedType:     r.Type,
			ReportedID:       red(r.ID),
			ReportedParent:   red(r.ParentID),
			Channel:          r.Channel,
		}
		if e.ParserVersion == "" {
			e.ParserVersion = ParserVersion
		}
		if r.ParentID != "" {
			if pid, ok := ids[key(r, r.ParentID)]; ok && pid != e.EventID {
				e.ParentEventID = pid
			}
		}
		if r.Type == TypeToolCall || r.Type == TypeToolResult {
			e.ToolName = red(r.Name)
			e.Target = red(clip(r.Input, 512))
		}
		if !r.End.IsZero() {
			d := r.End.Sub(r.Start).Milliseconds()
			e.DurationMS = &d
		}
		e.Summary = red(summary(r))
		setAgentFields(&e, r, red) // Multi-agent area: agent identity for attribution (agents.go)
		out = append(out, e)
	}
	return out, dropped
}

func eventID(runID string, seq int) string {
	return fmt.Sprintf("%s:%s:%d", runID, schemas.SourceAgentReported, seq)
}

func eventStatus(s string) schemas.EventStatus {
	switch s {
	case StatusOK:
		return schemas.EventStatusSuccess
	case StatusError:
		return schemas.EventStatusError
	case StatusStarted:
		return schemas.EventStatusStarted
	}
	return schemas.EventStatusUnknown
}

// summary is the short display line of a record: type, name, and the start
// of its output or input.
func summary(r Record) string {
	head := strings.ReplaceAll(r.Type, "_", " ")
	if r.Name != "" {
		head += " " + r.Name
	}
	detail := r.Output
	if detail == "" {
		detail = r.Input
	}
	if detail != "" {
		head += ": " + strings.Join(strings.Fields(detail), " ")
	}
	return clip(head, 200)
}

// FromNativeJSONL turns native event lines (or a JSON array) into events.
// The second result counts events refused (bad lines, over the cap).
func FromNativeJSONL(data []byte, channel string, o Options) ([]schemas.Event, int) {
	recs, bad := ParseNative(data, channel, o.Limits, o.now())
	evs, over := Normalize(recs, o)
	return evs, bad + over
}

// FromOTLP turns one OTLP/HTTP trace export body (protobuf or JSON, by
// content type) into events. The second result counts spans refused.
func FromOTLP(data []byte, contentType string, o Options) ([]schemas.Event, int, error) {
	recs, bad, err := ParseOTLP(data, contentType, o.Limits, o.now())
	if err != nil {
		return nil, 0, err
	}
	evs, over := Normalize(recs, o)
	return evs, bad + over, nil
}

// ReadRecords reads a collector ledger: one Record per line. A line that
// does not parse is counted, not fatal (a torn last line).
func ReadRecords(data []byte) ([]Record, int) {
	var out []Record
	bad := 0
	for line := range bytes.SplitSeq(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var r Record
		if err := json.Unmarshal(line, &r); err != nil || r.Type == "" {
			bad++
			continue
		}
		out = append(out, r)
	}
	return out, bad
}

// Status summarizes kept events per channel and reported type.
func Status(evs []schemas.Event, received, dropped int, collector, reason string) *schemas.AgentEventStatus {
	st := &schemas.AgentEventStatus{Collector: collector, Reason: reason, Received: received, Kept: len(evs), Dropped: dropped}
	for _, e := range evs {
		if st.Channels == nil {
			st.Channels, st.Kinds = map[string]int{}, map[string]int{}
		}
		st.Channels[e.Channel]++
		st.Kinds[e.ReportedType]++
	}
	return st
}
