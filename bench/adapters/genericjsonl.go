package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"github.com/maximhq/bifrost/bench/agentevents"
	"github.com/maximhq/bifrost/bench/schemas"
)

// ParserGenericJSONL is the framework-neutral parser: the agent's stdout is
// native agent events, one JSON object per line (spec/agent-event.schema.json,
// docs/agent-events.md). Lines that are not events are ignored. The events
// are agent-reported (source agent_reported, partial strength); the claim
// comes from the last message event's text, if any.
const ParserGenericJSONL = "generic_jsonl"

type genericJSONLParser struct{}

func newGenericJSONLParser() *genericJSONLParser { return &genericJSONLParser{} }

// GetName implements schemas.Part.
func (p *genericJSONLParser) GetName() string { return ParserGenericJSONL }

// GetVersion implements schemas.Part.
func (p *genericJSONLParser) GetVersion() string { return agentevents.ParserVersion }

// Parse implements schemas.Parser. Lines are read through the shared scanner,
// so damage is counted like every other parser's: a line that starts as JSON
// and does not parse is malformed, a line over the size limit is oversized,
// and anything else that is not an event is unrecognized. None of them stops
// the lines after it.
func (p *genericJSONLParser) Parse(ctx context.Context, rc *schemas.RunContext, raw []byte) (*schemas.ParseResult, error) {
	o := agentevents.Options{ParserVersion: agentevents.ParserVersion}
	if rc != nil {
		if rc.Run != nil {
			o.RunID, o.SlotID, o.PairID = rc.Run.RunID, rc.Run.SlotID, rc.Run.PairID
		}
		if rc.Key != nil {
			o.Secrets = []string{rc.Key.Value}
		}
	}
	channel := schemas.ReportChannelStdout + "_lines"
	prefix := []byte(schemas.AgentEventsStdoutPrefix)
	var kept bytes.Buffer
	d, err := scanLines(ctx, raw, func(line []byte) parsedLine {
		// Every line is an event; a prefixed line (the stdout channel's
		// form) is accepted too.
		line = bytes.TrimSpace(bytes.ReplaceAll(line, prefix, nil))
		if len(line) == 0 {
			return parsedLine{}
		}
		if !json.Valid(line) {
			return parsedLine{malformed: line[0] == '{' || line[0] == '['}
		}
		recs, _ := agentevents.ParseNative(line, channel, o.Limits, time.Time{})
		if len(recs) == 0 {
			return parsedLine{}
		}
		kept.Write(line)
		kept.WriteByte('\n')
		return parsedLine{recognized: true, emitted: len(recs)}
	})
	evs, _ := agentevents.FromNativeJSONL(kept.Bytes(), channel, o)
	for i := range evs {
		evs[i].Channel = schemas.ReportChannelStdout
	}
	d.Emitted = len(evs)
	d.Source, d.Parser, d.ParserVersion = schemas.TraceSourceProtocol, p.GetName(), p.GetVersion()
	d.Quality = traceQuality(d)
	res := &schemas.ParseResult{Events: evs, Diagnostics: &d}
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].ReportedType == agentevents.TypeMessage && evs[i].Summary != "" {
			res.Claim = ClaimFromText(evs[i].Summary)
			break
		}
	}
	return res, err
}
