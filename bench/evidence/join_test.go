package evidence

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestJoinEventsDeduplicatesAndKeepsAuthoritativeConflict(t *testing.T) {
	first := schemas.Event{EventID: "e1", Source: schemas.SourceAgentStream, Type: schemas.EventToolResult, Summary: "agent says ok"}
	second := first
	second.Source = schemas.SourceToolRunner
	second.Summary = "runner says failed"
	joined := JoinEvents([]schemas.Event{first, first, second})
	if len(joined.Events) != 1 || joined.Duplicates != 1 || len(joined.Conflicts) != 1 {
		t.Fatalf("join result = %+v", joined)
	}
	if joined.Events[0].Source != schemas.SourceToolRunner || joined.Conflicts[0].Confidence != schemas.EvidencePartial {
		t.Fatalf("authoritative conflict result = %+v", joined)
	}
}

func TestJoinEventsMarksFallbackIdentity(t *testing.T) {
	joined := JoinEvents([]schemas.Event{{Type: schemas.EventClaim, Source: schemas.SourceAgentStream, Seq: 1, Summary: "done"}})
	if len(joined.Events) != 1 || joined.Events[0].EvidenceStrength != schemas.EvidenceInferred || joined.Unresolved != 1 {
		t.Fatalf("fallback join = %+v", joined)
	}
}
