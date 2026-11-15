package adapters

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestProtocolParserNormalizesEventsAndDiagnostics(t *testing.T) {
	rc := &schemas.RunContext{Run: &schemas.Run{RunID: "r_1", SlotID: "s_1", PairID: "p_1"}}
	raw := `{"schema":"bifrost.agent.event","version":"1","type":"model_response","summary":"answer"}` + "\n" +
		`{"schema":"bifrost.agent.event","version":"1","type":"tool_call","tool":"shell","target":"pytest -q"}` + "\n" +
		`{"schema":"bifrost.agent.event","version":"1","type":"claim","claim":{"made":true,"source":"native","matched":"done"}}` + "\n"
	res, err := newProtocolParser().Parse(context.Background(), rc, []byte(raw))
	if err != nil || res == nil {
		t.Fatalf("result = %+v, err = %v", res, err)
	}
	if len(res.Events) != 3 || res.Events[1].Type != schemas.EventToolCall || res.Claim == nil || !res.Claim.Made {
		t.Fatalf("result = %+v", res)
	}
	if res.Diagnostics == nil || res.Diagnostics.Source != schemas.TraceSourceProtocol || res.Diagnostics.Emitted != 3 || res.Diagnostics.Quality != schemas.TraceQualityComplete {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}
}

func TestProtocolValidationAndParentIdentity(t *testing.T) {
	raw := `{"schema": "bifrost.agent.event", "version": "1", "type": "tool_call", "id": "call", "tool_call_id": "tc1"}` + "\n" +
		`{"schema":"bifrost.agent.event","version":"1","type":"tool_result","parent_event_id":"call","tool_call_id":"tc1"}` + "\n" +
		`{"schema":"bifrost.agent.event","version":"1","type":"retry","timestamp":"bad"}` + "\n" +
		`{"schema":"bifrost.agent.event","version":"1"}` + "\n" +
		`{"schema":"bifrost.agent.event","version":"1","type":"claim"}` + "\n" +
		`{"schema":"bifrost.agent.event","version":"1","type":"bogus"}` + "\n" +
		`{"schema":"bifrost.agent.event","version":"1","type":"model_response","status":"bogus"}`
	parser := newProtocolParser()
	rc := &schemas.RunContext{Run: &schemas.Run{RunID: "r_test"}}
	res, err := parser.Parse(context.Background(), rc, []byte(raw))
	if err != nil || len(res.Events) != 2 || res.Diagnostics.Malformed != 5 || res.Events[1].ParentEventID != res.Events[0].EventID {
		t.Fatalf("result=%+v diagnostics=%+v err=%v", res, res.Diagnostics, err)
	}
	again, _ := parser.Parse(context.Background(), rc, []byte(raw))
	if !reflect.DeepEqual(res, again) {
		t.Fatal("parsing the same capture produced different output")
	}
}

func TestProtocolEventKindsAndClipping(t *testing.T) {
	for _, kind := range []schemas.EventType{schemas.EventModelRequest, schemas.EventModelResponse, schemas.EventToolCall,
		schemas.EventToolResult, schemas.EventRetry, schemas.EventError, schemas.EventCompaction, schemas.EventClaim} {
		rec := schemas.AgentEventRecord{Schema: schemas.AgentEventSchema, Version: schemas.AgentEventVersion, Type: kind,
			Summary: strings.Repeat("é", 300), Target: strings.Repeat("é", 300),
			Claim: &schemas.Claim{Made: true, Source: schemas.EvidenceBoundary, Text: "Done."}}
		data, _ := json.Marshal(rec)
		res, err := newProtocolParser().Parse(context.Background(), nil, data)
		if err != nil || len(res.Events) != 1 || res.Events[0].Type != kind || utf8.RuneCountInString(res.Events[0].Summary) > summaryMax || utf8.RuneCountInString(res.Events[0].Target) > targetMax || res.Claim.Source != schemas.EvidenceNative {
			t.Fatalf("%s: result=%+v err=%v", kind, res, err)
		}
	}
}

func TestProtocolParserCountsBadAndOversizedLines(t *testing.T) {
	oversized := `{"schema":"bifrost.agent.event","version":"1","type":"error","summary":"` + strings.Repeat("x", maxParserLineBytes) + `"}`
	raw := []byte("{bad json\n" + oversized + "\n" + `{"schema":"bifrost.agent.event","version":"9","type":"error"}` + "\n")
	res, err := newProtocolParser().Parse(context.Background(), &schemas.RunContext{}, raw)
	if err != nil || res == nil || res.Diagnostics == nil {
		t.Fatalf("result = %+v, err = %v", res, err)
	}
	if res.Diagnostics.Oversized != 1 || res.Diagnostics.Malformed != 2 || res.Diagnostics.Emitted != 0 || res.Diagnostics.Quality != schemas.TraceQualityDegraded {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}
}

func TestGenericParserUsesProtocolBeforeHeuristics(t *testing.T) {
	raw := []byte(`{"schema":"bifrost.agent.event","version":"1","type":"tool_call","tool":"shell"}` + "\n")
	res, err := newGenericJSONParser(ParserCustomPython).Parse(context.Background(), &schemas.RunContext{}, raw)
	if err != nil || len(res.Events) != 1 || res.Diagnostics == nil || res.Diagnostics.Source != schemas.TraceSourceProtocol {
		t.Fatalf("result = %+v diagnostics = %+v, err = %v", res, res.Diagnostics, err)
	}
}
