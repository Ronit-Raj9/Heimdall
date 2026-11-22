package webhooks

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Delivery formats (lane Y05). A json hook gets the signed Event; the
// others get the body their service expects, built from the event's
// Summary, and still carry the X-Heimdall-* signature headers.
const (
	FormatJSON      = "json"      // the Event as JSON
	FormatSlack     = "slack"     // a Slack incoming webhook: Block Kit message
	FormatTeams     = "teams"     // a Microsoft Teams workflow webhook: Adaptive Card message
	FormatPagerDuty = "pagerduty" // PagerDuty Events API v2: trigger with a dedup key
)

// Formats lists the delivery formats.
var Formats = []string{FormatJSON, FormatSlack, FormatTeams, FormatPagerDuty}

// PagerDutyEventsURL is the Events API v2 endpoint a pagerduty hook posts to
// unless another URL is given (an EU account uses events.eu.pagerduty.com).
const PagerDutyEventsURL = "https://events.pagerduty.com/v2/enqueue"

var routingKeyRe = regexp.MustCompile(`^[A-Za-z0-9]{20,64}$`)

func routingKeyID(id string) string { return id + "/routing_key" }

// Severities of a Summary (PagerDuty's four).
const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityError    = "error"
	SeverityCritical = "critical"
)

// Field is one labelled value of a Summary.
type Field struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Summary is the human side of an event: what chat and paging formats
// show. Title is one line; Fields are short facts (score, change, cost).
type Summary struct {
	Title    string  `json:"title"`
	Text     string  `json:"text,omitempty"`
	Severity string  `json:"severity"`
	Link     string  `json:"link,omitempty"`
	Fields   []Field `json:"fields,omitempty"`
	// DedupKey groups repeats of one problem into one PagerDuty incident.
	DedupKey string `json:"dedup_key,omitempty"`
}

func (h Hook) format() string {
	if h.Format == "" {
		return FormatJSON
	}
	return h.Format
}

// pagingEvents are the events a PagerDuty hook takes: problems someone may
// need to act on, never routine finishes.
var pagingEvents = []string{EventEvaluationRegressed, EventReleaseBlocked, EventScheduleFailed, EventWorkerOffline, EventJobFailed}

// EventsFor lists the events a hook of format f can subscribe to.
func EventsFor(f string) []string {
	if f == FormatPagerDuty {
		return pagingEvents
	}
	return Events
}

// Accepts reports whether a hook of format f can subscribe to event e.
func Accepts(f, e string) bool { return slices.Contains(EventsFor(f), e) }

// DefaultEvents is what a new hook of format f subscribes to when the
// request names no events.
func DefaultEvents(f string) []string {
	switch f {
	case FormatPagerDuty:
		return []string{EventEvaluationRegressed}
	case FormatSlack, FormatTeams:
		return []string{EventEvaluationFinished, EventEvaluationRegressed, EventReleaseBlocked}
	}
	// A json hook keeps the default it always had, for API clients that
	// rely on it.
	return []string{EventExperimentFinished}
}

// summaryOf is ev's Summary, or a plain one built from its type.
func summaryOf(ev Event) Summary {
	if ev.Summary != nil {
		s := *ev.Summary
		if s.Severity == "" {
			s.Severity = SeverityInfo
		}
		return s
	}
	s := Summary{Title: "Heimdall: " + ev.Type, Severity: SeverityInfo}
	if ev.Type == EventPing {
		s.Title, s.Text = "Test message from Heimdall", "This hook is set up. Heimdall sends evaluation results and alerts here."
	}
	return s
}

// Render is the body hook h receives for ev.
func Render(h Hook, ev Event) ([]byte, error) {
	switch h.format() {
	case FormatSlack:
		return json.Marshal(slackMessage(ev))
	case FormatTeams:
		return json.Marshal(teamsMessage(ev))
	case FormatPagerDuty:
		if h.RoutingKey == "" {
			return nil, fmt.Errorf("webhook %s has no PagerDuty routing key", h.ID)
		}
		return json.Marshal(pagerDutyEvent(h, ev))
	}
	return json.Marshal(ev)
}

// ---- Slack: Block Kit (https://api.slack.com/block-kit) ----

func slackEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n - 3
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

func slackMessage(ev Event) map[string]any {
	s := summaryOf(ev)
	text := s.Title
	if s.Text != "" {
		text += ": " + s.Text
	}
	blocks := []any{map[string]any{"type": "header", "text": map[string]any{"type": "plain_text", "text": clip(s.Title, 150), "emoji": false}}}
	if s.Text != "" {
		blocks = append(blocks, map[string]any{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": clip(slackEscape(s.Text), 3000)}})
	}
	if len(s.Fields) > 0 {
		var fs []any
		for _, f := range s.Fields[:min(len(s.Fields), 10)] {
			fs = append(fs, map[string]any{"type": "mrkdwn", "text": clip("*"+slackEscape(f.Label)+"*\n"+slackEscape(f.Value), 2000)})
		}
		blocks = append(blocks, map[string]any{"type": "section", "fields": fs})
	}
	if s.Link != "" {
		blocks = append(blocks, map[string]any{"type": "actions", "elements": []any{map[string]any{
			"type": "button", "text": map[string]any{"type": "plain_text", "text": "Open in Heimdall"}, "url": s.Link, "style": "primary"}}})
	}
	blocks = append(blocks, map[string]any{"type": "context", "elements": []any{map[string]any{"type": "mrkdwn",
		"text": slackEscape("Heimdall " + ev.Type + " at " + ev.At.UTC().Format("2006-01-02 15:04 UTC"))}}})
	return map[string]any{"text": clip(text, 3000), "blocks": blocks}
}

// ---- Microsoft Teams: Adaptive Card in a message (workflow webhooks) ----

func teamsMessage(ev Event) map[string]any {
	s := summaryOf(ev)
	color := "Default"
	switch s.Severity {
	case SeverityError, SeverityCritical:
		color = "Attention"
	case SeverityWarning:
		color = "Warning"
	}
	body := []any{
		map[string]any{"type": "TextBlock", "text": "Heimdall", "size": "Small", "weight": "Bolder", "isSubtle": true},
		map[string]any{"type": "TextBlock", "text": s.Title, "size": "Large", "weight": "Bolder", "wrap": true, "color": color},
	}
	if s.Text != "" {
		body = append(body, map[string]any{"type": "TextBlock", "text": s.Text, "wrap": true})
	}
	if len(s.Fields) > 0 {
		var facts []any
		for _, f := range s.Fields {
			facts = append(facts, map[string]any{"title": f.Label, "value": f.Value})
		}
		body = append(body, map[string]any{"type": "FactSet", "facts": facts})
	}
	card := map[string]any{"$schema": "http://adaptivecards.io/schemas/adaptive-card.json", "type": "AdaptiveCard", "version": "1.4", "body": body}
	if s.Link != "" {
		card["actions"] = []any{map[string]any{"type": "Action.OpenUrl", "title": "Open in Heimdall", "url": s.Link}}
	}
	return map[string]any{"type": "message", "summary": clip(s.Title, 200),
		"attachments": []any{map[string]any{"contentType": "application/vnd.microsoft.card.adaptive", "contentUrl": nil, "content": card}}}
}

// ---- PagerDuty Events API v2 ----

func pagerDutyEvent(h Hook, ev Event) map[string]any {
	s := summaryOf(ev)
	if ev.Type == EventPing {
		// A resolve for a key no incident has checks the routing key
		// (PagerDuty answers 202, or 400 for a bad key) and pages nobody.
		return map[string]any{"routing_key": h.RoutingKey, "event_action": "resolve", "dedup_key": "heimdall-test-" + h.ID}
	}
	sev := s.Severity
	if !slices.Contains([]string{SeverityInfo, SeverityWarning, SeverityError, SeverityCritical}, sev) {
		sev = SeverityError
	}
	dedup := s.DedupKey
	if dedup == "" {
		dedup = "heimdall-" + ev.ID
	}
	details := map[string]any{"event": ev.Type, "event_id": ev.ID}
	for _, f := range s.Fields {
		details[f.Label] = f.Value
	}
	if s.Text != "" {
		details["detail"] = s.Text
	}
	out := map[string]any{
		"routing_key":  h.RoutingKey,
		"event_action": "trigger",
		"dedup_key":    clip(dedup, 255),
		"client":       "Heimdall",
		"payload": map[string]any{
			"summary":        clip(s.Title, 1024),
			"source":         "heimdall",
			"severity":       sev,
			"timestamp":      ev.At.UTC().Format(time.RFC3339),
			"class":          ev.Type,
			"group":          ev.OrgID,
			"custom_details": details,
		},
	}
	if s.Link != "" {
		out["client_url"] = s.Link
		out["links"] = []any{map[string]any{"href": s.Link, "text": "Open in Heimdall"}}
	}
	return out
}
