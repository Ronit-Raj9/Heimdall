package metrics

import (
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
)

// version is the version every built-in metric reports. A metric that changes
// its definition gets its own constant and a new version.
const version = "1.0.0"

// Gateway request kinds and statuses the metrics read (schemas.GatewayRequest).
const (
	kindMessage       = "message"
	kindAudio         = "audio" // X8 voice agents: speech, transcription and Realtime turn rows
	statusSuccess     = "success"
	statusProcessing  = "processing"
	reasonNoLog       = "no gateway request log"
	reasonIncomplete  = "gateway request log incomplete"
	reasonNoGrade     = "no grade"
	reasonNoClaim     = "no claim evidence"
	reasonUndecidable = "grader could not decide"
)

// logicalRequest is one model request as the agent issued it: the root row
// plus every fallback row Bifrost created for it. Retries inside one row are
// its attempts.
type logicalRequest struct {
	root       string
	rows       []schemas.GatewayRequest
	succeeded  bool // some row returned a response
	processing bool // some row has not finished
}

// requestLogProblem returns an unknown value and true when the gateway log
// cannot support a count: it is missing or Bifrost has not written every row.
// Counting an incomplete log would report a lower bound as if it were exact.
func requestLogProblem(log *schemas.RequestLog) (schemas.Value, bool) {
	if log == nil {
		return schemas.Unknown(reasonNoLog, schemas.EvidenceGateway), true
	}
	if !log.Complete {
		reason := reasonIncomplete
		if log.Reason != "" {
			reason = fmt.Sprintf("%s: %s", reasonIncomplete, log.Reason)
		}
		return schemas.Unknown(reason, schemas.EvidenceGateway), true
	}
	return schemas.Value{}, false
}

// logicalMessageRequests groups the message rows of a request log into
// logical requests, in order of first appearance. Bifrost writes a fallback
// attempt as its own row whose parent is the original request, so rows that
// share a root are one request. A root of "parent = self" is a root too.
func logicalMessageRequests(reqs []schemas.GatewayRequest) []logicalRequest {
	byID := make(map[string]schemas.GatewayRequest, len(reqs))
	for _, r := range reqs {
		if r.ID != "" {
			byID[r.ID] = r
		}
	}
	index := make(map[string]int)
	var out []logicalRequest
	for i, r := range reqs {
		if r.Kind != kindMessage {
			continue
		}
		root := rootID(r, byID)
		if root == "" {
			root = fmt.Sprintf("#%d", i)
		}
		at, ok := index[root]
		if !ok {
			at = len(out)
			index[root] = at
			out = append(out, logicalRequest{root: root})
		}
		out[at].rows = append(out[at].rows, r)
		switch r.Status {
		case statusSuccess:
			out[at].succeeded = true
		case statusProcessing:
			out[at].processing = true
		}
	}
	return out
}

// messageRows returns the rows of kind message, in log order.
func messageRows(reqs []schemas.GatewayRequest) []schemas.GatewayRequest {
	var out []schemas.GatewayRequest
	for _, r := range reqs {
		if r.Kind == kindMessage {
			out = append(out, r)
		}
	}
	return out
}

// firedVeto returns the name of the first veto gate that fired. A fired veto
// makes the run's outcome integrity_violation, which scores 0 whatever the
// grade says.
func firedVeto(gates []schemas.GateResult) (string, bool) {
	for _, g := range gates {
		if g.Effect == schemas.GateVeto && g.Fired {
			return g.Gate, true
		}
	}
	return "", false
}

// gradeProblem returns an unknown value and true when the grade cannot decide
// pass or fail.
func gradeProblem(g *schemas.Grade) (schemas.Value, bool) {
	if g == nil {
		return schemas.Unknown(reasonNoGrade, schemas.EvidenceOutcome), true
	}
	if g.InvalidEvaluator {
		reason := reasonUndecidable
		if g.Reason != "" {
			reason = fmt.Sprintf("%s: %s", reasonUndecidable, g.Reason)
		}
		return schemas.Unknown(reason, schemas.EvidenceOutcome), true
	}
	return schemas.Value{}, false
}

// claimLevel is the evidence level of a value that combines the grade with
// the agent's claim: the finer of outcome and where the claim was read.
func claimLevel(c *schemas.Claim) schemas.EvidenceLevel {
	if c == nil || levelRank(c.Source) <= levelRank(schemas.EvidenceOutcome) {
		return schemas.EvidenceOutcome
	}
	return c.Source
}

// orEmpty lets Compute treat a nil evidence pointer as evidence with nothing
// collected, so every field reads as missing rather than panicking.
func orEmpty(ev *schemas.Evidence) *schemas.Evidence {
	if ev == nil {
		return &schemas.Evidence{}
	}
	return ev
}

// rootID follows ParentID links to the first row of a logical request. When
// the parent row is not in the log, the parent id itself names the request.
// The visited set stops on self-parented rows and malformed cycles.
func rootID(r schemas.GatewayRequest, byID map[string]schemas.GatewayRequest) string {
	id, parent := r.ID, r.ParentID
	seen := map[string]bool{id: true}
	for parent != "" && !seen[parent] {
		p, ok := byID[parent]
		if !ok {
			return parent
		}
		seen[parent] = true
		id, parent = p.ID, p.ParentID
	}
	return id
}

// levelRank orders evidence levels from outcome (0) to boundary (3). Unknown
// levels rank below outcome.
func levelRank(l schemas.EvidenceLevel) int {
	switch l {
	case schemas.EvidenceOutcome:
		return 0
	case schemas.EvidenceGateway:
		return 1
	case schemas.EvidenceNative:
		return 2
	case schemas.EvidenceBoundary:
		return 3
	default:
		return -1
	}
}
