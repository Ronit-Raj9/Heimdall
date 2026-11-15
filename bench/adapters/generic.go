package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

const genericParserVersion = "1.1.0"

const (
	ParserCustomPython = "custom-python"
	ParserGeminiCLI    = "gemini-cli"
	ParserMiniSWE      = "mini-swe-agent"
)

type genericJSONParser struct{ name string }

func newGenericJSONParser(name string) *genericJSONParser { return &genericJSONParser{name: name} }

func (p *genericJSONParser) GetName() string    { return p.name }
func (p *genericJSONParser) GetVersion() string { return genericParserVersion }

func (p *genericJSONParser) Parse(ctx context.Context, rc *schemas.RunContext, raw []byte) (*schemas.ParseResult, error) {
	return p.ParseReader(ctx, rc, bytes.NewReader(raw))
}

func (p *genericJSONParser) ParseReader(ctx context.Context, rc *schemas.RunContext, raw io.Reader) (*schemas.ParseResult, error) {
	result := &schemas.ParseResult{}
	protocol := newProtocolState(rc)
	protocolSeen, heuristicLines := false, 0
	last := ""
	d, err := scanReader(ctx, raw, func(line []byte) parsedLine {
		last = lastText(line)
		if protocolLine(line) {
			protocolSeen = true
			return protocol.line(line)
		}
		var obj map[string]json.RawMessage
		if json.Unmarshal(line, &obj) != nil {
			return parsedLine{malformed: true}
		}
		typ := firstString(obj, "type", "event", "kind")
		if typ == "" {
			return parsedLine{}
		}
		result.Events = append(result.Events, genericEvent(rc, p.name, len(result.Events)+1, typ, obj))
		heuristicLines++
		return parsedLine{recognized: true, emitted: 1}
	})
	if protocolSeen {
		d.Recognized -= heuristicLines
		d.Unrecognized += heuristicLines
		return protocol.finish(d), err
	}
	result.Claim = ClaimFromText(last)
	d.Source, d.Parser, d.ParserVersion = schemas.TraceSourceGeneric, p.GetName(), p.GetVersion()
	if d.Emitted > 0 {
		d.Quality = schemas.TraceQualityDegraded
		d.Reason = "event types inferred from unversioned JSON fields"
	} else if result.Claim != nil && d.Malformed == 0 && d.Oversized == 0 {
		d.Quality = schemas.TraceQualityClaimOnly
	} else {
		d.Quality = traceQuality(d)
	}
	result.Diagnostics = &d
	return result, err
}

func (p *genericJSONParser) ParseExecutionOutput(ctx context.Context, rc *schemas.RunContext, raw []byte) (*schemas.ParseResult, error) {
	return p.Parse(ctx, rc, raw)
}

func (p *genericJSONParser) CollectEvents(ctx context.Context, rc *schemas.RunContext, raw []byte) ([]schemas.Event, error) {
	result, err := p.Parse(ctx, rc, raw)
	if result == nil {
		return nil, err
	}
	return result.Events, err
}

func genericEvent(rc *schemas.RunContext, parser string, seq int, typ string, obj map[string]json.RawMessage) schemas.Event {
	runID, slotID, pairID := "", "", ""
	if rc != nil && rc.Run != nil {
		runID, slotID, pairID = rc.Run.RunID, rc.Run.SlotID, rc.Run.PairID
	}
	if rc != nil && rc.Slot != nil {
		slotID, pairID = rc.Slot.SlotID, rc.Slot.PairID
	}
	eventType := schemas.EventModelResponse
	lower := strings.ToLower(typ)
	switch {
	case strings.Contains(lower, "tool") && (strings.Contains(lower, "result") || strings.Contains(lower, "finish")):
		eventType = schemas.EventToolResult
	case strings.Contains(lower, "tool"):
		eventType = schemas.EventToolCall
	case strings.Contains(lower, "error") || strings.Contains(lower, "fail"):
		eventType = schemas.EventError
	case strings.Contains(lower, "retry"):
		eventType = schemas.EventRetry
	}
	ts := time.Time{}
	if rc != nil && rc.Exec != nil {
		ts = rc.Exec.Started
	}
	if value := firstString(obj, "timestamp", "time"); value != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
			ts = parsed
		}
	}
	return schemas.Event{
		EventVersion: schemas.EventSchemaVersion,
		EventID:      fmtEventID(runID, schemas.SourceAgentStream, seq), RunID: runID, SlotID: slotID, PairID: pairID,
		Seq: seq, Sequence: seq, TS: ts, Timestamp: ts, Type: eventType, Source: schemas.SourceAgentStream,
		Evidence: schemas.EvidenceNative, EvidenceLevel: schemas.EvidenceNative, Status: schemas.EventStatusUnknown,
		ParserVersion: genericParserVersion, ToolName: firstString(obj, "tool", "tool_name", "name"),
		EvidenceSource: schemas.EvidenceSourceInferred, EvidenceStrength: schemas.EvidenceInferred,
		Summary: clip(firstString(obj, "message", "text", "output", "error"), summaryMax),
	}
}

func firstString(obj map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		var value string
		if raw, ok := obj[key]; ok && json.Unmarshal(raw, &value) == nil && value != "" {
			return value
		}
	}
	return ""
}

func lastText(raw []byte) string {
	// Claims never retain a stream-sized JSON record.
	if len(raw) > 4096 {
		raw = raw[len(raw)-4096:]
	}
	raw = bytes.TrimSpace(raw)
	if i := bytes.LastIndexByte(raw, '\n'); i >= 0 {
		raw = raw[i+1:]
	}
	return string(raw)
}

func fmtEventID(runID string, source schemas.EventSource, seq int) string {
	return runID + ":" + string(source) + ":" + strconv.Itoa(seq)
}
