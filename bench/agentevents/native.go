package agentevents

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// native is one event of the native protocol (spec/agent-event.schema.json).
// Input, output and times stay raw: an agent may send any JSON there.
type native struct {
	Type       string                     `json:"type"`
	ID         json.RawMessage            `json:"id"`
	ParentID   json.RawMessage            `json:"parent_id"`
	Name       string                     `json:"name"`
	ToolCallID string                     `json:"tool_call_id"`
	Input      json.RawMessage            `json:"input"`
	Output     json.RawMessage            `json:"output"`
	Status     string                     `json:"status"`
	StartTime  json.RawMessage            `json:"start_time"`
	EndTime    json.RawMessage            `json:"end_time"`
	Attributes map[string]json.RawMessage `json:"attributes"`
}

// ParseNative decodes native events: JSON lines, a JSON array of objects, or
// one object. Lines on the stdout channel must start with the stdout prefix;
// other stdout lines are ignored and not counted. It returns the capped
// records and how many events were refused (a line that is not a JSON object
// with a type). now stamps Received and stands in for a missing start time.
func ParseNative(data []byte, channel string, lim Limits, now time.Time) ([]Record, int) {
	lim = lim.WithDefaults()
	var out []Record
	bad := 0
	addRaw := func(raw []byte) {
		var n native
		if err := json.Unmarshal(raw, &n); err != nil || strings.TrimSpace(n.Type) == "" {
			bad++
			return
		}
		out = append(out, n.record(channel, lim, now))
	}
	trimmed := bytes.TrimSpace(data)
	if channel != schemas.ReportChannelStdout && len(trimmed) > 0 && trimmed[0] == '[' {
		var arr []json.RawMessage
		if err := json.Unmarshal(trimmed, &arr); err != nil {
			return nil, 1
		}
		for _, raw := range arr {
			addRaw(raw)
		}
		return out, bad
	}
	for line := range bytes.SplitSeq(data, []byte("\n")) {
		if channel == schemas.ReportChannelStdout {
			// A stdout line may carry a carriage return or ANSI prefix noise;
			// only a line whose payload starts with the prefix counts.
			i := bytes.Index(line, []byte(schemas.AgentEventsStdoutPrefix))
			if i < 0 {
				continue
			}
			line = line[i+len(schemas.AgentEventsStdoutPrefix):]
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		addRaw(line)
	}
	return out, bad
}

// record turns a native event into a capped record.
func (n native) record(channel string, lim Limits, now time.Time) Record {
	r := Record{
		Channel:    channel,
		Type:       n.Type,
		ID:         scalarText(n.ID),
		ParentID:   scalarText(n.ParentID),
		Name:       n.Name,
		ToolCallID: n.ToolCallID,
		Input:      anyText(n.Input),
		Output:     anyText(n.Output),
		Status:     strings.ToLower(strings.TrimSpace(n.Status)),
		Start:      parseTime(n.StartTime),
		End:        parseTime(n.EndTime),
		Received:   now.UTC(),
	}
	if len(n.Attributes) > 0 {
		r.Attributes = map[string]string{}
		for k, v := range n.Attributes {
			if len(r.Attributes) >= 4*maxAttrs {
				break
			}
			r.Attributes[k] = anyText(v)
		}
	}
	if r.Status == "success" {
		r.Status = StatusOK
	}
	if r.Start.IsZero() {
		r.Start = r.Received
	}
	if !r.End.IsZero() && r.End.Before(r.Start) {
		r.End = time.Time{}
	}
	sanitize(&r, lim)
	return r
}

// scalarText reads an id sent as a string or a number.
func scalarText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var f json.Number
	if json.Unmarshal(raw, &f) == nil {
		return f.String()
	}
	return ""
}

// anyText keeps a string as it is and any other JSON value as compact JSON.
func anyText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var buf bytes.Buffer
	if json.Compact(&buf, raw) == nil {
		return buf.String()
	}
	return string(raw)
}

// parseTime reads an RFC 3339 string or Unix milliseconds. A value that is
// neither, or out of a sane range (before 2000 or after 2200), is zero.
func parseTime(raw json.RawMessage) time.Time {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return time.Time{}
	}
	var t time.Time
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if p, err := time.Parse(time.RFC3339Nano, s); err == nil {
			t = p
		} else if ms, err := strconv.ParseFloat(s, 64); err == nil {
			t = fromMillis(ms)
		}
	} else {
		var ms float64
		if json.Unmarshal(raw, &ms) == nil {
			t = fromMillis(ms)
		}
	}
	if t.Year() < 2000 || t.Year() > 2200 {
		return time.Time{}
	}
	return t.UTC()
}

func fromMillis(ms float64) time.Time {
	if math.IsNaN(ms) || math.IsInf(ms, 0) || ms <= 0 || ms > 1e15 {
		return time.Time{}
	}
	return time.UnixMilli(int64(ms)).Add(time.Duration((ms - math.Floor(ms)) * float64(time.Millisecond)))
}

// errUnsupported is returned for a collector body the package cannot read.
type errUnsupported string

func (e errUnsupported) Error() string { return string(e) }

func unsupported(format string, a ...any) error { return errUnsupported(fmt.Sprintf(format, a...)) }
