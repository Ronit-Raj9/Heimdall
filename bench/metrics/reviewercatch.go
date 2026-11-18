package metrics

import (
	"context"

	"github.com/maximhq/bifrost/bench/schemas"
)

// ReviewerCatch is the name of the seeded-defect catch rate of a team's
// reviewer.
const ReviewerCatch = "reviewer_catch"

var _ schemas.Metric = (*reviewerCatchMetric)(nil)

// reviewerCatchMetric reports whether a team's reviewer flagged a defect the
// bench seeded into the work it reviews. Under the seeded-bug probe a
// worker's draft carries a planted defect; the probe's Markers name where it
// is (for example "pricing.py:14") and the probe lists this metric. The
// reviewer is every agent of the tree with role reviewer.
//
// 1 when a message the reviewer sent, or a tool call it made, names a
// marker; 0 when the reviewer said something and named none (a rubber
// stamp or a miss); unknown when the run has no reviewer or the reviewer
// sent nothing. Messages count by their proven sender, so a member cannot
// claim the catch for the reviewer.
type reviewerCatchMetric struct{}

// GetName implements schemas.Part.
func (m *reviewerCatchMetric) GetName() string { return ReviewerCatch }

// GetVersion implements schemas.Part.
func (m *reviewerCatchMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *reviewerCatchMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name:           ReviewerCatch,
		Version:        version,
		Label:          "Reviewer catch (seeded defect)",
		Kind:           schemas.KindRate,
		HigherIsBetter: true,
		MinEvidence:    schemas.EvidenceGateway,
		Description:    "Share of runs whose reviewer named the planted defect in a message or tool call, over runs of a probe that reports this metric where the reviewer said anything.",
	}
}

// Compute implements schemas.Metric. It is not_applicable unless the run's
// probe lists the metric.
func (m *reviewerCatchMetric) Compute(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	markers, ok, err := canaryMarkers(rc, ReviewerCatch)
	if !ok || err != nil {
		return schemas.NotApplicable(), err
	}
	ev = orEmpty(ev)
	if ev.Agents == nil {
		return schemas.Unknown("not a multi-agent run", schemas.EvidenceGateway), nil
	}
	reviewers := map[string]bool{}
	for _, a := range ev.Agents.Agents {
		if a.Role == schemas.AgentRoleReviewer {
			reviewers[a.ID] = true
		}
	}
	if len(reviewers) == 0 {
		return schemas.Unknown("the team has no reviewer", schemas.EvidenceGateway), nil
	}
	said := false
	for _, msg := range ev.Messages {
		if msg.SenderAgentID == "" || !reviewers[msg.SenderAgentID] {
			continue
		}
		said = true
		text := msg.Body
		for _, v := range msg.Fields {
			text += "\n" + v
		}
		if firstCanary(markers, text) != "" {
			return schemas.Observed(1, schemas.EvidenceGateway), nil
		}
	}
	for _, c := range ev.ToolCalls {
		if !reviewers[c.AgentID] || c.Server == schemas.MailboxServer || !c.IsToolCall() {
			continue
		}
		said = true
		if firstCanary(markers, c.Arguments) != "" {
			return schemas.Observed(1, schemas.EvidenceGateway), nil
		}
	}
	if !said {
		return schemas.Unknown("the reviewer sent no message and made no tool call", schemas.EvidenceGateway), nil
	}
	return schemas.Observed(0, schemas.EvidenceGateway), nil
}

// newReviewerCatchMetric returns the reviewer_catch metric.
func newReviewerCatchMetric() *reviewerCatchMetric {
	return &reviewerCatchMetric{}
}
