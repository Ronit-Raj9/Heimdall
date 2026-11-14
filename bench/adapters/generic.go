package adapters

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

const genericParserVersion = "1.0.0"

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
	_ = ctx
	result := &schemas.ParseResult{}
	seq := 0
	for scanner := bufio.NewScanner(bytes.NewReader(raw)); scanner.Scan(); {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var obj map[string]json.RawMessage
		if json.Unmarshal([]byte(line), &obj) != nil {
			continue
		}
		typ := firstString(obj, "type", "event", "kind")
		if typ == "" {
			continue
		}
		seq++
		e := genericEvent(rc, p.name, seq, typ, obj)
		result.Events = append(result.Events, e)
	}
	if text := lastText(raw); text != "" {
		result.Claim = ClaimFromText(text)
	}
	return result, nil
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
	ts := time.Now().UTC()
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
	lines := strings.Split(string(raw), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if text := strings.TrimSpace(lines[i]); text != "" {
			return text
		}
	}
	return ""
}

func fmtEventID(runID string, source schemas.EventSource, seq int) string {
	return runID + ":" + string(source) + ":" + strconv.Itoa(seq)
}
