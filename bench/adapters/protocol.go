package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// ParserProtocol parses the optional framework-neutral agent event protocol.
const ParserProtocol = "bifrost-agent-event"

const protocolParserVersion = "1.0.0"

type protocolParser struct{}

func newProtocolParser() *protocolParser { return &protocolParser{} }

func (p *protocolParser) GetName() string    { return ParserProtocol }
func (p *protocolParser) GetVersion() string { return protocolParserVersion }

func (p *protocolParser) Parse(ctx context.Context, rc *schemas.RunContext, raw []byte) (*schemas.ParseResult, error) {
	return p.ParseReader(ctx, rc, bytes.NewReader(raw))
}

func (p *protocolParser) ParseReader(ctx context.Context, rc *schemas.RunContext, raw io.Reader) (*schemas.ParseResult, error) {
	state := newProtocolState(rc)
	d, err := scanReader(ctx, raw, state.line)
	return state.finish(d), err
}

type protocolState struct {
	rc     *schemas.RunContext
	result schemas.ParseResult
	ids    map[string]string
}

func newProtocolState(rc *schemas.RunContext) *protocolState {
	return &protocolState{rc: rc, ids: map[string]string{}}
}

func (s *protocolState) line(line []byte) parsedLine {
	if len(line) == 0 || line[0] != '{' {
		return parsedLine{}
	}
	var rec schemas.AgentEventRecord
	if err := json.Unmarshal(line, &rec); err != nil {
		return parsedLine{recognized: protocolLine(line), malformed: true}
	}
	if !strings.HasPrefix(rec.Schema, schemas.AgentEventSchema) {
		return parsedLine{}
	}
	if rec.Schema != schemas.AgentEventSchema || rec.Version != schemas.AgentEventVersion {
		return parsedLine{recognized: true, malformed: true}
	}
	if rec.ID != "" && (s.ids[rec.ID] != "" || rec.ID == rec.ParentEventID) {
		return parsedLine{recognized: true, malformed: true}
	}
	e, claim, err := protocolEvent(s.rc, rec, len(s.result.Events)+1)
	if err != nil {
		return parsedLine{recognized: true, malformed: true}
	}
	if claim != nil {
		s.result.Claim = claim
	}
	if rec.ID != "" {
		s.ids[rec.ID] = e.EventID
	}
	s.result.Events = append(s.result.Events, *e)
	return parsedLine{recognized: true, emitted: 1}
}

func (s *protocolState) finish(d schemas.ParseDiagnostics) *schemas.ParseResult {
	for i := range s.result.Events {
		if parent := s.result.Events[i].ParentEventID; parent != "" {
			if canonical := s.ids[parent]; canonical != "" {
				s.result.Events[i].ParentEventID = canonical
			} else {
				s.result.Events[i].ParentEventID = ""
				d.Malformed++
			}
		}
	}
	d.Source, d.Parser, d.ParserVersion = schemas.TraceSourceProtocol, ParserProtocol, protocolParserVersion
	d.Emitted = len(s.result.Events)
	d.Quality = traceQuality(d)
	s.result.Diagnostics = &d
	return &s.result
}

func (p *protocolParser) ParseExecutionOutput(ctx context.Context, rc *schemas.RunContext, raw []byte) (*schemas.ParseResult, error) {
	return p.Parse(ctx, rc, raw)
}

func (p *protocolParser) CollectEvents(ctx context.Context, rc *schemas.RunContext, raw []byte) ([]schemas.Event, error) {
	result, err := p.Parse(ctx, rc, raw)
	if result == nil {
		return nil, err
	}
	return result.Events, err
}

func protocolEvent(rc *schemas.RunContext, rec schemas.AgentEventRecord, seq int) (*schemas.Event, *schemas.Claim, error) {
	if rec.Type == "" {
		return nil, nil, fmt.Errorf("event type is required")
	}
	if !validProtocolEventType(rec.Type) {
		return nil, nil, fmt.Errorf("unsupported event type %q", rec.Type)
	}
	if rec.Status != "" && rec.Status != schemas.EventStatusUnknown && rec.Status != schemas.EventStatusStarted && rec.Status != schemas.EventStatusSuccess && rec.Status != schemas.EventStatusError {
		return nil, nil, fmt.Errorf("invalid event status")
	}
	if rec.Type == schemas.EventClaim && rec.Claim == nil {
		return nil, nil, fmt.Errorf("claim record requires claim")
	}
	if rec.Tokens != nil && (rec.Tokens.Fresh < 0 || rec.Tokens.Out < 0 || rec.Tokens.CacheRead < 0 || rec.Tokens.CacheWrite < 0) {
		return nil, nil, fmt.Errorf("negative token count")
	}
	if len(rec.ID) > 500 || len(rec.ParentEventID) > 500 || len(rec.ToolCallID) > 500 || len(rec.ToolName) > 500 || len(rec.ArgsSHA256) > 128 || len(rec.ResultSHA256) > 128 {
		return nil, nil, fmt.Errorf("event identifier exceeds limit")
	}
	if len(rec.Summary) > summaryMax {
		rec.Summary = clip(rec.Summary, summaryMax)
	}
	e := genericEvent(rc, ParserProtocol, seq, string(rec.Type), map[string]json.RawMessage{})
	e.Type = rec.Type
	e.ParserVersion = protocolParserVersion
	e.ToolCallID = rec.ToolCallID
	e.ParentEventID = rec.ParentEventID
	e.ToolName = rec.ToolName
	e.Target = clip(rec.Target, targetMax)
	e.ArgsHash = rec.ArgsSHA256
	e.ResultHash = rec.ResultSHA256
	e.Summary = rec.Summary
	if rec.Status != "" {
		e.Status = rec.Status
	}
	e.IsError = rec.IsError
	if rec.Type == schemas.EventError || rec.IsError {
		e.IsError, e.Status = true, schemas.EventStatusError
	}
	e.Tokens = rec.Tokens
	if !rec.Timestamp.IsZero() {
		e.TS, e.Timestamp = rec.Timestamp, rec.Timestamp
	}
	if rec.Claim != nil {
		claim := *rec.Claim
		claim.Source = schemas.EvidenceNative
		claim.Text, claim.Matched = clip(claim.Text, claimTextMax), clip(claim.Matched, summaryMax)
		rec.Claim = &claim
		if e.Summary == "" && rec.Type == schemas.EventClaim {
			e.Summary = claim.Text
		}
	}
	e.EvidenceSource, e.EvidenceStrength = schemas.EvidenceSourceAgentStream, schemas.EvidencePartial
	return &e, rec.Claim, nil
}

func validProtocolEventType(t schemas.EventType) bool {
	switch t {
	case schemas.EventModelRequest, schemas.EventModelResponse, schemas.EventToolCall, schemas.EventToolResult,
		schemas.EventRetry, schemas.EventError, schemas.EventCompaction, schemas.EventClaim:
		return true
	default:
		return false
	}
}

func traceQuality(d schemas.ParseDiagnostics) schemas.TraceQuality {
	if d.Emitted == 0 {
		if d.Quality == schemas.TraceQualityClaimOnly {
			return schemas.TraceQualityClaimOnly
		}
		return schemas.TraceQualityDegraded
	}
	if d.Malformed > 0 || d.Oversized > 0 || d.Unrecognized > 0 || d.Truncated || d.Reason != "" {
		return schemas.TraceQualityPartial
	}
	return schemas.TraceQualityComplete
}

func protocolLine(line []byte) bool {
	var envelope struct {
		Schema string `json:"schema"`
	}
	return json.Unmarshal(line, &envelope) == nil && strings.HasPrefix(envelope.Schema, schemas.AgentEventSchema)
}
