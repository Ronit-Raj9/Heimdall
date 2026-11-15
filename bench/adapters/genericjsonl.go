package adapters

import (
	"bytes"
	"context"

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

// Parse implements schemas.Parser.
func (p *genericJSONLParser) Parse(_ context.Context, rc *schemas.RunContext, raw []byte) (*schemas.ParseResult, error) {
	o := agentevents.Options{ParserVersion: agentevents.ParserVersion}
	if rc != nil {
		if rc.Run != nil {
			o.RunID, o.SlotID, o.PairID = rc.Run.RunID, rc.Run.SlotID, rc.Run.PairID
		}
		if rc.Key != nil {
			o.Secrets = []string{rc.Key.Value}
		}
	}
	// Every line is an event; a prefixed line (the stdout channel's form)
	// is accepted too.
	raw = bytes.ReplaceAll(raw, []byte(schemas.AgentEventsStdoutPrefix), nil)
	evs, _ := agentevents.FromNativeJSONL(raw, schemas.ReportChannelStdout+"_lines", o)
	for i := range evs {
		evs[i].Channel = schemas.ReportChannelStdout
	}
	res := &schemas.ParseResult{Events: evs}
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].ReportedType == agentevents.TypeMessage && evs[i].Summary != "" {
			res.Claim = ClaimFromText(evs[i].Summary)
			break
		}
	}
	return res, nil
}
