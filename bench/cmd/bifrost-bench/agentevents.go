package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Agent events row (docs/agent-events.md): whether the run's event collector
// served and what the agent reported about itself. Reporting is optional, so
// this row warns at most; it never fails a check.
const rowAgentEvents = "agent_events"

func init() {
	rowTitles[rowAgentEvents] = "agent reported its own steps (OTLP or native events)"
}

const agentEventsFix = "optional: emit OpenTelemetry spans (OTEL_* is set for the agent) or native events to BENCH_EVENTS_URL / BENCH_EVENTS_FILE; see docs/agent-events.md"

// agentEventsRow reads the run's agent event status.
func agentEventsRow(in *checkInput) checkRow {
	row := newRow(rowAgentEvents)
	st := in.ev.AgentEvents
	switch {
	case st == nil:
		return row.set(checkSkip, "no event collector ran and nothing was reported (events.collector switched off): gateway and observer evidence only", "set events.collector: true in deploy/bench.yaml (the default) to receive agent events")
	case st.Kept == 0 && st.Collector == "missing":
		return row.set(checkWarn, "the event collector did not start ("+st.Reason+"); the agent reported nothing through the file or stdout channel: gateway and observer evidence only", agentEventsFix)
	case st.Kept == 0:
		return row.set(checkWarn, "collector "+st.Collector+"; the agent reported nothing (gateway and observer evidence only)", agentEventsFix)
	}
	detail := fmt.Sprintf("%d events received (%s; kinds %s)", st.Kept, countList(st.Channels), countList(st.Kinds))
	if st.Collector == "on" {
		detail = "collector reachable at http://toolproxy:9200; " + detail
	}
	if st.Dropped > 0 {
		detail += fmt.Sprintf("; %d dropped at a cap or unparseable", st.Dropped)
		return row.set(checkWarn, detail, "send fewer or smaller events: at most 5000 events, 8 MiB, 4 MiB per request per run")
	}
	return row.set(checkPass, detail, "")
}

// countList renders a count map as "a 2, b 1", largest first.
func countList(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, m[k]))
	}
	return strings.Join(parts, ", ")
}

// reportsEvents is the agents page evidence note for an agent whose check
// run reported events: which channels it used.
func reportsEvents(st *schemas.AgentEventStatus) string {
	if st == nil || st.Kept == 0 {
		return ""
	}
	var ch []string
	if st.Channels[schemas.ReportChannelOTLP] > 0 {
		ch = append(ch, "OTLP")
	}
	if st.Channels[schemas.ReportChannelNative]+st.Channels[schemas.ReportChannelFile]+st.Channels[schemas.ReportChannelStdout] > 0 {
		ch = append(ch, "native")
	}
	return "reports events (" + strings.Join(ch, " | ") + ")"
}
