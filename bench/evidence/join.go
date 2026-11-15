package evidence

import (
	"encoding/json"
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// JoinResult is the stable result of joining normalized events from all
// evidence sources. Exact duplicates are removed; conflicts stay visible.
type JoinResult struct {
	Events     []schemas.Event
	Duplicates int
	Conflicts  []schemas.ConflictRecord
	Unresolved int
}

// JoinEvents joins events using explicit identifiers first and documented
// fallback keys after that. A fallback key is always marked inferred.
func JoinEvents(input []schemas.Event) JoinResult {
	result := JoinResult{Events: make([]schemas.Event, 0, len(input))}
	byKey := map[string]int{}
	for _, original := range input {
		event := original
		key, inferred := eventJoinKey(event)
		if inferred {
			result.Unresolved++
			if event.EvidenceStrength == "" || event.EvidenceStrength == schemas.EvidenceVerified {
				event.EvidenceStrength = schemas.EvidenceInferred
			}
		}
		if event.EvidenceSource == "" {
			event.EvidenceSource = eventOrigin(event.Source)
		}
		if event.EvidenceStrength == "" {
			event.EvidenceStrength = eventStrength(event.Source)
		}
		at, ok := byKey[key]
		if !ok {
			byKey[key] = len(result.Events)
			result.Events = append(result.Events, event)
			continue
		}
		if sameEvent(result.Events[at], event) {
			result.Duplicates++
			continue
		}
		result.Conflicts = append(result.Conflicts, conflict(result.Events[at], event))
		if eventRank(event) > eventRank(result.Events[at]) {
			result.Events[at] = event
		}
	}
	return result
}

func eventJoinKey(event schemas.Event) (string, bool) {
	switch {
	case event.EventID != "":
		return "event:" + event.EventID, false
	case event.RequestID != "" && event.ToolCallID != "":
		return fmt.Sprintf("request:%s:tool:%s:type:%s", event.RequestID, event.ToolCallID, event.Type), true
	case event.AttemptID != "":
		return fmt.Sprintf("attempt:%s:type:%s", event.AttemptID, event.Type), true
	case event.RequestID != "":
		return fmt.Sprintf("request:%s:type:%s:target:%s", event.RequestID, event.Type, event.Target), true
	default:
		sum, _ := utils.HashJSON(struct {
			Type    schemas.EventType   `json:"type"`
			Source  schemas.EventSource `json:"source"`
			TS      string              `json:"ts"`
			Seq     int                 `json:"seq"`
			Summary string              `json:"summary"`
		}{event.Type, event.Source, event.TS.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"), event.Seq, event.Summary})
		return "fallback:" + sum, true
	}
}

func sameEvent(a, b schemas.Event) bool {
	aa, errA := json.Marshal(a)
	bb, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(aa) == string(bb)
}

func conflict(a, b schemas.Event) schemas.ConflictRecord {
	return schemas.ConflictRecord{
		ConflictType: "event_identity_conflict",
		Sources:      []schemas.EvidenceOrigin{eventOrigin(a.Source), eventOrigin(b.Source)},
		Values:       []string{eventSummary(a), eventSummary(b)},
		Resolution:   "authoritative source retained",
		Confidence:   schemas.EvidencePartial,
	}
}

func eventSummary(event schemas.Event) string {
	return fmt.Sprintf("%s:%s:%s", event.Type, event.Status, event.Summary)
}

func eventRank(event schemas.Event) int {
	switch event.Source {
	case schemas.SourceGrader:
		return 5
	case schemas.SourceGateway, schemas.SourceProxyLedger:
		return 4
	case schemas.SourceToolRunner, schemas.SourceShimLog, schemas.SourceWorkspace, schemas.SourceProcess:
		return 3
	case schemas.SourceAgentStream:
		return 1
	case schemas.SourceAgentReported: // Agent events area: the agent's own report, the weakest
		return 0
	default:
		return 2
	}
}

func eventOrigin(source schemas.EventSource) schemas.EvidenceOrigin {
	switch source {
	case schemas.SourceGateway:
		return schemas.EvidenceSourceBifrostLog
	case schemas.SourceProxyLedger:
		return schemas.EvidenceSourceFaultProxy
	case schemas.SourceAgentStream:
		return schemas.EvidenceSourceAgentStream
	case schemas.SourceAgentReported: // Agent events area
		return schemas.EvidenceSourceAgentReported
	case schemas.SourceToolRunner, schemas.SourceShimLog:
		return schemas.EvidenceSourceToolRunner
	case schemas.SourceWorkspace:
		return schemas.EvidenceSourceWorkspaceDiff
	case schemas.SourceProcess:
		return schemas.EvidenceSourceProcessResult
	case schemas.SourceGrader:
		return schemas.EvidenceSourceHiddenGrader
	default:
		return schemas.EvidenceSourceUnknown
	}
}

func eventStrength(source schemas.EventSource) schemas.EvidenceStrength {
	switch source {
	case schemas.SourceGrader, schemas.SourceToolRunner, schemas.SourceShimLog, schemas.SourceWorkspace, schemas.SourceProcess:
		return schemas.EvidenceVerified
	case schemas.SourceGateway, schemas.SourceProxyLedger:
		return schemas.EvidenceStrong
	case schemas.SourceAgentStream, schemas.SourceAgentReported: // Agent events area: agent-reported
		return schemas.EvidencePartial
	default:
		return schemas.EvidenceUnknown
	}
}
